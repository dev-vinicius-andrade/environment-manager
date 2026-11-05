package update

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	maxAPIBody      = 4 << 20
	maxChecksums    = 1 << 20
	maxArchive      = 200 << 20
	checksumsAsset  = "checksums.txt"
	defaultAPIBase  = "https://api.github.com"
	defaultUserAgnt = "envmgr-updater"
)

// Asset is one file attached to a release.
type Asset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
	Size        int64  `json:"size"`
}

// Release is the part of a GitHub release the updater needs.
type Release struct {
	Tag        string  `json:"tag_name"`
	Name       string  `json:"name"`
	Body       string  `json:"body"`
	URL        string  `json:"html_url"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}

// Version parses the release tag.
func (r Release) Version() (Version, error) { return ParseVersion(r.Tag) }

// Asset returns the asset with this exact name.
func (r Release) Asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// AssetName is the archive name the release workflow publishes for a target.
func AssetName(version, goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("envmgr_%s_%s_%s%s", version, goos, goarch, ext)
}

// BinaryName is the executable inside the archive.
func BinaryName(goos string) string {
	if goos == "windows" {
		return "envmgr.exe"
	}
	return "envmgr"
}

// Client talks to the GitHub Releases API and downloads assets.
type Client struct {
	Repo    string
	APIBase string // default https://api.github.com
	Token   string // optional; sent only to APIBase
	HTTP    *http.Client
	// AllowURL decides which asset URLs may be downloaded; the default allows
	// https URLs on github.com and *.githubusercontent.com. Tests override it.
	AllowURL func(*url.URL) bool
}

// NewClient returns a client for repo with sane timeouts.
func NewClient(repo, token string) *Client {
	return &Client{Repo: repo, Token: token, HTTP: &http.Client{Timeout: 5 * time.Minute}}
}

func (c *Client) apiBase() string {
	if c.APIBase != "" {
		return strings.TrimRight(c.APIBase, "/")
	}
	return defaultAPIBase
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

func defaultAllowURL(u *url.URL) bool {
	if u.Scheme != "https" {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return h == "github.com" || strings.HasSuffix(h, ".githubusercontent.com")
}

func (c *Client) allowed(u *url.URL) bool {
	if c.AllowURL != nil {
		return c.AllowURL(u)
	}
	return defaultAllowURL(u)
}

func (c *Client) getJSON(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase()+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", defaultUserAgnt)
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ErrNoRelease
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("GitHub refused the request (%s): probably the rate limit; set GITHUB_TOKEN and retry", resp.Status)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub answered %s for %s", resp.Status, path)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxAPIBody)).Decode(v)
}

// ErrNoRelease is returned when no matching release exists.
var ErrNoRelease = errors.New("no matching release found")

// Latest returns the newest release: the latest stable one, or with
// prerelease the newest non-draft release of any kind.
func (c *Client) Latest(ctx context.Context, prerelease bool) (Release, error) {
	if !prerelease {
		var r Release
		err := c.getJSON(ctx, "/repos/"+c.Repo+"/releases/latest", &r)
		return r, err
	}
	var rs []Release
	if err := c.getJSON(ctx, "/repos/"+c.Repo+"/releases?per_page=20", &rs); err != nil {
		return Release{}, err
	}
	var best Release
	var bestV Version
	found := false
	for _, r := range rs {
		v, err := r.Version()
		if r.Draft || err != nil {
			continue
		}
		if !found || v.Compare(bestV) > 0 {
			best, bestV, found = r, v, true
		}
	}
	if !found {
		return Release{}, ErrNoRelease
	}
	return best, nil
}

// ByTag returns the release for a version ("1.2.3" or "v1.2.3").
func (c *Client) ByTag(ctx context.Context, version string) (Release, error) {
	v, err := ParseVersion(version)
	if err != nil {
		return Release{}, err
	}
	var r Release
	err = c.getJSON(ctx, "/repos/"+c.Repo+"/releases/tags/v"+v.String(), &r)
	return r, err
}

// download streams an asset into w and returns its SHA-256 and size. The size
// is capped so a hostile server cannot fill the disk.
func (c *Client) download(ctx context.Context, a Asset, limit int64, w io.Writer) (sum string, n int64, err error) {
	u, err := url.Parse(a.DownloadURL)
	if err != nil || !c.allowed(u) {
		return "", 0, fmt.Errorf("refusing to download %q: not an https GitHub URL", a.DownloadURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.DownloadURL, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("User-Agent", defaultUserAgnt)
	hc := *c.httpClient()
	hc.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if !c.allowed(r.URL) {
			return fmt.Errorf("refusing redirect to %s", r.URL.Redacted())
		}
		return nil
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("downloading %s: %s", a.Name, resp.Status)
	}
	h := sha256.New()
	n, err = io.Copy(io.MultiWriter(w, h), io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return "", n, err
	}
	if n > limit {
		return "", n, fmt.Errorf("%s is larger than %d bytes; refusing it", a.Name, limit)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// Checksums downloads and parses the release's checksums.txt.
func (c *Client) Checksums(ctx context.Context, r Release) (map[string]string, error) {
	a, ok := r.Asset(checksumsAsset)
	if !ok {
		return nil, fmt.Errorf("release %s has no %s; refusing to install an unverified binary", r.Tag, checksumsAsset)
	}
	var sb strings.Builder
	if _, _, err := c.download(ctx, a, maxChecksums, &sb); err != nil {
		return nil, err
	}
	return ParseChecksums(sb.String())
}

// ParseChecksums reads sha256sum output ("<hex>  <name>", name may start with *).
func ParseChecksums(s string) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 2 {
			continue
		}
		sum := strings.ToLower(f[0])
		if len(sum) != 64 {
			continue
		}
		if _, err := hex.DecodeString(sum); err != nil {
			continue
		}
		out[strings.TrimPrefix(f[1], "*")] = sum
	}
	if len(out) == 0 {
		return nil, errors.New("checksums file has no entries")
	}
	return out, nil
}

// DownloadVerified downloads the archive into a temp file and checks its
// SHA-256 against want. It returns the file path; the caller removes it.
func (c *Client) DownloadVerified(ctx context.Context, a Asset, want, dir string) (string, error) {
	f, err := os.CreateTemp(dir, "envmgr-update-*")
	if err != nil {
		return "", err
	}
	path := f.Name()
	sum, _, err := c.download(ctx, a, maxArchive, f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && !strings.EqualFold(sum, want) {
		err = fmt.Errorf("checksum mismatch for %s: expected %s, got %s", a.Name, want, sum)
	}
	if err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}
