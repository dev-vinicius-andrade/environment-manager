# Self-update and install (`envmgr update`, `envmgr version`, install scripts)

Release contract, workflow and tagging: `releases.md`. Steps to cut a release: `.claude/instructions/cut-a-release.md`.

## `envmgr version [--short]`

Prints version, commit, build date, Go version and platform from `internal/buildinfo` (stamped by `scripts/package.sh` / `make build VERSION=x.y.z`). An unstamped build (`go run`, `go build`, `go install`) reports `dev`. `--short` prints the bare version; the updater uses it to test a downloaded binary.

## `envmgr update`

`envmgr update [--check] [--version X] [--pre] [--force]`. Code: `internal/update` (logic, tested), `commands/envmgr/update` (cobra wiring).

Flow (`update.Run`):

1. Find the release: `GET /repos/<repo>/releases/latest` (stable only), or the newest non-draft of `releases?per_page=20` with `--pre`, or `releases/tags/vX.Y.Z` with `--version`.
2. Compare with the running version using semver precedence (`ParseVersion`/`Compare`: a pre-release is lower than its release). `--check` stops here and says whether a newer release exists.
3. Not newer: nothing happens ("already up to date") unless `--force` or `--version`. A `dev` build is never replaced without `--force` or `--version` (it has no version to compare).
4. Pick the asset `envmgr_<version>_<os>_<arch>.{tar.gz|zip}` for this OS/arch and the release's `checksums.txt`. No `checksums.txt`, or no entry for the archive: refuse (an unverified binary is never installed).
5. Download the archive into the target's directory (same filesystem, so the final rename is atomic), computing SHA-256 while streaming; mismatch: delete and fail.
6. Extract only the top-level `envmgr`/`envmgr.exe` entry (nested or `../` names are ignored; 200 MiB cap).
7. Run the new binary with `version --short`; it must report the expected version (catches a wrong architecture or a corrupt file) before anything is replaced.
8. Replace the target, then delete temp files.

Replacement (`internal/update/replace.go`): the target is the running executable with symlinks resolved, so an install reached through a link (`~/.local/bin/envmgr -> /opt/envmgr/envmgr`) updates the real file and the link keeps working. On unix it renames over the target (atomic, fine while running). On Windows a running exe cannot be overwritten but can be renamed: the old file becomes `envmgr.exe.old` (deleted by the next update) and is moved back if the swap fails. The new file keeps the old file's permission bits. An unwritable install directory gives a hint; envmgr never elevates itself.

## Trust and limits

- Downloads are limited to `https://github.com` and `*.githubusercontent.com` (also checked on every redirect); `GITHUB_TOKEN`/`GH_TOKEN` is sent only to the API, never to downloads.
- The SHA-256 comes from the same release as the archive: it detects corruption and a bad mirror, not someone who can edit the release itself. Releases are not signed yet (candidate next step: cosign or minisign signatures, verified by the updater).
- No background or automatic checks: nothing runs unless the user runs `envmgr update`. (The shell hook runs on every prompt, so a passive "update available" notice was deliberately left out; a TUI/tray hint is an open idea.)
- Anonymous API rate limit is 60 requests/hour per IP; the error says to set `GITHUB_TOKEN`.
- Private forks work only with a token and public asset URLs; not supported otherwise.

## Install scripts

`scripts/install.sh` (`curl -fsSL https://raw.githubusercontent.com/dev-vinicius-andrade/environment-manager/main/scripts/install.sh | sh`) and `scripts/install.ps1` (`irm .../scripts/install.ps1 | iex`). Both resolve the latest release (or `ENVMGR_VERSION`), download the archive for the OS/arch and `checksums.txt`, verify the SHA-256, and install to `ENVMGR_INSTALL_DIR` (default `~/.local/bin`, or `%LOCALAPPDATA%\envmgr\bin` added to the user PATH). No sudo or administrator rights. The Windows script renames a running `envmgr.exe` aside, like the updater. Syntax checked with `sh -n` and the PowerShell parser; run them against a real release when the first tag exists.

## Tests

`internal/update/update_test.go` uses an `httptest` fake of the GitHub API and asset server: semver ordering, checksum parsing, zip and tar.gz extraction (traversal and nested entries ignored), update/check/up-to-date/dev/specific-version/prerelease paths, refusal on bad or missing checksums and foreign URLs, wrong platform, replace keeping mode, replacing through a symlink (skipped on Windows without symlink rights; passes under WSL). The self-test step is skipped in those tests and was verified separately against a real packaged binary (right version accepted, wrong version and garbage rejected).

## Not done

Signature verification; a passive update notice (TUI/tray); installing from `go install` with a version (reports `dev`); a versioned-directory layout with a `current` symlink and rollback (today there is no rollback beyond `update --version <older>`).
