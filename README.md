# envmgr

A terminal tool to manage your environment: **named profiles of environment variables per shell session**, the **hosts file**, **other machines**, and your **dotfiles**.

```text
$ envmgr set dev MY_API_URL https://api.dev.example.com
$ envmgr set local MY_API_URL http://localhost:8080
$ eval "$(envmgr use dev)"        # this shell now has MY_API_URL=https://api.dev...
$ eval "$(envmgr deactivate)"     # and the previous value is back
```

## What it does

1. **Environment profiles.** A profile (`local`, `dev`, `external`, `test1`...) holds environment variables, hosts entries, or both. Activating one changes the current shell session only, and `deactivate` restores what was there before. Useful for several cloud accounts, several API keys, several environments of the same service.
2. **One machine controlling another.** A *parent* hands out a single-use, expiring join code; a *child* joins with it and stays bound to that one parent, which can then run profile operations on it. No remote command execution, ever.
3. **Dotfiles.** Clone your dotfiles repository and link its files into place from a small manifest, with backups and a safe undo.

Plus a terminal UI, a Windows tray icon to pick the machine-wide profile (open shells follow it), and hosts-file editing through a block that envmgr owns.

## Install

**Linux and macOS**

```bash
curl -fsSL https://raw.githubusercontent.com/dev-vinicius-andrade/environment-manager/main/scripts/install.sh | sh
```

**Windows (PowerShell)**

```powershell
irm https://raw.githubusercontent.com/dev-vinicius-andrade/environment-manager/main/scripts/install.ps1 | iex
```

