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

Second goal: one machine controlling another. v1 is a narrow, token + pinned-TLS authenticated profile agent (`envmgr serve` / `envmgr remote`); see `.claude/docs/remote.md` and `.claude/docs/goals.md`. Remote command execution is deliberately out of scope.

## Current state

Implemented and tested: profiles (`use`, `deactivate`, `list`, `current`, `set`, `unset`, `run`, `profile show|delete`) with bash/zsh/fish/powershell/cmd emitters (`.claude/docs/profiles.md`), an on-disk store (`ENVMGR_HOME` overrides the dir), and the remote agent/client. Run `go test -buildvcs=false ./...`; manual walkthrough in `.claude/instructions/e2e-demo.md`.

Not done: hosts-file management; remote command execution (by design); the fish round trip never ran (fish not installed). `services/` is empty and `main.go` has a placeholder `var version`. `go vet` reports a pre-existing `signal.Notify` warning in `helpers/spinner.go`. `.bin/` holds stale committed `nioscli` linux binaries (the `.gitignore` is a Visual Studio template). `types/context.EnvContext` is an empty shared context.

## Commands

- Build current platform: `make build` (output `.bin/<GOARCH>/<GOOS>/<BINARY_NAME>`; `GOOS`/`GOARCH` are make vars and are empty if not passed, giving `.bin//`). Version is injected via `-X main.version=$(VERSION)`.
- Cross-build: `make build_linux`, `build_windows`, `build_darwin`, `build_freebsd`, `build_all`; `make clean`.
- Run without building: `go run -buildvcs=false main.go` (`-buildvcs=false` is needed while git reports "dubious ownership" on this folder).
- No lint config or tests exist yet; use `go vet ./...` and `go test ./...` (single test: `go test ./pkg -run TestName`).

## Architecture

- `main.go` calls `envmgr.CreateCommand()` (cobra root, package `commands/envmgr`) and exits 1 on error.
- `commands/envmgr/envmgr.go`: root wrapper struct `commandDefinition` (cobra command + shared `*context.EnvContext`). Pattern: each subcommand package exposes `CreateCommand(ctx, parent)` and attaches itself; `defineSubCommands` wires them. See `.claude/instructions/add-subcommand.md`.
- `internal/profile` (store, name validation), `internal/shell` (state + per-shell emitters), `internal/remote` (server, client, remotes.json). All commands follow the same `CreateCommand` pattern and are registered in `envmgr.go`.
- `helpers/` (`CobraHelper` colored help, os/spinner/util helpers), `types/` (shared types, enums, colors, context), `constants/`, `utils/`. `types/interfaces.CommandDefinition` is unused.

## Git

Local repo was initialized fresh; remote is named `upstream` (`git@github.com:dev-vinicius-andrade/environment-manager.git`), there is no `origin`. Git reports "dubious ownership" until `safe.directory` is added to the global git config.
