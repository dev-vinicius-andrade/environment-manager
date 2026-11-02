# Environment profiles

Named profiles, activated per shell session. A profile holds **environment variables, hosts entries, or both**, and the user applies whatever they want: environment only (default), hosts only, or both (owner request). Implemented in `internal/profile` (storage, hosts entries), `internal/shell` (shell code generation), `internal/hostsapply` (applying a profile's hosts) and the commands under `commands/envmgr/{use,deactivate,list,current,set,unset,run,profile}`. Hosts entries: see "Hosts entries" below and `hosts.md`; step by step: `.claude/instructions/profiles-with-hosts.md`.

## Storage

`internal/profile.Store`: one JSON file per profile at `$ENVMGR_HOME/profiles/<name>.json` (default `<user config dir>/envmgr`), mode 0600. Profile names match `^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`; variable names match `^[A-Za-z_][A-Za-z0-9_]*$` (`profile.ValidVar`). Names starting with `ENVMGR_` (case-insensitive) are reserved for envmgr's state and rejected by `set`, `use` and `run`.

File format: `{"name", "vars": {...}, "hosts": [{"ip", "names": [...], "comment"?}]}`. `hosts` is omitted when empty, so files written before hosts existed load unchanged and a variables-only profile is saved byte for byte as before. A profile with neither vars nor hosts is valid. Version note: an older envmgr reading a newer file ignores `hosts`, and drops it if it saves the profile (`set`/`unset`).

## Hosts entries

- `profile.HostEntry{IP, Names, Comment}`. Validated with the same functions as the hosts file (`internal/hostspec`, shared by `internal/hosts` and `internal/profile`, so there is one implementation and no import cycle): IPv4/IPv6 via `net/netip` (no zone ids, no leading zeros), RFC 1123 names lower-cased and de-duplicated, 1 to 32 names per entry, at most 256 entries, an IP in one entry only, a name mapped once. `Comment` is optional (at most 128 bytes, no `#`, no control or format characters) and stays in the profile: it is not written to the hosts file.
- Critical names (`localhost`, `broadcasthost`, `ip6-*`) are refused when an entry is added and when a profile is applied (never with `--force`); a hand-edited profile containing one still loads.
- Store helpers: `SetHost(profile, ip, names...)`, `SetHostEntry(profile, entry)` (with comment), `UnsetHost(profile, hostname)`; `Profile.AddHost`, `RemoveHostname`, `RemoveHostIP`. Adding a name already mapped to another IP moves it (same rule as `hosts add`).
- Editing: `envmgr profile hosts add <profile> <ip> <hostname>... [--comment c]` (creates the profile if missing), `envmgr profile hosts remove <profile> <hostname>|<ip>`. These edit the profile file only. (Chosen over top-level `host-set`/`host-unset`: it mirrors `envmgr hosts add|remove` and keeps the top level for applying.)
- Applying is machine-wide, not per shell: see "Scopes" below and `hosts.md` ("Profile hosts").

## Why activation prints code

A child process cannot change its parent shell's environment. `use` and `deactivate` therefore print shell code on **stdout** for the shell to evaluate; messages and errors go to **stderr**. On any error (unknown profile, bad shell, value the shell cannot represent) stdout stays empty and the exit code is 1, so `eval` evaluates nothing.

## State variables (in the shell's own environment)

| Variable | Meaning |
| --- | --- |
| `ENVMGR_PROFILE` | name of the active profile |
| `ENVMGR_VARS` | comma-separated, sorted names of the variables the profile set |
| `ENVMGR_PREV_<VAR>` | value `<VAR>` had before activation; absent means `<VAR>` did not exist |
| `ENVMGR_PINNED` | `1` when the session chose its profile (`use`, TUI activate): it ignores the machine-wide selection until `envmgr follow`; cleared by `deactivate`, `sync` and `follow` (`.claude/docs/tray.md`) |

`use <p>` emits, in order: restore every variable in `ENVMGR_VARS` (to `ENVMGR_PREV_<VAR>`, or unset it) and drop its backup; for each variable of `p`, back up its original value (the backup of the previous profile if it managed it, else the current value) and set it; set `ENVMGR_PROFILE` / `ENVMGR_VARS`, then `ENVMGR_PINNED=1` (`shell.UseOps`; `shell.FollowUseOps`, used by `sync`/`follow`, unsets it instead). Switching A to B therefore restores variables A set and B does not, and re-running `use` on the same profile keeps the true original as backup. `deactivate` restores everything and unsets the state; with nothing active it prints nothing. Names read back from `ENVMGR_VARS` are re-validated, so a tampered value can never inject a name into shell code. Backups are computed by envmgr from its inherited environment (only exported variables are visible).

## Shell support (`--shell`, default: powershell on Windows, else from `$SHELL`: fish, zsh, otherwise bash)

| Shell | Activate | Set / unset form | Quoting |
| --- | --- | --- | --- |
| bash, zsh | `eval "$(envmgr use dev)"` | `export V='..'` / `unset V` | single quotes, `'` written as `'\''` |
| fish | `envmgr use dev --shell fish \| source` | `set -gx V '..'` / `set -e V` | single quotes, `\` and `'` backslash-escaped |
| PowerShell | `envmgr use dev --shell powershell \| Invoke-Expression` | `$env:V = '..'` / `$env:V = $null` | printable ASCII in single quotes (`''` for `'`); every other character (newlines, typographic quotes, non-ASCII) as `[char]0xNNNN`, so output is ASCII and one statement per line (Invoke-Expression evaluates piped lines one by one; Windows PowerShell decodes native output with the OEM code page) |
| cmd | `envmgr use dev --shell cmd > "%TEMP%\envmgr.cmd" && "%TEMP%\envmgr.cmd"` | `@set V=..` / `@set V=` | unquoted, `^ & \| < > ( ) "` caret-escaped, `%` doubled; assumes delayed expansion off (the default) |

Limits: values containing NUL are rejected for every shell. cmd only accepts printable ASCII and tab (it cannot hold newlines in a `set` line and decodes batch files with the console code page); other values, including the backup of an existing non-ASCII variable, make `use --shell cmd` fail with an error suggesting PowerShell. On Windows shells, setting a variable to an empty value removes it.

## Scopes: environment, hosts, both

| Command | Environment (shell code on stdout) | Hosts (written by envmgr into the hosts file) |
| --- | --- | --- |
| `use p` (default), `use p --only env` | yes | no |
| `use p --hosts`, `use p --only all` | yes | yes |
| `use p --only hosts` (= `hosts apply p`) | no, stdout stays empty | yes |
| `deactivate` / `--hosts` / `--only hosts` (= `hosts unapply`) | same pattern | removes the applied profile's group |

- The environment part is per shell (code on stdout, as above). The hosts part is machine-wide: envmgr writes the managed block itself (group `profile:<name>`, replacing the previously applied profile; backup, lock, atomic replace; see `hosts.md`) and records it in `<ENVMGR_HOME>/hosts-applied.json`.
- Order and failure rule (decided): the shell code is rendered first, then the hosts file is written, then stdout is written. If the hosts part fails (typically permission denied: not elevated), **nothing is printed on stdout and the exit code is 1**, so the shell is never half applied. `--best-effort` prints the environment code anyway, with a warning on stderr, exit 0. A render error (e.g. a newline with `--shell cmd`) aborts before the hosts file is touched.
- `--dry-run` (needs the hosts scope): prints the hosts diff on stderr, writes nothing, prints no shell code. `--file` / `ENVMGR_HOSTS_FILE` pick another hosts file.
- `deactivate --hosts` removes whatever profile group is applied on the machine, not necessarily the shell's active profile (the hosts state is machine-wide).
- `run` stays environment-only on purpose: it changes one child process's environment, while hosts are machine-wide and need elevation; mixing a per-process command with a machine-wide side effect would surprise.

## Commands

- `envmgr use <profile> [--shell S] [--hosts | --only env|hosts|all] [--best-effort] [--dry-run] [--file F]` / `envmgr deactivate [same flags]`: print activation / restore code (above) and apply/remove the hosts part per scope.
- `envmgr list`: profile names, the active one (from `ENVMGR_PROFILE`) prefixed with `*`.
- `envmgr current [--status]`: prints `ENVMGR_PROFILE`, or nothing; always exit 0. On stderr (terminal, or always with `--status`) it adds `(pinned)` or `(following tray)`.
- `envmgr select <profile>` / `select --none`, `envmgr selected`: set / print the machine-wide selection; `envmgr sync [--shell S]` (used by the hook) and `envmgr follow [--shell S]` print code that applies it; `envmgr hook <shell> [--install|--uninstall] [--dry-run]` prints or installs the prompt hook. See `.claude/docs/tray.md`.
- `envmgr set <profile> <VAR> <value>`: creates the profile if missing; values starting with `-` are accepted. Does not change the current shell (re-run `use`).
- `envmgr unset <profile> <VAR>`: removes a variable; a variable not in the profile is a stderr notice, exit 0.
- `envmgr profile show <name> [--reveal] [--section vars|hosts]`: `NAME=value` lines, values shown as `********` unless `--reveal`, then hosts entries as `IP NAME... [# comment]` (never masked). With hosts entries the full view adds `# variables` / `# hosts` headers; a variables-only profile prints exactly as before. `--section` prints one part without header.
- `envmgr profile hosts add|remove ...`: edit the hosts entries (above).
- `envmgr hosts apply <profile>` / `unapply` / `applied`: the hosts part only (`hosts.md`).
- `envmgr profile delete <name>`: removes the file; shells where it is active keep their state and can still `deactivate`.
- `envmgr run <profile> [--] <cmd> [args...]`: runs the command with the profile's variables appended to the current environment (stdio passed through, envmgr exits with the child's exit code). It does not undo an active profile's variables nor update `ENVMGR_*` for the child.

## Tests

`go test ./internal/shell` has table tests per shell (exact output, injection payloads, A to B switching, deactivate) and real round trips that evaluate the output the way a user would: bash (`eval "$(...)"`), PowerShell (`Get-Content | Invoke-Expression`, line by line), cmd (`call` of a .cmd file, Windows only) and fish (`| source`). Each round trip skips when the shell is not available. Command tests use a temp `ENVMGR_HOME` via `t.Setenv`.

Hosts in profiles: `internal/profile/profile_test.go` (old file loads and re-saves byte for byte, round trip with hosts, hosts-only and empty profiles, validation table: newline/`#`/space injection in names, bad/zone/leading-zero IPs, comment injection, too many names/entries, duplicates; store helpers), `internal/hostsapply` (apply, A replaced by B, unapply, unmanaged lines byte for byte with LF and CRLF, dry run, state file, permission error, scope parsing, sync hint), `commands/envmgr/use` and `deactivate` (`*_hosts_test.go`: the flag matrix, failing hosts write leaves stdout empty and exits 1, `--best-effort`, `--dry-run`, `--file`), `commands/envmgr/profile` (`profile hosts`, `show --section`). Every one of these packages points `ENVMGR_HOSTS_FILE` at a missing temp file in `TestMain`.
