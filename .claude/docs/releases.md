# Releases, CI and self-update

Pushing a version tag builds every target and publishes a GitHub release; `envmgr update` and the install scripts consume it. Step by step: `.claude/instructions/cut-a-release.md`.

Files: `.github/workflows/ci.yml`, `.github/workflows/release.yml`, `scripts/package.sh`, `scripts/install.sh`, `scripts/install.ps1`; the Go side is `internal/buildinfo`, `internal/update`, `commands/envmgr/{version,update}`.

## Contract (do not change without changing the updater and the install scripts)

- **Tag**: `vMAJOR.MINOR.PATCH`, or with a suffix (`v1.2.0-rc.1`) for a prerelease. The release workflow triggers on those tag patterns only.
- **Targets**: linux, darwin, windows, each for amd64 and arm64 (six archives). `CGO_ENABLED=0`; the Windows tray (`fyne.io/systray`) builds without cgo, so all targets cross-compile from one Ubuntu runner.
- **Asset names**: `envmgr_<version>_<os>_<arch>.tar.gz` (linux, darwin) or `.zip` (windows). `<version>` has no leading `v` (`1.2.0`, `1.2.0-rc.1`). Each archive holds, at its top level, `envmgr` (`envmgr.exe`), `LICENSE` and `README.md`.
- **`checksums.txt`**: one release asset in `sha256sum` format (`<hex>  <filename>`), covering every archive.
- **Version injection**: `-ldflags "-s -w -X <module>/internal/buildinfo.Version=<version> -X ...Commit=<sha> -X ...Date=<RFC 3339>"`. A build without it reports the default version (`dev`), for example `go install` or `go build`.

## Workflow

`release.yml`: a `build` matrix (6 jobs on ubuntu-latest) runs `scripts/package.sh "${GITHUB_REF_NAME#v}" <os> <arch> dist` and uploads the archive; the `release` job (the only one with `contents: write`) downloads them all, writes `checksums.txt` with `sha256sum`, and runs `gh release create <tag> dist/* --generate-notes --verify-tag` (plus `--prerelease` when the tag contains `-`). Release notes are generated from the merged PRs and commits since the previous tag.

`ci.yml`: on push to `main` and on pull requests, `go vet ./...` and `go test ./...` on ubuntu, windows and macos. `go vet` is clean (the former `signal.Notify` warning in `helpers/spinner.go` was fixed by buffering the channel).

`scripts/package.sh <version> <os> <arch> <outdir>` builds and packages one target; it works in Git Bash on Windows (zip via `zip`, else `bsdtar`, else `python3`). Use it locally to check packaging.

## How the updater finds things

`envmgr update` asks the GitHub API (`releases/latest`, which skips prereleases and drafts) for the newest release, compares versions as semver against the running one, picks the asset named for its own GOOS/GOARCH, downloads it and `checksums.txt`, verifies the SHA-256 and replaces the running binary. The install scripts (`install.sh`, `install.ps1`) use the same asset names and checksum file; they honor `ENVMGR_VERSION` (default latest) and `ENVMGR_INSTALL_DIR`. `GITHUB_TOKEN` is used for API requests when set. Behavior and limits of the updater itself: `self-update.md`.

## Limits

- The workflow has never run on GitHub until the first tag is pushed; the first release should be a prerelease (`v0.1.0-rc.1`) to check it.
- Archives are not code-signed or notarized (macOS Gatekeeper and Windows SmartScreen may warn on a browser download); integrity is the SHA-256 from the same release, which protects against corruption, not against someone who can edit the release.
- Only the six targets above; 32-bit and BSD builds from the Makefile are not released.
- A tag cannot be reused for different content: delete the release and tag and cut a new patch version.
