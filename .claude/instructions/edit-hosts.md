# Edit the hosts file (locally and from a parent)

envmgr only edits its own block (`# BEGIN envmgr` ... `# END envmgr`) and backs up the file before every write. For background, see `.claude/docs/hosts.md`. To keep hosts entries in a profile and apply them with it (`use --hosts`, `hosts apply`), see `profiles-with-hosts.md`.

## 1. Try it safely first (temp file, no elevation)

Use a copy of the hosts file, never the real one, while trying or developing.

bash:

```sh
tmp=$(mktemp -d); cp /etc/hosts "$tmp/hosts"
export ENVMGR_HOSTS_FILE="$tmp/hosts"
envmgr hosts path                                   # must print the temp file
envmgr hosts add 10.0.0.5 api.dev.local db.dev.local --group dev --dry-run
envmgr hosts add 10.0.0.5 api.dev.local db.dev.local --group dev
envmgr hosts list --all
envmgr hosts group disable dev
envmgr hosts remove db.dev.local
envmgr hosts restore --list
envmgr hosts restore                                # newest backup
```

PowerShell:

```powershell
$tmp = Join-Path $env:TEMP "envmgr-hosts-try"; New-Item -ItemType Directory -Force $tmp | Out-Null
Copy-Item "$env:SystemRoot\System32\drivers\etc\hosts" "$tmp\hosts"
$env:ENVMGR_HOSTS_FILE = "$tmp\hosts"
envmgr hosts path
envmgr hosts add 10.0.0.5 api.dev.local --group dev --dry-run
envmgr hosts add 10.0.0.5 api.dev.local --group dev
envmgr hosts list --all
Remove-Item Env:ENVMGR_HOSTS_FILE                   # back to the real file afterwards
```

Instead of the env var, `--file <path>` works on every `hosts` subcommand.

## 2. Edit the real hosts file

1. Check the target: `envmgr hosts path` (with `ENVMGR_HOSTS_FILE` unset).
2. Preview: add `--dry-run` to the command.
3. Run the command elevated:
   - **Windows**: open PowerShell or Windows Terminal with "Run as administrator", then `envmgr hosts add 10.0.0.5 api.dev.local --group dev`.
   - **Linux/macOS**: `sudo envmgr hosts add 10.0.0.5 api.dev.local --group dev`. `sudo` drops most environment variables; that is what you want here. To test under sudo, pass `--file` explicitly.
4. Without elevation, the command fails with "permission denied ... run from an elevated shell / with sudo" and changes nothing. envmgr never elevates itself.

Switch a set of entries:

```sh
envmgr hosts group disable dev     # entries stay, commented out
envmgr hosts group enable dev
```

Undo: `envmgr hosts restore` (newest backup) or `envmgr hosts restore <name>` from `envmgr hosts restore --list`. Restore backs up the current file first.

Some browsers and OSes cache DNS. After a change, you may need to flush:
- Windows: `ipconfig /flushdns`;
- macOS: `sudo dscacheutil -flushcache; sudo killall -HUP mDNSResponder`.

## 3. Edit a child's hosts file from the parent

Hosts editing is a separate grant; a normal invite does not allow it.

1. On the parent, invite with the grant (read-write is needed for changes):

   ```sh
   envmgr parent invite --name laptop --allow-hosts
   ```

   An existing child cannot be upgraded:
   1. `envmgr parent revoke laptop`;
   2. invite again with `--allow-hosts`;
   3. on the child, `envmgr join <code> --force`.
2. On the child, run the agent with rights to write its hosts file:
   - **Windows**: an elevated terminal, `envmgr join <code>`, later `envmgr agent run`.
   - **Linux/macOS**: `sudo env ENVMGR_HOME="$HOME/.config/envmgr" envmgr join <code>`, later `sudo env ENVMGR_HOME="$HOME/.config/envmgr" envmgr agent run`. Keep the same `ENVMGR_HOME`, so root uses this user's binding.
   - `join` and `agent run` print that the parent may edit the hosts file and which file. `envmgr agent status` shows `caps: hosts`.
3. On the parent:

   ```sh
   envmgr parent agents                                         # CAPS column: hosts
   envmgr parent hosts laptop list
   envmgr parent hosts laptop add 10.0.0.5 api.dev.local --group dev --dry-run
   envmgr parent hosts laptop add 10.0.0.5 api.dev.local --group dev
   envmgr parent hosts laptop group disable dev
   envmgr parent hosts laptop remove api.dev.local
   ```

- The child always edits its own hosts file: the system file, or its local `ENVMGR_HOSTS_FILE`. A parent cannot choose a path.
- Critical names (`localhost`, `broadcasthost`, `ip6-*`) are always refused from a parent.
- Error messages:
  - "not granted ... --allow-hosts": the child was invited without the grant;
  - "read-only": the child is `ro` (only `list` works);
  - "permission denied ... run `envmgr agent run` ... elevated": the child process cannot write its hosts file.
- Each change is logged on the child's stderr (parent label, op, hostnames) and in the hub log. The child keeps its own backups next to its hosts file. Restoring them is local only: `envmgr hosts restore` on the child.

## Remote trial on one machine (temp files only)

bash, with three terminals or background jobs:

```sh
tmp=$(mktemp -d); printf '127.0.0.1 localhost\n' > "$tmp/child-hosts"
ENVMGR_HOME=$tmp/parent envmgr parent serve --addr 127.0.0.1:7799 &
ENVMGR_HOME=$tmp/parent envmgr parent invite --name kid --allow-hosts   # copy the code
ENVMGR_HOME=$tmp/child ENVMGR_HOSTS_FILE=$tmp/child-hosts envmgr join <code> &
ENVMGR_HOME=$tmp/parent envmgr parent hosts kid add 10.0.0.5 api.dev.local --group dev
cat "$tmp/child-hosts"
```

PowerShell:

```powershell
$tmp = Join-Path $env:TEMP "envmgr-remote-hosts"; New-Item -ItemType Directory -Force $tmp | Out-Null
Set-Content "$tmp\child-hosts" "127.0.0.1 localhost"
# terminal 1
$env:ENVMGR_HOME = "$tmp\parent"; envmgr parent serve --addr 127.0.0.1:7799
# terminal 2
$env:ENVMGR_HOME = "$tmp\parent"; envmgr parent invite --name kid --allow-hosts
# terminal 3
$env:ENVMGR_HOME = "$tmp\child"; $env:ENVMGR_HOSTS_FILE = "$tmp\child-hosts"; envmgr join <code>
# terminal 2
envmgr parent hosts kid add 10.0.0.5 api.dev.local --group dev; Get-Content "$tmp\child-hosts"
```
