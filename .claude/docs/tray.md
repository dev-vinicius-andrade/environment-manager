# Tray icon, machine-wide selection and the shell hook

Owner request: on Windows, a system tray icon from which the user picks the profile, applied to **open shells** (not to the registry). A terminal can override the tray (pinned) and go back with `envmgr follow`.

Code: `internal/selected` (selection file + sync decision), `internal/hook` (hook scripts, profile-file install), `internal/tray` (menu model, icon, Windows app), commands `select`, `selected`, `sync`, `follow`, `hook`, `tray` (packages `commands/envmgr/{selectcmd,selected,synccmd,follow,hook,tray}`; `select` is a Go keyword, hence `selectcmd`).

## Selection model

- One machine-wide selection per envmgr home: `<ENVMGR_HOME>/selected.json` = `{"profile": "<name>", "env": bool, "hosts": bool, "updatedAt": "<RFC 3339>"}`; `""` means none. `env` / `hosts` are the selected **scopes**. A file without them (written before scopes existed) means env only. Written atomically (temp file + rename, mode 0600; on Windows reads and renames retry briefly on sharing violations; `selected.WriteFileAtomic`, also used for `hosts-applied.json`). It holds a name only, never values.
- `selected.Selector`: `Get` (missing file = none; an invalid name in the file is an error, so it never reaches shell code), `Set(name)` (env only), `SetScoped(name, Scopes{Env, Hosts})` (at least one; `profile.ValidName`, the profile must exist), `Clear()` (writes an empty profile, so watchers see a change). `Selection.EnvProfile()` / `HostsProfile()`.
- `envmgr select <profile> [--hosts | --only env|hosts|all | --env]` / `envmgr select --none` set it (messages on stderr, nothing on stdout; with the hosts scope it says the hosts are not applied by select); `envmgr selected` prints the profile (or nothing) and the scopes on stderr.
- **The hosts scope is a record of intent.** The hook, `sync` and `follow` follow only the env scope (a hosts-only selection counts as none for shells) and never write the hosts file (that needs elevation). When the selection has the hosts scope and that profile is not the applied one (`hosts-applied.json`), `sync` prints one stderr line telling the user to run `envmgr hosts apply <p>` in an elevated shell, **once per selection change** (remembered by profile + `updatedAt` in `<ENVMGR_HOME>/hosts-hint.json`), so the hook does not repeat it on every prompt.

## Pinned vs following

A shell session is **pinned** when `ENVMGR_PINNED=1` is in its environment, otherwise it **follows** the selection.

| Action in the shell | Effect |
| --- | --- |
| `envmgr use p`, TUI activate | activates p and pins (`ENVMGR_PINNED=1`) |
| `envmgr deactivate`, TUI deactivate | restores and clears the pin (the shell follows again from the next selection change) |
| hook / `envmgr sync` | applies the selection without pinning (emits `unset ENVMGR_PINNED`) |
| `envmgr follow` | clears the pin and applies the selection now |

`envmgr current` prints the profile on stdout as before; on stderr (when it is a terminal, or always with `--status`) it adds `(pinned)` or `(following tray)`.

## `envmgr sync --shell S` decision table (`selected.Decide`)

| Session / selection | Output |
| --- | --- |
| pinned | nothing |
| no selection, profile active | deactivate code |
| no selection, nothing active | nothing |
| selected profile missing (deleted) | deactivate code if active, plus a stderr note |
| selection equals `ENVMGR_PROFILE` | nothing |
| otherwise (switch or first activation) | same code as `use`, but `ENVMGR_PINNED` is unset instead of set |

stdout carries code only (empty when nothing changes); a one-line notice goes to stderr when something changes. On error (unreadable selection, value the shell cannot represent) stdout is empty and the exit code is 1. `follow` = sync ignoring the pin, always clearing it (only `unset ENVMGR_PINNED` when the shell already has the selection).

## Shell hook (`envmgr hook <powershell|bash|zsh|fish>`)

Prints init code; `--install` puts it in the profile file (see `.claude/instructions/install-shell-hook.md`). On every prompt the hook:

1. returns at once when the shell is pinned;
2. does a cheap in-shell check of `selected.json` (path from `$ENVMGR_HOME` at prompt time, else the default embedded at generation): PowerShell compares `LastWriteTimeUtc.Ticks` + `Length` (`[System.IO.FileInfo]`, no process); bash/zsh/fish read the file with the `read` builtin and compare the content (no fork);
3. only when that differs from the value cached in the session, runs `envmgr sync --shell S` (absolute path of the binary that generated the hook) and evaluates its output.

A new shell syncs once on its first prompt. Guarantees (tested): nothing printed when nothing changed; `$?` / `$LASTEXITCODE` preserved; the existing prompt is kept (PowerShell wraps the current `prompt` function, bash prepends to `PROMPT_COMMAND` string or array, zsh uses `add-zsh-hook precmd`, fish a `fish_prompt` event function); sourcing twice is harmless (PowerShell detects its own wrapper, bash checks `PROMPT_COMMAND`). Paths are quoted with the same quoting functions as activation code; the PowerShell script is pure ASCII (`[char]0xNNNN` for other characters). cmd has no prompt hook: `hook cmd` fails with an explanation (use `envmgr follow --shell cmd` by hand).

Must be evaluated as a whole in PowerShell: `envmgr hook powershell | Out-String | Invoke-Expression` (the line-by-line `| Invoke-Expression` used for activation code would break the function definitions).

## Tray (`envmgr tray`, Windows only)

