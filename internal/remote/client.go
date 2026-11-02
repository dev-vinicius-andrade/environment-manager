package remote

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
)

const maxResponseBytes = 8 << 20

// ErrFingerprintMismatch is returned when the server certificate does not
// match the pinned fingerprint.
var ErrFingerprintMismatch = errors.New("server certificate fingerprint does not match the pinned fingerprint (possible man-in-the-middle, or the server identity was regenerated)")

// APIError is a non-2xx response from the server.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	switch e.Status {
	case http.StatusUnauthorized:
		return "remote: unauthorized (check the token)"
	case http.StatusTooManyRequests:
		return "remote: too many failed attempts, try again later"
	}
	if e.Message != "" {
		return fmt.Sprintf("remote: %s (HTTP %d)", e.Message, e.Status)
	}
	return fmt.Sprintf("remote: HTTP %d", e.Status)
}

// IsNotFound reports whether err is a 404 from the server.
func IsNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusNotFound
}

// Client talks to an envmgr serve instance over HTTPS with a pinned cert.
type Client struct {
	base  *url.URL
	token string
	http  *http.Client
}

// NewClient returns a client for address (host:port). fingerprint is the
// server certificate SHA-256 and is mandatory: no other certificate is
// accepted, and system CAs are never consulted.
func NewClient(address, token, fingerprint string) (*Client, error) {
	if _, _, err := net.SplitHostPort(address); err != nil {
		return nil, fmt.Errorf("invalid address %q: %w", address, err)
	}
	if token == "" {
		return nil, errors.New("missing token")
	}
	fp, err := NormalizeFingerprint(fingerprint)
	if err != nil {
		return nil, err
	}
	want, _ := hex.DecodeString(fp)
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS13,
		// Chain/hostname verification is replaced by the pin check below,
		// which runs on every handshake; an unpinned cert is always rejected.
		InsecureSkipVerify: true, //nolint:gosec
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return ErrFingerprintMismatch
			}
			got := sha256.Sum256(rawCerts[0])
			if subtle.ConstantTimeCompare(got[:], want) != 1 {
				return ErrFingerprintMismatch
			}
			return nil
		},
	}
	tr := &http.Transport{
		Proxy:                 nil,
		TLSClientConfig:       tlsCfg,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          2,
		IdleConnTimeout:       30 * time.Second,
	}
	return &Client{
		base:  &url.URL{Scheme: "https", Host: address},
		token: token,
		http: &http.Client{
			Transport: tr,
			Timeout:   60 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

// List returns the remote profile names.
func (c *Client) List() ([]string, error) {
	var out listResponse
	if err := c.do(http.MethodGet, "/v1/profiles", nil, &out); err != nil {
		return nil, err
	}
	return out.Profiles, nil
}

// Get fetches one remote profile.
func (c *Client) Get(name string) (profile.Profile, error) {
	if !profile.ValidName(name) {
		return profile.Profile{}, fmt.Errorf("invalid profile name %q", name)
	}
	var p profile.Profile
	if err := c.do(http.MethodGet, "/v1/profiles/"+name, nil, &p); err != nil {
		return profile.Profile{}, err
	}
	if p.Vars == nil {
		p.Vars = map[string]string{}
	}
	p.Name = name
	return p, p.Validate()
}

// Put creates or overwrites a remote profile.
func (c *Client) Put(p profile.Profile) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if p.Vars == nil {
		p.Vars = map[string]string{}
	}
	return c.do(http.MethodPut, "/v1/profiles/"+p.Name, p, nil)
}

// Delete removes a remote profile.
func (c *Client) Delete(name string) error {
	if !profile.ValidName(name) {
		return fmt.Errorf("invalid profile name %q", name)
	}
	return c.do(http.MethodDelete, "/v1/profiles/"+name, nil, nil)
}

// SetVar sets one variable in a remote profile, creating the profile if needed.
func (c *Client) SetVar(name, key, value string) error {
	if err := checkNames(name, key); err != nil {
		return err
	}
	return c.do(http.MethodPut, "/v1/profiles/"+name+"/vars/"+key, varRequest{Value: &value}, nil)
}

// UnsetVar removes one variable from a remote profile.
func (c *Client) UnsetVar(name, key string) error {
	if err := checkNames(name, key); err != nil {
		return err
	}
	return c.do(http.MethodDelete, "/v1/profiles/"+name+"/vars/"+key, nil, nil)
}

func checkNames(name, key string) error {
	if !profile.ValidName(name) {
		return fmt.Errorf("invalid profile name %q", name)
	}
	if !profile.ValidVar(key) {
		return fmt.Errorf("invalid variable name %q", key)
	}
	return nil
}

func (c *Client) do(method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	u := *c.base
	u.Path = path
	req, err := http.NewRequest(method, u.String(), body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, ErrFingerprintMismatch) {
			return ErrFingerprintMismatch
		}
		return fmt.Errorf("remote: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("remote: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e errorResponse
		_ = json.Unmarshal(data, &e)
		return &APIError{Status: resp.StatusCode, Message: e.Error}
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("remote: invalid response: %w", err)
		}
	}
	return nil
}
