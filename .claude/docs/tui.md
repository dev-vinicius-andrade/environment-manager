# Terminal UI (`envmgr tui`)

Interactive Bubble Tea UI for profiles. Code: `internal/tui` (model, screens, styles) and `commands/envmgr/tui` (cobra command, terminal wiring). Libraries: `github.com/charmbracelet/bubbletea` v1.3.10, `bubbles` v1.0.0, `lipgloss` v1.1.0 (these raised the `go` directive in go.mod to 1.24.2).

## Starting it

| Shell | Command |
| --- | --- |
| bash, zsh | `eval "$(envmgr tui)"` |
| fish | `envmgr tui --shell fish \| source` |
| PowerShell | `envmgr tui --shell powershell \| Invoke-Expression` |
| cmd | `envmgr tui --shell cmd > "%TEMP%\envmgr.cmd" && "%TEMP%\envmgr.cmd"` |

`envmgr` with no arguments opens the same UI (auto-detected shell) when stdin and stderr are terminals; otherwise it prints help as before (`runCommand` in `commands/envmgr/envmgr.go`, via `tui.ShouldLaunch` / `tui.Run`).

## How activation works (stderr / stdout split)

A child process cannot change its parent shell, so the TUI follows the same contract as `use` / `deactivate`:

- The UI is drawn on **stderr** (`tea.WithOutput(stderr)`, alt screen). Keys come from stdin when it is a terminal, else from the controlling terminal (Bubble Tea opens `/dev/tty` / `CONIN$`). Colors are detected on stderr, so a captured stdout does not disable them.
- Activate (`enter`/`a`) and deactivate (`d`) end the program; the command then writes the code from `shell.Use` / `shell.Deactivate` (same functions, same `--shell` flag and auto-detection as `use` / `deactivate`) to **stdout**, and a one-line confirmation to stderr. The shell evaluates stdout.
- Activating pins the shell (`ENVMGR_PINNED=1`, like `use`): it ignores the tray / `envmgr select` until `envmgr follow`; deactivating unpins it (`.claude/docs/tray.md`).
- Quitting (`q`, `esc`, `ctrl+c`) writes nothing to stdout.
- Render errors (for example a newline value with `--shell cmd`, a reserved variable in a hand-edited profile) are shown in the status line; the UI stays open and nothing is written.
- **stdout is a terminal** (started without `eval`/pipe): nothing would evaluate the code, so activate/deactivate open a notice instead of exiting. It shows the exact one-liner (`eval "$(envmgr use dev --shell bash)"`, `envmgr use dev --shell powershell | Invoke-Expression`, ...) and how to start the TUI wrapped; `c` copies the one-liner (system clipboard via `atotto/clipboard`, falling back to the OSC 52 terminal escape). The status line also shows the wrapped start command in this case.
- Values are never written to stdout except inside activation code. On screen they are masked (`********`) unless revealed with `r`; revealed values have control characters escaped (`\n`, `\x1b`, ...), so a value cannot drive the terminal.

## Screen: Profiles

Left pane: profiles (`>` marks the cursor, `*` the active profile from `ENVMGR_PROFILE`). Right pane: the selected profile's details in two sections, **Variables** and **Hosts**, with a `[Variables (n)] Hosts (m)` line under the title. Header: tabs, active profile, target shell. Bottom: status line, then the help bar (always shown; `? help` and `q quit` are never cut).