- Library `fyne.io/systray` v1.11.0 (no cgo on Windows; imported only from `_windows.go` files). Other platforms: `envmgr tray` fails with "tray is only supported on Windows" (`internal/tray/app_other.go`, runtime check in the command).
- `envmgr tray` relaunches itself as `tray --foreground` with `CREATE_NO_WINDOW | DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP` and returns (prints the PID); `--foreground` runs in place. One instance per envmgr home: named mutex `Local\envmgr-tray-<sha256(home)[:16 hex]>` (a second start reports "already running").
- Icon: generated `.ico` bytes (16 and 32 px, 32-bit): gray ring = no profile selected (or the selected one is missing), green disc = profile selected (any scope). Tooltip `envmgr: <profile>` (env only), `... (env + hosts)`, `... (hosts only)`, `envmgr: none`, or `envmgr: <profile> (missing)`, plus `; hosts applied: <p>` when `hosts-applied.json` names a profile.
- Menu: one checkbox item per profile (sorted, checked when selected with any scope), each with a submenu: `Environment` and `Hosts` check items (the selected scopes) and `Apply hosts (asks for administrator)...`; then `None (deactivate)`, separator, `Open TUI`, `Open profiles folder`, `Start with Windows` (checked state from the registry), `Quit`. No profiles: a disabled "No profiles" item. More than 40 profiles: the first 40 plus a disabled "N more: use the TUI" item; a selection beyond the cap replaces the 40th entry so its check mark is visible.
- Refresh: every 2 s the tray re-reads the store, the selection, `hosts-applied.json` and the Run key, builds a new `Model` and applies only `Diff(old, new)` (a fixed pool of 40 menu items with their submenus is retitled, checked, shown and hidden), so CLI changes show up.
- Clicks: `React(model, target)` returns the action (pure). `Environment` / `Hosts` toggle that scope of the profile (another profile starts with no scope; turning the last scope off clears the selection) and call `selected.SetScoped` / `Clear`. **Choosing Hosts never writes the hosts file** (the tray is not elevated). `Apply hosts (asks for administrator)...` is the only way the tray applies hosts: it calls `ShellExecute` with the `runas` verb on the envmgr binary with `ElevatedApplyArgs(home, ENVMGR_HOSTS_FILE, p)` = `hosts apply <p> --home <home> [--file <f>] --pause` (an elevated process does not inherit this environment, hence the explicit home/file; `--pause` keeps its console open until Enter). Windows shows its UAC prompt; declining starts nothing. No silent elevation, no stored credentials. The argument builder and its Windows quoting (`WindowsCommandLine`, checked against `windows.DecomposeCommandLine`) are unit tested; the `ShellExecute` call is a thin untested wrapper. Other clicks open the TUI (Windows Terminal `wt new-tab ... envmgr tui` when `wt` exists and `ENVMGR_HOME` is not overridden, else a new PowerShell console running `& '<exe>' tui`), opens `<home>\profiles` in Explorer, or toggles `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` value `envmgr-tray` = `"<exe>" tray` (no admin; untoggle deletes the value).
- The tray never changes another process's environment nor the registry environment keys.
- Debug: `ENVMGR_TRAY_DEBUG=1` logs start, every model change (`Model.Summary()`) and clicks to `ENVMGR_TRAY_LOG` or `%TEMP%\envmgr-tray\debug.log`.

## Tests

- `internal/selected`: Get/Set/Clear, validation (invalid names, missing profile, tampered file), atomic writes under a concurrent reader, the decision table, a full sync/follow scenario simulated for all five shells, render errors leave no code, sync never pins while use does.
- `internal/hook`: golden snapshots per shell (`testdata/hook-*.golden`; regenerate with `go test ./internal/hook -update`), quoting/ASCII, install blocks, profile paths, install/uninstall planning (idempotent, CRLF kept, only the block removed). Real round trips with a test build (`testdata/envmgrtest`, the real root plus these commands) and a temp `ENVMGR_HOME`: bash (Git Bash on Windows; WSL's `bash.exe` is skipped) and PowerShell run select a, select b, `use c` (pinned stays on c), `follow`, `select --none`, use+deactivate then select; plus prompt/`PROMPT_COMMAND` preservation, `$?` / `$LASTEXITCODE`, silence when unchanged, sourcing twice. zsh/fish have snapshots only (not installed here).
- `internal/tray`: model (checked item, cap, missing selection, empty, scopes and tooltip), `Diff`, `React` (scope toggles, apply hosts), icon structure, helper quoting, `ElevatedApplyArgs` and the Windows command-line round trip.
- `internal/selected`: old `selected.json` without scopes reads as env only; scopes written and read back; a hosts-only selection is "none" for shells. `commands/envmgr/synccmd`: `select --hosts/--only/--env`, `selected` scopes, the sync hint printed once per selection change.
- Commands: `commands/envmgr/{synccmd,hook,current,tray}` tests.

Manual check done on Windows 11: `tray --foreground` started from a background process stayed alive, a second instance was refused, the debug log followed `select dev`, a new profile, `select --none` and a deleted selected profile; the detached `tray` launch started a windowless process (each stopped by its own PID). Clicking the real icon/menu could not be automated.

## Limits

- A shell picks up a change at its next prompt, not while a command runs; shells without the hook (and cmd) only change with `follow`/`use`.
- After `deactivate` the shell is unpinned but only reacts to the next selection change (the hook caches the file state).
- Editing the selected profile's variables is not detected (the selection file did not change); re-select or `follow`.
- Child shells inherit `ENVMGR_*` (including the pin) from their parent.
- "Start with Windows" always uses the default home (the Run entry cannot carry `ENVMGR_HOME`).
- The tray has no "remove applied hosts" item: use `envmgr hosts unapply` in an elevated shell. The submenu and the UAC launch were not clicked by hand in this change (only built and unit tested).
- The hook embeds the absolute path of the binary that generated it; moving the binary needs `hook --install` again (the installed block is skipped silently when the binary is gone).
