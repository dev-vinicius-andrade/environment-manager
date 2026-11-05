# Instructions index

Specialized how-to guides: how to do one specific thing in this repo (e.g. add a subcommand, add a shell emitter, release a build). One file per topic, step by step. Check here before starting a task; add a new one when a procedure is repeated or non-obvious.

Add one line per file: `file — what it explains`.

- `add-subcommand.md` — how to add a cobra subcommand and wire it into the root
- `e2e-demo.md` — run the two-machine end-to-end demo and negative checks
- `connect-a-child.md` — connect a child machine to a parent (invite, join, operate, revoke, rotate)
- `install-shell-hook.md` — install/uninstall the prompt hook that follows the tray selection (PowerShell, bash, zsh, fish), try it with a temp home, troubleshooting
- `edit-hosts.md` — edit the hosts file locally and from a parent (`--allow-hosts`), safely with a temp file, elevation on Windows and Linux/macOS
- `profiles-with-hosts.md` — define a profile with environment and hosts entries, apply env / hosts / both (`use --hosts`, `--only`, `hosts apply`), elevation notes, safe trial with temp files

- `dotfiles.md` — set up the `links` manifest, clone/register a dotfiles repo, status/link/pull/unlink, Windows `--copy`, trying it with a temp home

- `cut-a-release.md` — tag and publish a release, check assets/checksums, try the install scripts and `envmgr update`, roll back a bad release

- `add-tui-screen.md` — how to add a screen (tab) to the TUI: `Screen` interface, registration, output contract, tests

Difference from `.claude/docs/`: docs explain what exists and why; instructions explain how to do a task.
