# Cut a release

Background and the asset contract: `.claude/docs/releases.md`. Remote is `upstream`; git here needs `-c safe.directory=E:/repos/github/dev-vinicius-andrade/environment-manager`.

1. **Decide the version** (`vMAJOR.MINOR.PATCH`). Use a prerelease suffix (`v0.2.0-rc.1`) for a first run or risky change: it is marked prerelease and `envmgr update` and the install scripts do not pick it up by default.
2. **Docs**: make sure README, `.claude/docs/` and `.claude/instructions/` match what ships (commands, flags, install). Update the indexes if you added files.
3. **Check locally** from an up-to-date `main`:
   ```bash
   go vet ./... && go test ./...
   scripts/package.sh 0.2.0-rc.1 linux amd64 /tmp/envmgr-dist     # any os/arch: builds, packages
   ```
   Extract the archive and run `envmgr version` and `envmgr --help`. Symlink tests skip on Windows without Developer Mode; run `wsl go test ./internal/dotfiles` for them.
4. **Tag and push** (the tag must point at a commit already on `main`):
   ```bash
   git tag -a v0.2.0 -m "v0.2.0"
   git push upstream v0.2.0
   ```
5. **Watch the run**: `gh run watch --repo dev-vinicius-andrade/environment-manager` (workflow `release`). All six `build` jobs must pass, then `release`.
6. **Check the release** (`gh release view v0.2.0`): six archives (`envmgr_0.2.0_<os>_<arch>.tar.gz|zip`) and `checksums.txt`; download one and `sha256sum -c --ignore-missing checksums.txt`; edit the generated notes if they need it.
7. **Try the consumers against the new release**:
   - `curl -fsSL https://raw.githubusercontent.com/dev-vinicius-andrade/environment-manager/main/scripts/install.sh | ENVMGR_VERSION=0.2.0 ENVMGR_INSTALL_DIR=$(mktemp -d) sh` and the PowerShell equivalent (`$env:ENVMGR_VERSION='0.2.0'; $env:ENVMGR_INSTALL_DIR="$env:TEMP\envmgr-try"; irm .../install.ps1 | iex`); then run the installed `envmgr version`.
   - From an older installed build: `envmgr update --check`, then `envmgr update`, then `envmgr version`.

## If something goes wrong

- A build job fails: fix on `main`, delete the tag and any draft/partial release, tag again (below). Nothing is published until the `release` job runs.
- A bad release is out: `gh release delete v0.2.0 --cleanup-tag --yes --repo dev-vinicius-andrade/environment-manager`, then cut the fix as a new patch version. Do not move a tag that users may already have installed.
- Locally remove a tag: `git tag -d v0.2.0`.
