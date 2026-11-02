# Set up and use your dotfiles with envmgr

Background and safety model: `.claude/docs/dotfiles.md`.

## 1. Describe what goes where

In the dotfiles repository, add a `links` section to `.dotfiles.config.json` (keep its other keys):

```json
"links": [
  { "src": ".zshrc",              "dest": "~/.zshrc" },
  { "src": "nvim",                "dest": "~/.config/nvim" },
  { "src": "tmux/.tmux.conf",     "dest": "~/.tmux.conf" },
  { "src": "hyprland",            "dest": "~/.config/hypr", "os": ["linux"] }
]
```

Take the destinations from the repo's own `scripts/zsh/init/*.zsh` (the `create_symbolic_link` calls) if it has them.

## 2. Get the repository

```bash
envmgr dotfiles clone git@github.com:you/dotfiles.git          # to ~/dotfiles, registered
envmgr dotfiles clone <source> /path/to/dest --link             # and link right away
envmgr dotfiles register /path/you/already/have                 # use an existing checkout
```

## 3. Look before linking

```bash
envmgr dotfiles status          # ok / missing / conflict / wrong-link / src-missing / skipped
envmgr dotfiles link --dry-run  # shows backups it would make
```

## 4. Link, update, undo

```bash
envmgr dotfiles link            # backs up anything in the way as <dest>.envmgr-bak-<time>
envmgr dotfiles pull            # git pull --ff-only, then link
envmgr dotfiles unlink          # removes only what envmgr made, restores the backups
```

## Windows

Symlinks need Developer Mode (Settings, For developers) or an elevated shell; envmgr does not elevate. Without them: `envmgr dotfiles link --copy` (copies are kept by `unlink` unless `--force`, since you may have edited them).

## Try it without touching your real home

```bash
export ENVMGR_HOME=$(mktemp -d)
envmgr dotfiles register ~/dotfiles
envmgr dotfiles link --home "$(mktemp -d)" --dry-run
```

`--home` changes what `~` means. The state files go under `ENVMGR_HOME`.

## Troubleshooting

- `.dotfiles.config.json not found` / `no "links"`: add the section (step 1).
- `A required privilege is not held by the client`: Windows symlink rights; see above.
- `dest uses $X, which is not set`: define the variable or use `~`.
- A destination shows `conflict`: it is a real file envmgr did not create; `link` will back it up and replace it.
