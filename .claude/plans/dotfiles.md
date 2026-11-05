# Plan: dotfiles management (`envmgr dotfiles`)

Goal: the user's dotfiles repo is the source of truth; envmgr clones it and links its files into place, safely and on Windows too. It does not replace stow/chezmoi and does not install packages.

## Findings about the real repo (`E:\repos\github\dev-vinicius-andrade\dotfiles`)

- One top-level directory per tool (`nvim`, `tmux`, `wezterm`, ...), so a stow-style "folder = package in ~" does not fit: `nvim/` goes to `~/.config/nvim`, `tmux/.tmux.conf` to `~/.tmux.conf`. A manifest is required.
- Links are made today by per-tool zsh scripts (`scripts/zsh/init/*.zsh`) with `ln -sfnv`; an existing regular file is overwritten without a backup, Linux/WSL only.
- `.dotfiles.config.json` already exists (JSON with `//` comments, package-manager data). The manifest lives there as a new `links` section; envmgr ignores keys it does not know.

## Scope v1 (local only)

`clone`, `register`, `status`, `link`, `unlink`, `pull`. Deferred: `adopt` (needs comment-preserving manifest edits), profiles choosing packages, parent controlling children (would sit behind an explicit invite grant like `--allow-hosts`), package installation.

## Design

- Manifest: `links: [{ "src": "nvim", "dest": "~/.config/nvim", "os": ["linux","darwin"] }]`. `src` is relative to the repo and may not leave it; `dest` expands `~`, `$VAR`, `${VAR}` and must end up absolute; `os` (optional) filters by GOOS.
- State in `<ENVMGR_HOME>`: `dotfiles.json` (repo dir, source) and `dotfiles-applied.json` (what envmgr created, with backups).
- `link`: creates parent dirs; destination already a correct link: no-op; wrong link or regular file/dir: moved to `<dest>.envmgr-bak-<timestamp>` and linked (never deleted); `--dry-run` prints the plan. Symlink failure (Windows without Developer Mode/admin): error with the hint, or `--copy`. Never elevates.
- `unlink`: removes only what the applied record says envmgr made and that still matches; restores the backup unless `--no-restore`.
- `status`: ok / missing / wrong-link / conflict / src-missing / skipped (os) per entry; exit 1 when something is not ok with `--check`.
- `clone <source> [dest]`: `git clone` (dest default `~/dotfiles`, must be missing or empty), registers it.
- `pull`: `git pull --ff-only` in the repo, then link.

## Tests

Unit tests on temp dirs for manifest parse (comments, validation, expansion), plan states, apply/backup/idempotence, unlink/restore, copy mode; command tests through cobra; real run against the user's repo with `--dry-run` and a temp HOME.