| Key | Action |
| --- | --- |
| up/down, j/k (home/end, g/G) | move in the focused pane |
| left/right, h/l | focus profiles / details pane |
| tab, shift+tab | with the details pane focused: switch Variables / Hosts (the screen claims the key through `KeyClaimer`); with the list focused they keep their screen meaning |
| enter, a | activate the selected profile (exits, prints code). A profile with hosts entries first opens the scope dialog: `e`/enter environment (as before), `h` hosts only (envmgr writes the hosts file, the TUI stays open and shows the result or the permission error in the status line), `b` both (hosts first; on failure nothing is printed and the TUI stays open; on success it exits with the environment code), esc cancels. Profiles without hosts activate directly, as before |
| d | deactivate the active profile (exits, prints code); "no active profile" otherwise |
| n | new profile (name checked with `profile.ValidName`, must not exist) |
| e | set a variable: key then value (enter/tab moves, enter on value saves). With the variables pane focused, key and value are prefilled from the selected variable. Keys checked with `profile.ValidVar` and the `ENVMGR_` reserved prefix, like `envmgr set`. The value field is masked unless revealed. |
| x | delete the selected variable (variables pane focused; no confirmation) |
| e, x in the Hosts section | add / edit (prefilled when the details pane is focused) / delete a hosts entry: fields ip, hostnames (space separated), comment; tab/up/down move, enter validates the field and saves on the last one. Same rules as `envmgr profile hosts add` (`internal/hostspec`; critical names refused). Editing an entry replaces it (names and comment). Only the profile file changes; the status line reminds to apply again when the profile's hosts are applied |
| D | delete the selected profile, `y` confirms, `n`/`esc` cancels |
| r | reveal / mask values |
| / | filter profiles (case-insensitive substring; enter keeps, esc clears) |
| ? | help overlay (any key closes) |
| tab, shift+tab | next / previous screen (only one in v1) |
| q, esc, ctrl+c | quit without output (`ctrl+c` works in every mode; in prompts `esc` cancels the prompt instead) |

All store access goes through `profile.Store`. Editing a profile that is active shows a reminder to activate it again (the shell keeps the old values until then).

## Sizes and colors

Two panes from 60x15 up; narrower terminals show only the focused pane (switch with h/l); lists scroll to keep the cursor visible; below 24x6 only a "terminal too small" message is drawn. Resizes are handled through `tea.WindowSizeMsg`. Colors are `lipgloss.AdaptiveColor` (light and dark backgrounds); `NO_COLOR` (any non-empty value) forces the plain profile. Selection and the active profile also have text markers, so nothing depends on color.

## Design and extension point

- `App` (`internal/tui/tui.go`) is the frame: header with the tab list, status line, help bar, help overlay, global keys. It holds `[]Screen`; v1 registers only `Profiles` in `New`.
- `Screen` (`internal/tui/screen.go`): `Title`, `Update`, `View(width, height)`, `Capturing` (screen owns every key, e.g. text input), `Status`, `ShortHelp`, `FullHelp`. A screen ends the program with output by returning a command that yields `ExitMsg{Result}`; the command package prints `Result.Code` to stdout. Remote / parent screens plug in here; see `.claude/instructions/add-tui-screen.md`.
- Screens share `env` (store, shell kind, lookup, stdout-is-terminal, copy function, styles, active state read once at start, and the hosts file: `Options.HostsFile`, default `hosts.ResolvePath`).
- Optional `KeyClaimer` (`ClaimsKey(key)`): a screen may take a global key (tab, shift+tab) in its current state.
- Update logic is synchronous (the store is read in `New`, no async commands except `ExitMsg`; text inputs use a static cursor, so there are no blink timers).

## Tests

- `go test ./internal/tui`: drives the model with `tea.KeyMsg` / `tea.WindowSizeMsg` values through a harness that executes returned commands synchronously (no terminal). Covers navigation, active marker, mask/reveal and escaping, create profile (invalid, duplicate), add/edit/delete variable with store assertions in a temp dir, reserved/invalid names, delete confirmation, activate/deactivate code equal to `shell.Use` / `shell.Deactivate` for bash and PowerShell, render errors, quit with no result, the stdout-is-terminal notice and copy, filter, help overlay, and that every frame fits its size (120x40 down to 10x3, every mode).
- `internal/tui/hosts_test.go`: sections and tab, add/edit/delete hosts entry (invalid IP/name/critical refused in place), the scope dialog (environment, hosts only, both, failure shown in the status line with nothing printed, cancel, stdout-is-terminal), frames fit with the hosts modes. Uses a temp hosts file; `TestMain` points `ENVMGR_HOSTS_FILE` at a missing file.
- `go test ./commands/envmgr/tui`: runs the real `tea.Program` through the cobra command with key bytes on stdin and stdout/stderr captured; asserts stdout is exactly the `use` code and empty on quit.

## Limitations

- Values are single-line in the editor (the text input drops newlines; set multi-line values with `envmgr set`).
- `e` with a changed key adds a new variable; it does not rename the old one.
- The active profile shown is read once at start (it is the calling shell's environment).
- Only profiles; remote/parent screens are future work.
- No hosts "unapply" key; use `envmgr hosts unapply` (or `deactivate --only hosts`). Applying hosts from the TUI needs the TUI itself to run elevated.
