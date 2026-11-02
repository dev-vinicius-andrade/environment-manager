# Environment profiles

Named profiles of environment variables, activated per shell session. Implemented in `internal/profile` (storage), `internal/shell` (shell code generation) and the commands under `commands/envmgr/{use,deactivate,list,current,set,unset,run,profile}`.

## Storage

`internal/profile.Store`: one JSON file per profile at `$ENVMGR_HOME/profiles/<name>.json` (default `<user config dir>/envmgr`), mode 0600. Profile names match `^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`; variable names match `^[A-Za-z_][A-Za-z0-9_]*$` (`profile.ValidVar`). Names starting with `ENVMGR_` (case-insensitive) are reserved for envmgr's state and rejected by `set`, `use` and `run`.

## Why activation prints code

A child process cannot change its parent shell's environment. `use` and `deactivate` therefore print shell code on **stdout** for the shell to evaluate; messages and errors go to **stderr**. On any error (unknown profile, bad shell, value the shell cannot represent) stdout stays empty and the exit code is 1, so `eval` evaluates nothing.

## State variables (in the shell's own environment)

| Variable | Meaning |
| --- | --- |
| `ENVMGR_PROFILE` | name of the active profile |
| `ENVMGR_VARS` | comma-separated, sorted names of the variables the profile set |
| `ENVMGR_PREV_<VAR>` | value `<VAR>` had before activation; absent means `<VAR>` did not exist |

`use <p>` emits, in order: restore every variable in `ENVMGR_VARS` (to `ENVMGR_PREV_<VAR>`, or unset it) and drop its backup; for each variable of `p`, back up its original value (the backup of the previous profile if it managed it, else the current value) and set it; set `ENVMGR_PROFILE` / `ENVMGR_VARS`. Switching A to B therefore restores variables A set and B does not, and re-running `use` on the same profile keeps the true original as backup. `deactivate` restores everything and unsets the state; with nothing active it prints nothing. Names read back from `ENVMGR_VARS` are re-validated, so a tampered value can never inject a name into shell code. Backups are computed by envmgr from its inherited environment (only exported variables are visible).

## Shell support (`--shell`, default: powershell on Windows, else from `$SHELL`: fish, zsh, otherwise bash)

| Shell | Activate | Set / unset form | Quoting |
| --- | --- | --- | --- |
| bash, zsh | `eval "$(envmgr use dev)"` | `export V='..'` / `unset V` | single quotes, `'` written as `'\''` |
| fish | `envmgr use dev --shell fish \| source` | `set -gx V '..'` / `set -e V` | single quotes, `\` and `'` backslash-escaped |
| PowerShell | `envmgr use dev --shell powershell \| Invoke-Expression` | `$env:V = '..'` / `$env:V = $null` | printable ASCII in single quotes (`''` for `'`); every other character (newlines, typographic quotes, non-ASCII) as `[char]0xNNNN`, so output is ASCII and one statement per line (Invoke-Expression evaluates piped lines one by one; Windows PowerShell decodes native output with the OEM code page) |
| cmd | `envmgr use dev --shell cmd > "%TEMP%\envmgr.cmd" && "%TEMP%\envmgr.cmd"` | `@set V=..` / `@set V=` | unquoted, `^ & \| < > ( ) "` caret-escaped, `%` doubled; assumes delayed expansion off (the default) |

Limits: values containing NUL are rejected for every shell. cmd only accepts printable ASCII and tab (it cannot hold newlines in a `set` line and decodes batch files with the console code page); other values, including the backup of an existing non-ASCII variable, make `use --shell cmd` fail with an error suggesting PowerShell. On Windows shells, setting a variable to an empty value removes it.

## Commands

- `envmgr use <profile> [--shell S]` / `envmgr deactivate [--shell S]`: print activation / restore code (above).
- `envmgr list`: profile names, the active one (from `ENVMGR_PROFILE`) prefixed with `*`.
- `envmgr current`: prints `ENVMGR_PROFILE`, or nothing; always exit 0.
- `envmgr set <profile> <VAR> <value>`: creates the profile if missing; values starting with `-` are accepted. Does not change the current shell (re-run `use`).
- `envmgr unset <profile> <VAR>`: removes a variable; a variable not in the profile is a stderr notice, exit 0.
- `envmgr profile show <name> [--reveal]`: `NAME=value` lines, values shown as `********` unless `--reveal`.
- `envmgr profile delete <name>`: removes the file; shells where it is active keep their state and can still `deactivate`.
- `envmgr run <profile> [--] <cmd> [args...]`: runs the command with the profile's variables appended to the current environment (stdio passed through, envmgr exits with the child's exit code). It does not undo an active profile's variables nor update `ENVMGR_*` for the child.

## Tests

`go test ./internal/shell` has table tests per shell (exact output, injection payloads, A to B switching, deactivate) and real round trips that evaluate the output the way a user would: bash (`eval "$(...)"`), PowerShell (`Get-Content | Invoke-Expression`, line by line), cmd (`call` of a .cmd file, Windows only) and fish (`| source`). Each round trip skips when the shell is not available. Command tests use a temp `ENVMGR_HOME` via `t.Setenv`.
