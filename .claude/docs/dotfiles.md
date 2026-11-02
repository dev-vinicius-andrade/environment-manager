# Dotfiles (`envmgr dotfiles`)

The user's dotfiles repository stays the source of truth. envmgr clones it and links each file to where its tool expects it, safely and on Windows too. It is not a replacement for stow/chezmoi and does not install packages. Plan and rationale: `.claude/plans/dotfiles.md`; how-to: `.claude/instructions/dotfiles.md`.

## Manifest

The repository's `.dotfiles.config.json` (JSON, `//` and `/* */` comments allowed) gets a `links` section; every other key is ignored, so the repo's own package-manager data stays untouched.

```json
"links": [
  { "src": "nvim",   "dest": "~/.config/nvim" },
  { "src": ".zshrc", "dest": "~/.zshrc" },
  { "src": "hyprland", "dest": "$HOME/.config/hypr", "os": ["linux"] }
]
```

- `src`: relative to the repository, may not be absolute or leave it (`..`). A file or a directory.
- `dest`: `~`, `$VAR`, `${VAR}` expanded (`$HOME`/`$USERPROFILE` fall back to the home directory; any other unset variable is an error); must be absolute afterwards. No two links share a destination.
- `os`: optional GOOS filter (`linux`, `windows`, `darwin`); other OSes show as `skipped`.

A stow-style "folder = package in ~" was rejected: the real repo maps `nvim/` to `~/.config/nvim`, `tmux/.tmux.conf` to `~/.tmux.conf`, so a manifest is required.

## Commands

`clone <source> [dest] [--link]`, `register <dir>`, `status [--check]`, `link [--dry-run] [--copy]`, `unlink [--dry-run] [--no-restore] [--force]`, `pull`. Global flags: `--dir` (repository, default the registered one), `--home` (what `~` means, default the user's home; used by tests).

State lives in `<ENVMGR_HOME>`: `dotfiles.json` (registered repository and its source) and `dotfiles-applied.json` (every destination envmgr created, with mode and backup).

## Safety model

- Nothing is ever deleted that envmgr did not create. A destination that is a real file/directory or a link to something else is moved to `<dest>.envmgr-bak-<yyyymmdd-hhmmss>` first, then linked. If linking fails, the backup is put back.
- `link` is idempotent: a correct link is left alone and creates no backup. A correct link that envmgr did not make is not recorded, so `unlink` will not touch it.
- `unlink` removes only recorded destinations that still match (a link to the recorded source). A destination the user replaced, or a link retargeted elsewhere, is kept with a note. Then it restores the backup (`--no-restore` skips that). Copies (`--copy`) are kept unless `--force`, since they may have been edited.
- `--dry-run` prints the same lines with a `would-` prefix and writes nothing.
- Entries fail independently; the exit code is 1 if any failed (a missing `src`, a symlink the OS refused...).
- `pull` is `git pull --ff-only` then `link`; it never merges or rewrites history. `clone` refuses a non-empty destination.
- Never elevates. On Windows, symlinks need Developer Mode or an elevated shell; the error says so and suggests `--copy`.

## Code

`internal/dotfiles` (manifest + JSONC stripping + expansion, `BuildPlan`/`Apply`/`Unlink`, state files, git wrappers), `commands/envmgr/dotfiles` (cobra wiring and reporting).

## Tests

Unit tests on temp dirs (manifest parse/validation/expansion, states, backup, idempotence, unlink/restore, altered destination, copy mode, state round trip) and command tests through cobra (register, status, link, unlink, clone of a local repo). The symlink tests skip where the OS cannot create symlinks (Windows without Developer Mode); run them on Linux (for example `wsl` with Go) to cover that path: they pass there, and a manual run of the built binary against a copy of the real repo linked, re-linked, status-checked and unlinked with the original `.zshrc` restored.

## Not done

`adopt <path>` (move an existing file into the repo and link it back: needs comment-preserving edits of the manifest); profiles choosing which links to apply; a parent controlling a child's dotfiles (would sit behind an explicit invite grant like `--allow-hosts`); package installation (the repo's own scripts do it); PowerShell's `$PROFILE` as a destination (write the explicit path).
