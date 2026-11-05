package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Options drive Run.
type Options struct {
	Client     *Client
	Current    string // running version; "dev" for an unstamped build
	GOOS       string // default runtime.GOOS
	GOARCH     string // default runtime.GOARCH
	Target     string // file to replace; default: the running executable (symlinks resolved)
	Version    string // install this version instead of the latest
	Prerelease bool   // consider pre-releases when looking for the latest
	Force      bool   // install even if not newer, or from an unstamped build
	CheckOnly  bool   // look, do not install
	SkipTest   bool   // skip running the new binary before replacing (tests)
	Progress   func(format string, args ...any)
}

// Result says what Run found and did.
type Result struct {
	Current  string
	Latest   string
	Release  Release
	Newer    bool // Latest is newer than Current (always true for a dev build)
	Updated  bool
	Reason   string // why nothing was installed
	Archive  string
	Replaced string
}

func (o Options) say(format string, args ...any) {
	if o.Progress != nil {
		o.Progress(format, args...)
	}
}

// Run finds the release and, unless CheckOnly, installs it. It never replaces
// anything before the archive's SHA-256 matches checksums.txt and the new
// binary reports the expected version.
func Run(ctx context.Context, o Options) (Result, error) {
	goos, goarch := o.GOOS, o.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	res := Result{Current: o.Current}

	var rel Release
	var err error
	if o.Version != "" {
		rel, err = o.Client.ByTag(ctx, o.Version)
	} else {
		rel, err = o.Client.Latest(ctx, o.Prerelease)
	}
	if errors.Is(err, ErrNoRelease) {
		if o.Version != "" {
			return res, fmt.Errorf("release %s not found", o.Version)
		}
		return res, errors.New("no release has been published yet")
	}
	if err != nil {
		return res, err
	}
	latest, err := rel.Version()
	if err != nil {
		return res, fmt.Errorf("release tag %q is not a version: %w", rel.Tag, err)
	}
	res.Release, res.Latest = rel, latest.String()

	dev := o.Current == "" || o.Current == "dev"
	res.Newer = true
	if !dev {
		cur, err := ParseVersion(o.Current)
		if err != nil {
			return res, fmt.Errorf("running version %q: %w", o.Current, err)
		}
		res.Newer = latest.Compare(cur) > 0
	}
	if o.CheckOnly {
		return res, nil
	}
	switch {
	case dev && !o.Force && o.Version == "":
		res.Reason = "this build has no release version (built from source); use --force to install the latest release over it"
		return res, nil
	case !res.Newer && !o.Force && o.Version == "":
		res.Reason = "already up to date"
		return res, nil
	}

	name := AssetName(latest.String(), goos, goarch)
	asset, ok := rel.Asset(name)
	if !ok {
		return res, fmt.Errorf("release %s has no asset %s for %s/%s", rel.Tag, name, goos, goarch)
	}
	sums, err := o.Client.Checksums(ctx, rel)
	if err != nil {
		return res, err
	}
	want, ok := sums[name]
	if !ok {
		return res, fmt.Errorf("%s has no checksum for %s; refusing to install it", checksumsAsset, name)
	}

	target := o.Target
	if target == "" {
		if target, err = TargetPath(); err != nil {
			return res, err
		}
	}
	dir := filepath.Dir(target)
	CleanupOld(target)

	o.say("downloading %s ...", name)
	archive, err := o.Client.DownloadVerified(ctx, asset, want, dir)
	if err != nil {
		return res, permHint(target, err)
	}
	defer os.Remove(archive)
	o.say("checksum ok (sha256 %s...)", want[:12])

	newBin, err := ExtractBinary(archive, name, BinaryName(goos), dir)
	if err != nil {
		return res, err
	}
	defer os.Remove(newBin) // gone after a successful rename; cleans up on failure
	if !o.SkipTest {
		if err := SelfTest(newBin, latest.String()); err != nil {
			return res, err
		}
	}
	if err := Replace(target, newBin); err != nil {
		return res, err
	}
	res.Updated, res.Archive, res.Replaced = true, name, target
	return res, nil
}
