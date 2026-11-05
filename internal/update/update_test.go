package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAndCompare(t *testing.T) {
	order := []string{"0.9.0", "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.0.1", "1.2.0", "2.0.0"}
	for i := range order {
		for j := range order {
			a, err := ParseVersion(order[i])
			if err != nil {
				t.Fatal(err)
			}
			b, _ := ParseVersion(order[j])
			want := cmpInt(i, j)
			if got := a.Compare(b); got != want {
				t.Errorf("%s vs %s = %d, want %d", order[i], order[j], got, want)
			}
		}
	}
	if v, _ := ParseVersion("v1.2.3+build5"); v.String() != "1.2.3" {
		t.Errorf("metadata not ignored: %s", v)
	}
	for _, bad := range []string{"", "1.2", "1.2.x", "01.2.3", "1.2.3-", "dev", "-1.0.0"} {
		if _, err := ParseVersion(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestAssetNames(t *testing.T) {
	if got := AssetName("1.2.3", "linux", "arm64"); got != "envmgr_1.2.3_linux_arm64.tar.gz" {
		t.Error(got)
	}
	if got := AssetName("1.2.3", "windows", "amd64"); got != "envmgr_1.2.3_windows_amd64.zip" {
		t.Error(got)
	}
	if BinaryName("windows") != "envmgr.exe" || BinaryName("linux") != "envmgr" {
		t.Error("binary names")
	}
}

func TestParseChecksums(t *testing.T) {
	h := strings.Repeat("ab", 32)
	m, err := ParseChecksums(h + "  a.tar.gz\n" + strings.ToUpper(h) + " *b.zip\nnot a line\nshort  c\n")
	if err != nil || len(m) != 2 || m["a.tar.gz"] != h || m["b.zip"] != h {
		t.Fatalf("%v %v", m, err)
	}
	if _, err := ParseChecksums("garbage"); err == nil {
		t.Error("empty checksums accepted")
	}
}

func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for n, c := range files {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(c))
	}
	zw.Close()
	return b.Bytes()
}

func makeTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for n, c := range files {
		tw.WriteHeader(&tar.Header{Name: n, Mode: 0o755, Size: int64(len(c)), Typeflag: tar.TypeReg})
		tw.Write([]byte(c))
	}
	tw.Close()
	gz.Close()
	return b.Bytes()
}

func TestExtractBinary(t *testing.T) {
	dir := t.TempDir()
	cases := map[string][]byte{
		"x.zip":    makeZip(t, map[string]string{"README.md": "r", "envmgr": "BIN", "../evil": "no", "sub/envmgr": "no"}),
		"x.tar.gz": makeTarGz(t, map[string]string{"README.md": "r", "envmgr": "BIN", "../evil": "no", "sub/envmgr": "no"}),
	}
	for name, data := range cases {
		p := filepath.Join(dir, name)
		os.WriteFile(p, data, 0o600)
		out, err := ExtractBinary(p, name, "envmgr", dir)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if b, _ := os.ReadFile(out); string(b) != "BIN" {
			t.Fatalf("%s: got %q", name, b)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "evil")); err == nil {
		t.Fatal("path traversal wrote outside the directory")
	}
	// no usable entry: only nested or traversal names
	p := filepath.Join(dir, "bad.zip")
	os.WriteFile(p, makeZip(t, map[string]string{"sub/envmgr": "x", "../envmgr": "x"}), 0o600)
	if _, err := ExtractBinary(p, "bad.zip", "envmgr", dir); err == nil {
		t.Fatal("nested binary accepted")
	}
}

// fakeGitHub serves a release API and its assets.
type fakeGitHub struct {
	srv      *httptest.Server
	release  Release
	assets   map[string][]byte
	checksum string // override the checksum line content for the archive
	skipSums bool
}

