# Profiles with environment and hosts entries

A profile can hold environment variables, hosts entries, or both. You choose what to apply: the environment (per shell, default), the hosts entries (machine-wide, written by envmgr into its managed block of the hosts file), or both. Background: `.claude/docs/profiles.md` ("Hosts entries", "Scopes") and `.claude/docs/hosts.md` ("Profile hosts").

## 1. Define the profile

```sh
envmgr set dev MY_API_URL http://api.dev                      # variables (as before)
envmgr profile hosts add dev 10.0.0.5 api.dev.local db.dev.local --comment "dev cluster"
envmgr profile hosts add dev 10.0.0.6 web.dev.local
envmgr profile hosts remove dev db.dev.local                   # one name
envmgr profile hosts remove dev 10.0.0.6                       # a whole entry
envmgr profile show dev                                        # both sections (values masked)
envmgr profile show dev --section hosts
```

- A hosts-only profile is fine: `envmgr profile hosts add net 10.1.0.1 nas.lan` creates it.
- Same validation as `envmgr hosts add` (IPv4/IPv6, RFC 1123 names, 1-32 names per entry). `localhost`, `broadcasthost` and `ip6-*` are refused. The comment stays in the profile; it is not written to the hosts file.
- Editing a profile never touches the hosts file. If the profile is applied, apply it again to update the file.
- In the TUI: select the profile, `l` (details pane), `tab` (Hosts section), `e` add/edit, `x` delete.

## 2. Apply what you want

| Want | bash/zsh | PowerShell |
| --- | --- | --- |
| environment only (default) | `eval "$(envmgr use dev)"` | `envmgr use dev --shell powershell \| Invoke-Expression` |
| environment and hosts | `eval "$(envmgr use dev --hosts)"` | `envmgr use dev --shell powershell --hosts \| Invoke-Expression` |
| hosts only | `envmgr hosts apply dev` (= `envmgr use dev --only hosts`) | same |
| undo hosts | `envmgr hosts unapply` (= `deactivate --only hosts`) | same |
| undo both | `eval "$(envmgr deactivate --hosts)"` | `envmgr deactivate --shell powershell --hosts \| Invoke-Expression` |
| which profile's hosts are applied | `envmgr hosts applied` | same |

- Preview: add `--dry-run` (diff on stderr for `use`/`deactivate`, on stdout for `hosts apply`/`unapply`; nothing written, no shell code).
- One profile's hosts are applied at a time: applying `test` while `dev` is applied replaces `dev`'s group (`# group: profile:dev`) by `test`'s.
- If the hosts part fails (usually: not elevated), `use --hosts` prints nothing on stdout and exits 1, so your shell is not half switched. Add `--best-effort` to activate the environment anyway.
- `envmgr run` stays environment-only.
- TUI: `enter` on a profile with hosts entries asks `e` environment, `h` hosts, `b` both (the TUI must run elevated for the hosts part; a failure is shown in the status line).
- Tray (Windows): each profile's submenu has `Environment` and `Hosts` (the selection's scopes; `Hosts` only records the choice) and `Apply hosts (asks for administrator)...`, which opens an elevated window after the UAC prompt. CLI equivalent of the scopes: `envmgr select dev --hosts` / `--only hosts`. Shells following the selection only pick up the environment; `envmgr sync` prints a one-time hint when the selected hosts are not applied.
- On a child (needs `invite --allow-hosts` and a read-write child): `envmgr parent push laptop dev`, then `envmgr parent hosts laptop apply-profile dev [--dry-run]` / `unapply`. The child uses its own copy of the profile.

## 3. Elevation

Writing the hosts file needs administrator rights; envmgr never elevates itself.

- **Windows**: run the terminal "as administrator", then `envmgr hosts apply dev` or `envmgr use dev --shell powershell --hosts | Invoke-Expression` in that terminal (the environment part applies to that elevated shell only). Or use the tray item, which asks UAC. An elevated prompt usually has the same `%APPDATA%`, so the default envmgr home is the same; pass `--home <dir>` to `hosts apply|unapply|applied` if yours differs.
- **Linux/macOS**: `sudo` changes the environment and the config dir, so point envmgr at your home explicitly:

  ```sh
  sudo envmgr hosts apply dev --home "$HOME/.config/envmgr"
  sudo envmgr hosts unapply --home "$HOME/.config/envmgr"
  ```

  For both parts in your own shell: apply hosts with sudo as above, then `eval "$(envmgr use dev)"` (environment only). Limitation: `hosts-applied.json` is then owned by root (mode 0600), so `envmgr hosts applied` as your user fails with permission denied; run it with the same `sudo ... --home` (envmgr does not chown it back).

## 4. Try it safely (temp files, no elevation)

bash:

```sh
tmp=$(mktemp -d); printf '127.0.0.1 localhost\n' > "$tmp/hosts"
export ENVMGR_HOME="$tmp/home" ENVMGR_HOSTS_FILE="$tmp/hosts"
envmgr set dev MY_API_URL http://api.dev
envmgr profile hosts add dev 10.0.0.5 api.dev.local
eval "$(envmgr use dev --hosts)"; echo "$MY_API_URL"; cat "$tmp/hosts"
envmgr hosts applied
eval "$(envmgr deactivate --hosts)"; cat "$tmp/hosts"
unset ENVMGR_HOME ENVMGR_HOSTS_FILE
```

PowerShell:

```powershell
$tmp = Join-Path $env:TEMP "envmgr-profile-hosts"; New-Item -ItemType Directory -Force $tmp | Out-Null
Set-Content "$tmp\hosts" "127.0.0.1 localhost"
$env:ENVMGR_HOME = "$tmp\home"; $env:ENVMGR_HOSTS_FILE = "$tmp\hosts"
envmgr set dev MY_API_URL http://api.dev
envmgr profile hosts add dev 10.0.0.5 api.dev.local
envmgr use dev --shell powershell --hosts | Invoke-Expression; $env:MY_API_URL; Get-Content "$tmp\hosts"
envmgr hosts applied
envmgr deactivate --shell powershell --hosts | Invoke-Expression; Get-Content "$tmp\hosts"
Remove-Item Env:ENVMGR_HOME, Env:ENVMGR_HOSTS_FILE
```

Check `envmgr hosts path` first: it must print the temp file. Instead of the env var, `--file <path>` works on `use`, `deactivate` and every `hosts` subcommand.
