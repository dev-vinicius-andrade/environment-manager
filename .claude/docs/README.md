# Docs index

Check here before starting a task. Add one line per doc: `file — what it covers`.

- `goals.md` — product goals (env profiles, remote control between machines) and open design questions
- `hosts.md` — hosts-file management (`envmgr hosts`, `parent hosts`): managed block safety model and format, backups/lock/atomic write, validation, profile groups (`hosts apply`), the remote `--allow-hosts` grant and threat model, privileges per OS, tests
- `profiles.md` — profiles (variables and/or hosts entries): `use`/`deactivate` shell code and scopes (env/hosts/both), `ENVMGR_*` state variables, shell support and quoting, profile commands, tests
- `dotfiles.md` — `envmgr dotfiles`: clone a dotfiles repo and link its files (manifest in `.dotfiles.config.json`, backup/unlink safety model, Windows symlink limits, tests, what is not done)
- `releases.md` — release contract (tag, six targets, asset names, `checksums.txt`, version ldflags), CI and release workflows, `scripts/package.sh`, how `envmgr update` and the install scripts find assets, limits
- `self-update.md` — `envmgr update`/`version` and the install scripts: flow, symlink and Windows replacement, trust model, tests, what is not done
- `tray.md` — machine-wide profile selection (`select`/`selected`/`sync`/`follow`), pinned vs following shells (`ENVMGR_PINNED`), the prompt hook (`envmgr hook`), the Windows tray icon (`envmgr tray`), tests, limits
- `remote.md` — remote control, parent/child hub model (`envmgr parent`, `join`, `agent`): who controls whom, invite lifecycle, API, threat model, limits

- `tui.md` — `envmgr tui` (and bare `envmgr` in a terminal): screens, keys, stderr UI / stdout activation code, the `Screen` extension point, tests, limits

Related: `.claude/instructions/` for how-to guides, `.claude/plans/` for task plans, `.claude/skills/` for project skills.
