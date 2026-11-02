# Add a cobra subcommand

Names: binary `envmgr`, module `github.com/dev-vinicius-andrade/environment-manager`. The canonical template is the VS Code snippet `create-new-command` in `.vscode/command.code-snippets`; follow it.

## Pattern

Each subcommand is its own package under `commands/envmgr/<name>/` with a private `commandDefinition` struct (`cmd`, `context *context.EnvContext`, `parentCommand`) and one exported constructor:

```go
func CreateCommand(context *context.EnvContext, parentCommand *cobra.Command) *cobra.Command
```

The constructor builds the cobra command, calls `defineFlags()` / `defineSubCommands()`, and attaches itself with `helpers.CobraHelper.AddCommandToParent(command, c.parentCommand)`. Nested subcommands repeat the same pattern with the parent being this command.

## Steps

1. Create `commands/envmgr/<name>/<name>.go` (`package <name>`) from the snippet (type `create-new-command` in VS Code) or copy its structure. Remove unused imports the snippet adds (`fmt`, `io/fs`, `os`, ...).
2. Put the logic in `runCommand`; define flags in `defineFlags()` on `c.cmd.Flags()` and store values on the shared `EnvContext` (`types/context/context.go`) or the local struct, not globals.
3. Register it in `defineSubCommands()` of `commands/envmgr/envmgr.go`: `<name>.CreateCommand(c.context, c.cmd)`.
4. Verify: `go build -buildvcs=false ./...` then `go run -buildvcs=false main.go <name> --help`.
5. Update docs: document the command in `.claude/docs/` and index it in `.claude/docs/README.md`.

## Notes

- Commands that must change the user's shell (activate/deactivate a profile) write the shell code to **stdout** only and send messages/errors to stderr, so `eval "$(envmgr use dev)"` / `| Invoke-Expression` is not polluted.
- Use `helpers.CobraHelper.ShowHelp` to print help with a colored title when a command is run without required args.
