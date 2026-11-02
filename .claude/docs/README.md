# Docs index

Check here before starting a task. Add one line per doc: `file — what it covers`.

- `goals.md` — product goals (env profiles, remote control between machines) and open design questions
- `hosts.md` — hosts-file management (`envmgr hosts`, `parent hosts`): managed block safety model and format, backups/lock/atomic write, validation, profile groups (`hosts apply`), the remote `--allow-hosts` grant and threat model, privileges per OS, tests
- `profiles.md` — profiles (variables and/or hosts entries): `use`/`deactivate` shell code and scopes (env/hosts/both), `ENVMGR_*` state variables, shell support and quoting, profile commands, tests
- `tray.md` — machine-wide profile selection (`select`/`selected`/`sync`/`follow`), pinned vs following shells (`ENVMGR_PINNED`), the prompt hook (`envmgr hook`), the Windows tray icon (`envmgr tray`), tests, limits
- `remote.md` — remote control, parent/child hub model (`envmgr parent`, `join`, `agent`): who controls whom, invite lifecycle, API, threat model, limits

- `tui.md` — `envmgr tui` (and bare `envmgr` in a terminal): screens, keys, stderr UI / stdout activation code, the `Screen` extension point, tests, limits

Related: `.claude/instructions/` for how-to guides, `.claude/plans/` for task plans, `.claude/skills/` for project skills.