The scripts download the archive for your OS and architecture from the latest [release](https://github.com/dev-vinicius-andrade/environment-manager/releases), verify its SHA-256 against `checksums.txt`, and install `envmgr`. Settings, as environment variables:

| Variable | Meaning | Default |
| --- | --- | --- |
| `ENVMGR_VERSION` | version to install, for example `1.2.0` | latest |
| `ENVMGR_INSTALL_DIR` | where to put the binary | `~/.local/bin` (Linux, macOS); `%LOCALAPPDATA%\envmgr\bin` (Windows, added to your user `PATH`) |

**Manual download.** Pick `envmgr_<version>_<os>_<arch>.tar.gz` (Linux, macOS) or `.zip` (Windows) from the [Releases](https://github.com/dev-vinicius-andrade/environment-manager/releases) page (`os`: `linux`, `darwin`, `windows`; `arch`: `amd64`, `arm64`), check it against `checksums.txt`, extract `envmgr` (`envmgr.exe`) and put it on your `PATH`:

```bash
sha256sum -c --ignore-missing checksums.txt
tar -xzf envmgr_1.2.0_linux_amd64.tar.gz && install envmgr ~/.local/bin/
```

```powershell
(Get-FileHash .\envmgr_1.2.0_windows_amd64.zip -Algorithm SHA256).Hash   # compare with checksums.txt
Expand-Archive .\envmgr_1.2.0_windows_amd64.zip -DestinationPath $env:LOCALAPPDATA\envmgr\bin
```

**From source** (Go 1.24 or newer):

```bash
go build -o envmgr .                 # or: make build
go install github.com/dev-vinicius-andrade/environment-manager@latest   # version shows "dev"
```

## Quick start

### Profiles

```bash
envmgr set dev MY_API_URL https://api.dev.example.com     # creates the profile if needed
envmgr set local MY_API_URL http://localhost:8080
envmgr list                                               # profiles
envmgr profile show dev                                   # values are masked; --reveal shows them
envmgr unset dev MY_API_URL
envmgr run dev -- curl "$MY_API_URL"                      # one command, nothing changes in your shell
```

A program cannot change its parent shell's environment, so `use` and `deactivate` print shell code for the shell to evaluate (messages go to stderr; on an error stdout stays empty):

| Shell | Activate | Back |
| --- | --- | --- |
| bash, zsh | `eval "$(envmgr use dev)"` | `eval "$(envmgr deactivate)"` |
| fish | `envmgr use dev --shell fish \| source` | `envmgr deactivate --shell fish \| source` |
| PowerShell | `envmgr use dev --shell powershell \| Invoke-Expression` | `envmgr deactivate --shell powershell \| Invoke-Expression` |
| cmd | `envmgr use dev --shell cmd > "%TEMP%\envmgr.cmd" && "%TEMP%\envmgr.cmd"` | same with `deactivate` |

`envmgr current` shows the active profile. Details, quoting rules and limits: [`.claude/docs/profiles.md`](.claude/docs/profiles.md).

### Pick a profile for every open shell (tray and hook)

`envmgr select <profile>` records a machine-wide choice. Shells with the **hook** follow it on their next prompt; a shell that ran `envmgr use` is pinned until `envmgr follow`.

```bash
envmgr hook --install bash        # or zsh, fish, powershell; run once, then open a new shell
envmgr select dev                 # every following shell switches to dev
envmgr select --none
```

On Windows, `envmgr tray` shows a tray icon with a menu per profile (environment, hosts, both). It runs in the background and starts with Windows if you tick that option. See [`.claude/docs/tray.md`](.claude/docs/tray.md) and [`.claude/instructions/install-shell-hook.md`](.claude/instructions/install-shell-hook.md).

### Hosts file

envmgr only edits the lines between `# BEGIN envmgr` and `# END envmgr`; everything else is left byte for byte, and a timestamped backup is written before every change.

```bash
envmgr hosts add 10.0.0.5 api.dev.local --group dev --dry-run    # preview the diff
envmgr hosts add 10.0.0.5 api.dev.local --group dev              # needs an elevated shell / sudo
envmgr hosts list
envmgr hosts restore --list
```

A profile can carry hosts entries (`envmgr profile hosts add dev 10.0.0.5 api.dev.local`), applied only when you ask: `envmgr use dev --hosts`, `envmgr use dev --only hosts`, `envmgr hosts apply dev`. envmgr never elevates itself. See [`.claude/docs/hosts.md`](.claude/docs/hosts.md).

### Another machine

On the **parent**:

```bash
envmgr parent serve --addr 0.0.0.0:7788                                   # keep running
envmgr parent invite --name laptop --addr-public parent.example.com:7788   # prints: envmgr join <code>
envmgr parent profiles laptop
envmgr parent set laptop dev MY_API_URL https://api.dev.example.com
```

On the **child**: `envmgr join <code>`. The child dials the parent (it opens no port), pins the parent's certificate, and takes orders only from it. Codes are single-use and expire; `invite --read-only` makes a read-only child, `--allow-hosts` additionally lets the parent edit the child's hosts block; `envmgr parent revoke laptop` cuts it off. See [`.claude/docs/remote.md`](.claude/docs/remote.md) and [`.claude/instructions/connect-a-child.md`](.claude/instructions/connect-a-child.md).

### Dotfiles

Keep your dotfiles in a Git repository and describe where each file goes in its `.dotfiles.config.json` (comments allowed; other keys are ignored):

```json
{
  "links": [
    { "src": ".zshrc",   "dest": "~/.zshrc" },
    { "src": "nvim",     "dest": "~/.config/nvim" },
    { "src": "hyprland", "dest": "$HOME/.config/hypr", "os": ["linux"] }
  ]
}
```

```bash
envmgr dotfiles clone git@github.com:you/dotfiles.git    # clones to ~/dotfiles and registers it
envmgr dotfiles status                                   # ok / missing / conflict / wrong-link
envmgr dotfiles link --dry-run                           # then without --dry-run
envmgr dotfiles pull                                     # git pull --ff-only, then link
envmgr dotfiles unlink                                   # removes only what envmgr made, restores backups
```

A file already at a destination is moved to `<dest>.envmgr-bak-<timestamp>`, never deleted. On Windows symlinks need Developer Mode or an elevated shell; without them use `link --copy`. See [`.claude/docs/dotfiles.md`](.claude/docs/dotfiles.md).

### Terminal UI

`envmgr tui` (or just `envmgr` in a terminal) opens an interactive screen to browse, edit and activate profiles. To activate from it, run it through your shell as shown in [`.claude/docs/tui.md`](.claude/docs/tui.md).

## Updating

```bash
envmgr version             # version, commit and build date
envmgr update --check      # is there a newer release?
envmgr update              # download it and replace this binary
```

`update` asks GitHub Releases for the latest version, downloads the archive for your OS and architecture, verifies its SHA-256 against the release's `checksums.txt`, and replaces the running binary. If `envmgr` is a symlink the real file is replaced and the link stays. On Windows the old `envmgr.exe` is renamed aside first, since a running executable cannot be overwritten. Set `GITHUB_TOKEN` to avoid API rate limits or to update from a private fork. Releases are cut by pushing a `vX.Y.Z` tag: see [`.claude/docs/releases.md`](.claude/docs/releases.md).

## Security notes

- No remote command execution: a parent can only list, read, write and delete profiles (and edit the child's hosts block when the child was invited with `--allow-hosts`).
- Join codes are single-use and expire; the transport is TLS 1.3 with the parent's certificate pinned by SHA-256 fingerprint.
- The hosts file is only changed inside the envmgr block, with a backup; envmgr never elevates itself.
- Dotfiles never delete a file envmgr did not create; what is in the way is backed up first.
- Profile files are stored with mode `0600` in `$ENVMGR_HOME` (default: your user config directory, `envmgr/`). They hold values in plain text: do not put secrets you would not keep in a file readable by your account.

## Development

```bash
go vet ./...
go test ./...                       # symlink tests skip where the OS cannot create symlinks
```

On Windows without Developer Mode, run the symlink tests in WSL: `wsl go test ./internal/dotfiles`. Cross-compile with `make build_all` or package one release target with `scripts/package.sh <version> <os> <arch> <outdir>`.

Project knowledge lives under [`.claude/`](.claude/): `docs/` explains what exists and why, `instructions/` has step-by-step how-tos, `plans/` holds task plans. Start from [`.claude/docs/README.md`](.claude/docs/README.md) and [`.claude/instructions/README.md`](.claude/instructions/README.md); every change updates them.

## License

[MIT](LICENSE)