func newFake(t *testing.T, tag, goos, goarch, binary string, drafts ...Release) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{assets: map[string][]byte{}}
	ver := strings.TrimPrefix(tag, "v")
	name := AssetName(ver, goos, goarch)
	var arc []byte
	if goos == "windows" {
		arc = makeZip(t, map[string]string{BinaryName(goos): binary, "LICENSE": "mit"})
	} else {
		arc = makeTarGz(t, map[string]string{BinaryName(goos): binary, "LICENSE": "mit"})
	}
	f.assets[name] = arc
	mux := http.NewServeMux()
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	sum := sha256.Sum256(arc)
	f.checksum = hex.EncodeToString(sum[:])
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, r *http.Request) {
		n := strings.TrimPrefix(r.URL.Path, "/dl/")
		if n == checksumsAsset {
			if f.skipSums {
				http.NotFound(w, r)
				return
			}
			fmt.Fprintf(w, "%s  %s\n%s  other.zip\n", f.checksum, name, strings.Repeat("0", 64))
			return
		}
		if b, ok := f.assets[n]; ok {
			w.Write(b)
			return
		}
		http.NotFound(w, r)
	})
	rel := Release{Tag: tag, URL: "https://example.test/rel", Assets: []Asset{
		{Name: name, DownloadURL: f.srv.URL + "/dl/" + name},
		{Name: checksumsAsset, DownloadURL: f.srv.URL + "/dl/" + checksumsAsset},
	}}
	f.release = rel
	mux.HandleFunc("/repos/o/r/releases/latest", func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(f.release) })
	mux.HandleFunc("/repos/o/r/releases/tags/", func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimPrefix(r.URL.Path, "/repos/o/r/releases/tags/") != tag {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(f.release)
	})
	mux.HandleFunc("/repos/o/r/releases", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(append([]Release{f.release}, drafts...))
	})
	return f
}

func (f *fakeGitHub) client() *Client {
	c := NewClient("o/r", "")
	c.APIBase = f.srv.URL
	c.AllowURL = func(u *url.URL) bool { return strings.HasPrefix(u.String(), f.srv.URL) }
	return c
}

func target(t *testing.T) string {
	p := filepath.Join(t.TempDir(), BinaryName("linux"))
	os.WriteFile(p, []byte("OLD"), 0o755)
	return p
}

func opts(f *fakeGitHub, tgt, current string) Options {
	return Options{Client: f.client(), Current: current, GOOS: "linux", GOARCH: "amd64", Target: tgt, SkipTest: true}
}

func TestRunUpdates(t *testing.T) {
	f := newFake(t, "v1.2.0", "linux", "amd64", "NEWBIN")
	tgt := target(t)
	res, err := Run(context.Background(), opts(f, tgt, "1.1.0"))
	if err != nil || !res.Updated {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if b, _ := os.ReadFile(tgt); string(b) != "NEWBIN" {
		t.Fatalf("target = %q", b)
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(tgt), "envmgr-*"))
	if len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

func TestRunCheckOnlyAndUpToDate(t *testing.T) {
	f := newFake(t, "v1.2.0", "linux", "amd64", "NEWBIN")
	tgt := target(t)

	o := opts(f, tgt, "1.1.0")
	o.CheckOnly = true
	res, err := Run(context.Background(), o)
	if err != nil || !res.Newer || res.Updated {
		t.Fatalf("check: %+v %v", res, err)
	}

	res, err = Run(context.Background(), opts(f, tgt, "1.2.0"))
	if err != nil || res.Updated || res.Reason != "already up to date" {
		t.Fatalf("up to date: %+v %v", res, err)
	}
	res, _ = Run(context.Background(), opts(f, tgt, "1.3.0-rc.1"))
	if res.Updated || res.Newer {
		t.Fatalf("newer local build was replaced: %+v", res)
	}
	if b, _ := os.ReadFile(tgt); string(b) != "OLD" {
		t.Fatal("target changed without an update")
	}
}

func TestRunDevBuildNeedsForce(t *testing.T) {
	f := newFake(t, "v1.2.0", "linux", "amd64", "NEWBIN")
	tgt := target(t)
	res, err := Run(context.Background(), opts(f, tgt, "dev"))
	if err != nil || res.Updated || !strings.Contains(res.Reason, "--force") {
		t.Fatalf("%+v %v", res, err)
	}
	o := opts(f, tgt, "dev")
	o.Force = true
	if res, err = Run(context.Background(), o); err != nil || !res.Updated {
		t.Fatalf("forced: %+v %v", res, err)
	}
}

func TestRunSpecificVersion(t *testing.T) {
	f := newFake(t, "v1.0.0", "linux", "amd64", "OLDER")
	tgt := target(t)
	o := opts(f, tgt, "2.0.0")
	o.Version = "1.0.0"
	if res, err := Run(context.Background(), o); err != nil || !res.Updated {
		t.Fatalf("%+v %v", res, err)
	}
	o.Version = "9.9.9"
	if _, err := Run(context.Background(), o); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing version: %v", err)
	}
}

