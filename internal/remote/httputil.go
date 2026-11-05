package remote

import (
	"bytes"
	"context"
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
	"sync"
	"time"
)

const (
	// MaxBodyBytes is the largest request body any endpoint accepts.
	MaxBodyBytes = 1 << 20
	// DefaultAddr is the default hub listen address (loopback only).
	DefaultAddr      = "127.0.0.1:7777"
	maxResponseBytes = 8 << 20
)

// ErrFingerprintMismatch is returned when the server certificate does not
// match the pinned fingerprint.
var ErrFingerprintMismatch = errors.New("parent certificate fingerprint does not match the pinned fingerprint (possible man-in-the-middle, or the parent identity was regenerated)")

// APIError is a non-2xx response.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	switch e.Status {
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusTooManyRequests:
		return "too many failed attempts, try again later"
	}
	if e.Message != "" {
		return fmt.Sprintf("%s (HTTP %d)", e.Message, e.Status)
	}
	return fmt.Sprintf("HTTP %d", e.Status)
}

// StatusOf returns the HTTP status of an *APIError in err's chain, or 0.
func StatusOf(err error) int {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Status
	}
	return 0
}

// pinnedClient returns an HTTPS client that accepts only the certificate whose
// SHA-256 equals fingerprint. System CAs are never consulted, proxies are not
// used and redirects are not followed.
func pinnedClient(fingerprint string, timeout time.Duration) (*http.Client, error) {
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
		ResponseHeaderTimeout: timeout,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          2,
		IdleConnTimeout:       90 * time.Second,
	}
	return &http.Client{
		Transport: tr,
		Timeout:   timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

// doJSON sends in (if non-nil) as JSON with a bearer token (if non-empty) and
// decodes a 2xx body into out (if non-nil). It returns the status code.
func doJSON(ctx context.Context, c *http.Client, method, url, token string, in, out any) (int, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return 0, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		if errors.Is(err, ErrFingerprintMismatch) {
			return 0, ErrFingerprintMismatch
		}
		return 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return resp.StatusCode, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e errorResponse
		_ = json.Unmarshal(data, &e)
		return resp.StatusCode, &APIError{Status: resp.StatusCode, Message: e.Error}
	}
	if out != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, fmt.Errorf("invalid response: %w", err)
		}
	}
	return resp.StatusCode, nil
}

func newTLSServer(h http.Handler, cert tls.Certificate) *http.Server {
	return &http.Server{
		Handler: h,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS13,
		},
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      45 * time.Second, // > long-poll and relay timeouts
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          discardLogger(),
	}
}

func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(dst)
	if err == nil && dec.More() {
		err = errors.New("trailing data")
	}
	if err == nil {
		_, err = io.Copy(io.Discard, r.Body) // detect oversized tails
	}
	var mbe *http.MaxBytesError
	switch {
	case errors.As(err, &mbe):
		writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return false
	case err != nil:
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

type errorResponse struct {
	Error string `json:"error"`
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) <= len(prefix) || h[:len(prefix)] != prefix {
		return ""
	}
	return h[len(prefix):]
}

// authLimiter locks out an IP after max consecutive auth failures, doubling
// the lockout on each further failure (1s, 2s, 4s ... capped at 5 minutes).
type authLimiter struct {
	mu      sync.Mutex
	max     int
	now     func() time.Time
	entries map[string]*authEntry
}

type authEntry struct {
	failures    int
	lockedUntil time.Time
	last        time.Time
}

const (
	maxLockout      = 5 * time.Minute
	limiterEntryTTL = time.Hour
	limiterMaxSize  = 10000
)

func newAuthLimiter(max int, now func() time.Time) *authLimiter {
	return &authLimiter{max: max, now: now, entries: map[string]*authEntry{}}
}

func (l *authLimiter) blocked(ip string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e := l.entries[ip]; e != nil {
		if d := e.lockedUntil.Sub(l.now()); d > 0 {
			return d
		}
	}
	return 0
}

func (l *authLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.gc(now)
	e := l.entries[ip]
	if e == nil {
		e = &authEntry{}
		l.entries[ip] = e
	}
	e.failures++
	e.last = now
	if over := e.failures - l.max; over >= 0 {
		d := maxLockout
		if over < 9 {
			d = time.Second << over
		}
		if d > maxLockout {
			d = maxLockout
		}
		e.lockedUntil = now.Add(d)
	}
}

func (l *authLimiter) success(ip string) {
	l.mu.Lock()
	delete(l.entries, ip)
	l.mu.Unlock()
}

// gc keeps the table bounded under a flood of source addresses.
func (l *authLimiter) gc(now time.Time) {
	if len(l.entries) < limiterMaxSize {
		return
	}
	for ip, e := range l.entries {
		if now.Sub(e.last) > limiterEntryTTL || !e.lockedUntil.After(now) {
			delete(l.entries, ip)
		}
	}
}
