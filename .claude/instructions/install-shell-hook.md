# Install the shell hook (follow the tray / `envmgr select`)

The hook makes an open shell switch to the machine-wide selected profile on its next prompt. Background and guarantees: `.claude/docs/tray.md`.

## Install

Run once per shell with the envmgr binary you will keep using (the block embeds its absolute path):

| Shell | Install | File written |
| --- | --- | --- |
| PowerShell | `envmgr hook --install powershell` | both `Documents\WindowsPowerShell\Microsoft.PowerShell_profile.ps1` (5.1) and `Documents\PowerShell\Microsoft.PowerShell_profile.ps1` (7+); the real Documents folder is used, also when redirected to OneDrive. Non-Windows: `~/.config/powershell/Microsoft.PowerShell_profile.ps1` |
| bash | `envmgr hook --install bash` | `~/.bashrc` |
| zsh | `envmgr hook --install zsh` | `$ZDOTDIR/.zshrc` or `~/.zshrc` |
| fish | `envmgr hook --install fish` | `$XDG_CONFIG_HOME/fish/config.fish` or `~/.config/fish/config.fish` |

- The file is created if missing; a marked block (`# >>> envmgr hook >>>` ... `# <<< envmgr hook <<<`) is appended. Running it again only refreshes the block (prints `already installed` / `updated`). Paths are printed. A symlinked profile file is updated through the link.
- `--dry-run` prints what would change and the block, without writing.
- Then open a new shell (or reload: `. $PROFILE`, `source ~/.bashrc`, `exec zsh`, `source ~/.config/fish/config.fish`).
- Install it **after** prompt frameworks (oh-my-posh, starship) in the file, so the hook wraps their prompt. `--install` appends at the end, which is right unless you add lines later.

Manual alternative (same effect, put it at the end of the profile):

```
PowerShell:  envmgr hook powershell | Out-String | Invoke-Expression
bash:        eval "$(envmgr hook bash)"
zsh:         eval "$(envmgr hook zsh)"
fish:        envmgr hook fish | source
```

cmd has no prompt hook: use `envmgr follow --shell cmd > "%TEMP%\envmgr.cmd" && "%TEMP%\envmgr.cmd"` when needed.

## Uninstall

`envmgr hook --uninstall <shell>` removes only the marked block (and the blank line added before it); the rest of the file is untouched. `not installed` means no block was found. Open a new shell afterwards (the current one keeps the hook until it exits).

## Use

```
envmgr select dev        # every following shell switches at its next prompt
envmgr select --none     # following shells deactivate
envmgr use prod          # this shell only: activate and pin (ignores the selection)
envmgr follow            # this shell: unpin and apply the selection now (eval it like use)
envmgr current --status  # dev + "(pinned)" or "(following tray)"
```

Try it with a throwaway home (PowerShell):

```powershell
$env:ENVMGR_HOME = Join-Path $env:TEMP 'envmgr-try'
envmgr set a URL http://a; envmgr set b URL http://b
envmgr hook powershell | Out-String | Invoke-Expression
envmgr select a          # press Enter: $env:URL is http://a
envmgr select b          # press Enter: http://b
envmgr tray              # tray icon for this home (Windows)
```

bash:

```bash
export ENVMGR_HOME="$(mktemp -d)"
envmgr set a URL http://a; envmgr set b URL http://b
eval "$(envmgr hook bash)"
envmgr select a          # next prompt: echo $URL -> http://a
```

## Troubleshooting

- **Nothing happens at the prompt**: check `envmgr current --status`; `(pinned)` means this shell ran `use` or the TUI: run `envmgr follow` (evaluated). Check the selection with `envmgr selected`.
- **Windows PowerShell 5.1 does not load the profile** ("running scripts is disabled"): the execution policy blocks profile scripts. Changing it is your decision, e.g. `Set-ExecutionPolicy -Scope CurrentUser RemoteSigned`; envmgr never changes it.
- **Different `ENVMGR_HOME` per shell**: the hook reads `$ENVMGR_HOME/selected.json` at each prompt, so set it before the prompt; the tray watches the home it was started with.
- **Binary moved or reinstalled elsewhere**: the block silently does nothing when the embedded path is gone; run `envmgr hook --install <shell>` again with the new binary.
- **A prompt framework replaced the prompt after the hook**: move the envmgr block to the end of the profile (`--uninstall` then `--install`).
- **Errors from sync** (for example a selected profile with a value cmd cannot hold) are printed once per selection change on stderr; the shell is left unchanged.
- **Git Bash on Windows**: use Windows paths with forward slashes for `ENVMGR_HOME` (`C:/Users/me/envmgr`), so both bash and envmgr.exe read the same file.