func TestRunRefusesBadChecksum(t *testing.T) {
	f := newFake(t, "v1.2.0", "linux", "amd64", "NEWBIN")
	f.checksum = strings.Repeat("1", 64)
	tgt := target(t)
	if _, err := Run(context.Background(), opts(f, tgt, "1.1.0")); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(tgt); string(b) != "OLD" {
		t.Fatal("target replaced despite a bad checksum")
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(tgt), "envmgr-*"))
	if len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

func TestRunRefusesMissingChecksums(t *testing.T) {
	f := newFake(t, "v1.2.0", "linux", "amd64", "NEWBIN")
	f.skipSums = true
	f.release.Assets = f.release.Assets[:1] // no checksums.txt attached
	tgt := target(t)
	if _, err := Run(context.Background(), opts(f, tgt, "1.1.0")); err == nil || !strings.Contains(err.Error(), "unverified") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(tgt); string(b) != "OLD" {
		t.Fatal("target replaced without checksums")
	}
}

func TestRunWrongPlatform(t *testing.T) {
	f := newFake(t, "v1.2.0", "linux", "amd64", "NEWBIN")
	o := opts(f, target(t), "1.1.0")
	o.GOARCH = "riscv64"
	if _, err := Run(context.Background(), o); err == nil || !strings.Contains(err.Error(), "no asset") {
		t.Fatalf("err = %v", err)
	}
}

func TestRefusesForeignDownloadURL(t *testing.T) {
	f := newFake(t, "v1.2.0", "linux", "amd64", "NEWBIN")
	f.release.Assets[0].DownloadURL = "http://evil.example/envmgr.tar.gz"
	c := f.client()
	c.AllowURL = nil // the real rule: https on github.com only
	if _, err := Run(context.Background(), Options{Client: c, Current: "1.0.0", GOOS: "linux", GOARCH: "amd64", Target: target(t), SkipTest: true}); err == nil {
		t.Fatal("download from a foreign host accepted")
	}
	for u, want := range map[string]bool{
		"https://github.com/o/r/releases/download/v1/a": true,
		"https://objects.githubusercontent.com/x":       true,
		"http://github.com/x":                           false,
		"https://github.com.evil.example/x":             false,
		"https://evilgithub.com/x":                      false,
	} {
		pu, _ := url.Parse(u)
		if defaultAllowURL(pu) != want {
			t.Errorf("%s allowed=%v", u, !want)
		}
	}
}

func TestLatestPrerelease(t *testing.T) {
	pre := Release{Tag: "v2.0.0-rc.1", Prerelease: true}
	draft := Release{Tag: "v3.0.0", Draft: true}
	f := newFake(t, "v1.5.0", "linux", "amd64", "X", pre, draft)
	c := f.client()
	r, err := c.Latest(context.Background(), true)
	if err != nil || r.Tag != "v2.0.0-rc.1" {
		t.Fatalf("%+v %v", r, err)
	}
	r, err = c.Latest(context.Background(), false)
	if err != nil || r.Tag != "v1.5.0" {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestReplaceKeepsModeAndHandlesSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real", "envmgr")
	os.MkdirAll(filepath.Dir(real), 0o755)
	os.WriteFile(real, []byte("OLD"), 0o755)
	link := filepath.Join(dir, "envmgr-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Fatal(err)
	}
	nw := filepath.Join(filepath.Dir(resolved), "new")
	os.WriteFile(nw, []byte("NEW"), 0o600)
	if err := Replace(resolved, nw); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(link); string(b) != "NEW" {
		t.Fatalf("through the link: %q", b)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("link was replaced by a file")
	}
}

func TestReplaceOnAnyOS(t *testing.T) {
	dir := t.TempDir()
	tgt := filepath.Join(dir, "envmgr")
	os.WriteFile(tgt, []byte("OLD"), 0o755)
	nw := filepath.Join(dir, "new")
	os.WriteFile(nw, []byte("NEW"), 0o600)
	if err := Replace(tgt, nw); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(tgt); string(b) != "NEW" {
		t.Fatalf("%q", b)
	}
	CleanupOld(tgt)
	if _, err := os.Stat(tgt + ".old"); err == nil {
		t.Fatal(".old left behind")
	}
}
