# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Working rules

- Everything Claude-related lives under `.claude/`: this file, `docs/`, `instructions/`, `plans/`, `skills/`, tools, agents, settings. Do not put Claude artifacts at the repo root.
- Before starting any task, check `.claude/docs/README.md`, `.claude/instructions/README.md` (indexes) and `.claude/plans/` for existing documentation, how-to instructions or plans that support it, and read them first.
- `.claude/docs/` explains what exists and why; `.claude/instructions/` explains how to do a specific task step by step. Write a new instruction when a procedure is repeated or non-obvious.
- Every change must decide whether documentation or instructions need adding or updating (`.claude/docs/`, `.claude/instructions/`, README, this file). Update the matching index when a file is added or removed.

## Project goal

Binary/identity name: **envmgr** (repo `environment-manager`, module `github.com/dev-vinicius-andrade/environment-manager`).

CLI to manage the user's environment (hosts, environment variables). Intended core feature: **named profiles of environment variables** (`local`, `dev`, `external`, `test1`...), where activating a profile makes a variable like `MY_API_URL` take that profile's value in the current shell session only (multiple GCP accounts, multiple auth keys, etc.). Implemented (see Current state).

Design constraint for that feature: a child process cannot mutate its parent shell's environment, so activation must work by emitting shell code for the shell to evaluate (`eval "$(envmgr use dev)"` / `envmgr use dev | Invoke-Expression`), `direnv`-style, with deactivate restoring the previous values. Primary dev platform is Windows/PowerShell, so PowerShell output is a priority alongside bash/zsh/fish.

Second goal: one machine controlling another. Hub model: a **parent** (`envmgr parent serve`) issues a single-use, expiring join code (`parent invite`); a **child** runs `envmgr join <code>`, dials the parent over pinned TLS and stays bound to that one parent, which can run profile operations on it (revocable, optionally read-only). See `.claude/docs/remote.md` and `.claude/docs/goals.md`. Remote command execution is deliberately out of scope.

The app is a terminal app with a TUI (`envmgr tui`, also bare `envmgr` in a terminal); it renders on stderr so stdout can carry activation shell code (`.claude/docs/tui.md`).

## Current state

Implemented and tested: profiles (`use`, `deactivate`, `list`, `current`, `set`, `unset`, `run`, `profile show|delete|hosts`) with bash/zsh/fish/powershell/cmd emitters (`.claude/docs/profiles.md`); a profile holds variables, hosts entries or both, applied by scope (`use --hosts`, `--only env|hosts|all`, `hosts apply|unapply|applied`; `.claude/instructions/profiles-with-hosts.md`), an on-disk store (`ENVMGR_HOME` overrides the dir), the parent/child remote model (`parent`, `join`, `agent`), hosts-file management through an envmgr-managed block (`envmgr hosts`, and `parent hosts` behind the explicit `invite --allow-hosts` grant; `.claude/docs/hosts.md`), the profiles TUI, and a machine-wide profile selection followed by open shells: Windows tray icon (`envmgr tray`), `select`/`selected`/`sync`/`follow`, a prompt hook (`envmgr hook <shell> [--install]`), sessions pinned by `use`/TUI via `ENVMGR_PINNED` (`.claude/docs/tray.md`). Run `go test -buildvcs=false ./...`; manual walkthrough in `.claude/instructions/e2e-demo.md`. The module needs Go 1.24+ (Charm libraries).

Not done: a TUI tab for the raw hosts block (profile hosts are edited/applied in the profiles TUI); remote command execution (by design); TUI screens for parent/children; running parent/child as background services; the fish round trip never ran (fish not installed). `services/` is empty and `main.go` has a placeholder `var version`. `go vet` reports a pre-existing `signal.Notify` warning in `helpers/spinner.go`. `.bin/` holds stale committed `nioscli` linux binaries (the `.gitignore` is a Visual Studio template). `types/context.EnvContext` is an empty shared context.

## Commands

- Build current platform: `make build` (output `.bin/<GOARCH>/<GOOS>/<BINARY_NAME>`; `GOOS`/`GOARCH` are make vars and are empty if not passed, giving `.bin//`). Version is injected via `-X main.version=$(VERSION)`.
- Cross-build: `make build_linux`, `build_windows`, `build_darwin`, `build_freebsd`, `build_all`; `make clean`.
- Run without building: `go run -buildvcs=false main.go` (`-buildvcs=false` is needed while git reports "dubious ownership" on this folder).
- No lint config or tests exist yet; use `go vet ./...` and `go test ./...` (single test: `go test ./pkg -run TestName`).

## Architecture

- `main.go` calls `envmgr.CreateCommand()` (cobra root, package `commands/envmgr`) and exits 1 on error.
- `commands/envmgr/envmgr.go`: root wrapper struct `commandDefinition` (cobra command + shared `*context.EnvContext`). Pattern: each subcommand package exposes `CreateCommand(ctx, parent)` and attaches itself; `defineSubCommands` wires them. See `.claude/instructions/add-subcommand.md`.
- `internal/profile` (store, name validation, profile hosts entries), `internal/shell` (state + per-shell emitters), `internal/remote` (hub, child agent loop, join codes, TLS pinning), `internal/hosts` (hosts-file managed block), `internal/hostspec` (shared IP/hostname/comment validation), `internal/hostsapply` (apply a profile's hosts, `hosts-applied.json`), `internal/selected` (machine-wide selection file, sync decision), `internal/hook` (prompt hook scripts, profile-file install), `internal/tray` (tray menu model, icon; Windows app on `fyne.io/systray`), `internal/tui` (Bubble Tea screens). All commands follow the same `CreateCommand` pattern and are registered in `envmgr.go`.
- `helpers/` (`CobraHelper` colored help, os/spinner/util helpers), `types/` (shared types, enums, colors, context), `constants/`, `utils/`. `types/interfaces.CommandDefinition` is unused.

## Git

Local repo was initialized fresh; remote is named `upstream` (`git@github.com:dev-vinicius-andrade/environment-manager.git`), there is no `origin`. Git reports "dubious ownership" until `safe.directory` is added to the global git config.
