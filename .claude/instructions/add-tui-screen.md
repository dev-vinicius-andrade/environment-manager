# Add a TUI screen (tab)

The TUI (`envmgr tui`, see `.claude/docs/tui.md`) is an `App` frame around a list of `Screen` values; each screen is one tab. Use this to add, for example, a Remotes or Parents screen.

## Steps

1. Create `internal/tui/<name>.go` with a type implementing `Screen` (`internal/tui/screen.go`):
   - `Title()`: tab label.
   - `Update(msg)`: handle `tea.KeyMsg` and your own messages. Keys arrive after the App's global keys: `ctrl+c` always quits; `q`, `esc`, `?`, `tab`, `shift+tab` are global only while `Capturing()` is false.
   - `View(width, height)`: render exactly `width x height` or less. Use the helpers in `text.go` (`block`, `pad`, `fit`, `window`, `wrapLines`, `printable` for untrusted text such as values or names from the network).
   - `Capturing()`: true while a text input, confirmation or notice owns the keyboard.
   - `Status()`, `ShortHelp()`, `FullHelp()`: status line, bottom help bar hints, `?` overlay entries.
2. Share state through `*env` (store, shell kind, lookup, styles, copy). Add a field there (and to `Options` if the command must provide it) rather than using globals; keep I/O behind an interface or func field so tests can stub it.
3. Register the screen in `New` in `internal/tui/tui.go`: append to `screens` (the comment "Extension point" marks the spot). The header shows the tab automatically; `tab` / `shift+tab` switch.
4. Output contract: never write to stdout. To end the program with shell code, return `exitCmd(Result{Action: ..., Code: ..., Message: ...})`; `commands/envmgr/tui` prints `Code` to stdout and `Message` to stderr. If `env.stdoutTTY` is true, show the one-liner instead of exiting (see `profilesScreen.showNotice`).
5. Long-running work (network): return a `tea.Cmd` that does the work and yields a result message; handle it in `Update`. Keep a timeout and show errors in `Status()`. Do not block in `Update` or `View`.
6. Styles: add any new style to `Styles` in `styles.go` using `lipgloss.AdaptiveColor`, and pair color with a text marker.
7. Tests: add `internal/tui/<name>_test.go` using the harness in `tui_test.go` (`newHarness`, `keys`, `typeText`, `view`, `checkFits`). Select the screen with `h.keys("tab")`. Cover navigation, every mutation (assert the store or a fake), invalid input, and that views fit small sizes.
8. Docs: add the screen and its keys to `.claude/docs/tui.md`.
9. Verify: `gofmt -l internal/tui`, `go vet ./internal/tui ./commands/envmgr/tui`, `go test -buildvcs=false -count=1 ./internal/tui ./commands/envmgr/tui`.
