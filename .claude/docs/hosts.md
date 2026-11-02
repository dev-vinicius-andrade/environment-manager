# Hosts file management

`envmgr hosts` edits the system hosts file of this machine; `envmgr parent hosts <agent> ...` does the same on a child, only when the child was invited with `--allow-hosts`. Steps: `.claude/instructions/edit-hosts.md`.

Code:
- `internal/hosts/`: the single implementation. Validation (`validate.go`, delegating the value checks to `internal/hostspec`, which profiles use too), parse/render of the managed block (`doc.go`), profile groups (`profilegroup.go`), file I/O with lock, backups, atomic write and permission errors (`file.go`), unified-style diff (`diff.go`). Both the local command and the remote child call it; nothing is parsed twice.
- `internal/hostsapply/`: applying a profile's hosts entries (`Apply`, `Unapply`), the machine-wide state `hosts-applied.json`, scope parsing and the sync hint.
- `commands/envmgr/hosts/` (`envmgr hosts`).
- `internal/remote/ops.go` (`hosts.*` operations, the `hosts` capability) and `commands/envmgr/parent/hosts.go` (`envmgr parent hosts`).

## Safety model

- envmgr only writes the **managed block**: the lines from `# BEGIN envmgr` to `# END envmgr`. It is created at the end of the file when missing, and stays (possibly empty) once created.
- Every byte outside the block is kept as is: other lines, comments, blank lines, a UTF-8 BOM, mixed line endings. The only exception: when the block is appended to a file whose last line has no line terminator, that line gets one.
- The block uses the file's line ending (the first one found: CRLF or LF; an empty file gets the OS default). The file keeps its trailing-newline state.
- envmgr refuses to edit a file it cannot handle safely, and changes nothing:
  - UTF-16 or NUL bytes;
  - more than one block, BEGIN without END, or END without BEGIN;
  - a line inside the block that envmgr did not write (unknown text, a stray comment, an entry whose commented state does not match its group, an invalid IP or name);
  - a file larger than 64 MiB.
- A missing file is an error; envmgr never creates the hosts file.
- A symlinked hosts file is resolved: the target is edited and the link is kept.

## Block format

```text
# BEGIN envmgr
# Managed by envmgr (envmgr hosts). Lines between BEGIN and END are rewritten by envmgr.
# group: default
10.0.0.1 api.local
# group: dev (disabled)
# 10.0.0.5 api.dev.local db.dev.local
# END envmgr
```

- Every entry belongs to a group. `--group` defaults to `default`. Group names follow the profile-name rules, except profile groups (below).
- A disabled group stays in the block; its entries are written as `# <ip> <names>`. Enabling it uncomments them.
- Entries without a group header (hand-written before the first header) are read as group `default`.
- Hostnames are stored lower-case, IPs in canonical form (`2001:DB8::1` becomes `2001:db8::1`).

## Commands

All take `--file <path>`. The path is chosen in this order: `--file`, `ENVMGR_HOSTS_FILE`, then the system default:
- Windows: `%SystemRoot%\System32\drivers\etc\hosts`;
- elsewhere: `/etc/hosts`.

| Command | Effect |
| --- | --- |
| `hosts list [--all]` | Managed entries as `GROUP STATE IP HOSTNAMES`. `--all` also prints the unmanaged lines with their line numbers, read-only. |
| `hosts add <ip> <hostname>... [--group g] [--force]` | Maps names to the IP in the group, creating the group (enabled). Idempotent. A name mapped to another IP in the same group moves. |
| `hosts remove <hostname> [--group g]` | Removes the name from that group, or from every group. Empty entries and groups disappear. |
| `hosts group enable\|disable <g>` | Switches a group on or off. |
| `hosts backup` | Writes a backup now and prints its path. |
| `hosts restore [<backup>] [--list]` | Replaces the whole file with a backup (default: the newest). The current file is backed up first, so a restore can be undone. `--list` lists the backups. |
| `hosts path` | Prints the resolved path (stdout) and where it came from (stderr). |

| `hosts apply <profile> [--home dir]` | Writes the profile's hosts entries as group `profile:<profile>`, replacing any applied profile group. Same as `use <profile> --only hosts`. |
| `hosts unapply [--home dir]` | Removes every profile group. Same as `deactivate --only hosts`. |
| `hosts applied [--home dir]` | Prints the applied profile (stdout), file and time (stderr); warns when the file disagrees (for example after a `restore`). |

- `--dry-run` (alias `--show-diff`) on `add`, `remove`, `group`, `restore`, `apply` and `unapply` prints a unified-style diff on stdout and writes nothing. It takes no lock and needs no elevation.
- A no-op (for example adding an existing mapping) writes nothing and makes no backup.
- `--home` reads the profiles (and writes `hosts-applied.json`) of another envmgr home: useful under `sudo` (whose environment and config dir differ) and in the tray's elevated window. A hidden `--pause` waits for Enter before exiting (the tray uses it so its elevated window stays readable).

## Profile hosts

A profile can hold hosts entries (`profiles.md`, "Hosts entries"). Applying them:

- **Group naming (collision safe)**: the entries go into one group named `profile:<profile>` (for example `# group: profile:dev`). `:` is not allowed in user group names (profile-name rules), so a profile group can never collide with a group made by `hosts add --group`. The parser accepts both kinds (`hosts.ValidGroup`); `hosts.CheckGroup`, used for every user edit (local and remote `hosts.add`, `remove --group`), refuses `profile:*`. `hosts remove <name>` without `--group` skips profile groups. `hosts group enable|disable profile:<p>` works (a temporary switch; applying again re-enables).
- **One at a time**: `Doc.ApplyProfile` replaces every `profile:*` group by the new one in one edit, at the place of the old group (or at the end). Applying B while A is applied therefore replaces A; re-applying the same profile is a no-op (no write, no backup). A profile without hosts entries only removes the previous group. User groups and every byte outside the block are kept (tested with LF and CRLF).
- **Validation at apply time**: the entries are re-validated by `internal/hosts` and critical names are always refused (no `--force`).
- **State**: `<ENVMGR_HOME>/hosts-applied.json` = `{"profile", "file", "appliedAt"}`, written atomically (0600) after a successful write, cleared by `unapply`. It is machine-wide (per envmgr home); the hosts file itself stays the truth (`hosts applied` warns on a mismatch).
- **Entry points**: `envmgr hosts apply|unapply`, `envmgr use|deactivate --hosts / --only hosts` (failure rule in `profiles.md`, "Scopes"), the TUI activate dialog, the tray's "Apply hosts (asks for administrator)..." (`tray.md`) and, remotely, `hosts.apply-profile` / `hosts.unapply` (below).
- Same safety model as every hosts write: backup first, lock, atomic replace, permission error with the elevation message, never self-elevating.

## Writes

- **Backup**: before every write, the current bytes are copied to `<hosts>.envmgr-bak-<UTC timestamp>` next to the file. The last 10 are kept; older ones are deleted. Restore accepts only one of these names.
- **Atomic replace**: temp file in the same directory, fsync, then rename over the file (Unix keeps the file mode; ownership becomes the writing user's, which is root under sudo).
- **Windows fallback**: if the rename fails (the file is open elsewhere, or replacing it is refused), envmgr rewrites the file in place and prints a warning. The backup was already written.
- **Lock**: `<hosts>.envmgr.lock`, created exclusively, stops two envmgr processes from writing at once. It is considered stale after 30 s. A waiting writer gives up after 10 s and names the lock file.
- **Permissions**: when the directory or file is not writable, the error says to run from an elevated shell (Windows: Run as administrator) or with sudo (Linux/macOS). envmgr never elevates itself.

## Validation (local and remote, same code)

- **IP**: `net/netip`, IPv4 or IPv6. Zone ids (`fe80::1%eth0`), leading zeros and names are rejected.
- **Hostname**: RFC 1123: labels of 1 to 63 of `[A-Za-z0-9-]`, no leading or trailing `-`, total of at most 253 characters, no trailing dot. This rejects spaces, `#`, `_`, CR/LF and every control character, so no input can start a new line or a comment.
- **Entry**: 1 to 32 names after de-duplication.
- **Group**: profile-name rules (`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`).
- **Critical names**: `localhost`, `localhost.localdomain`, `broadcasthost` and `ip6-*` are refused unless you pass `--force` (local only).

## Remote: the `hosts` grant

Editing hosts remotely lets a parent redirect any name on the child, which amounts to a DNS hijack. It is therefore a separate, explicit grant, off by default.

- `envmgr parent invite ... --allow-hosts` records the capability `hosts` on the invite.
  - The invite command prints a warning.
  - Enrollment copies the capability into `agents.json` (`caps`).
  - The child saves it in `agent/parent.json` (`caps`).
- To see it:
  - parent: `parent agents` and `parent invites` show a `CAPS` column (`hosts` or `-`);
  - child: `join` says the grant was given, `agent run` prints which hosts file the parent may edit, and `agent status` shows `caps:`.
- To change it: revoke and re-invite, as for the role. Revocation works as before.
- Operations (allow-listed in `ops.go`):
  - `hosts.list`;
  - `hosts.add` (`ip`, `hostnames`, `group`, `dryRun`);
  - `hosts.remove` (exactly one hostname, optional `group`);
  - `hosts.group` (`group`, `enable`);
  - `hosts.apply-profile` (`profile`, `dryRun`): applies the hosts entries of the **child's own** profile of that name (`hostsapply.Apply` on the child's store, hosts file and home). Only the name travels: a job carrying `ip`, `hostnames`, `group`, `hosts`, `vars`, ... is refused at the hub and at the child; unknown JSON fields are ignored by the child. Unknown profile: `notFound`.
  - `hosts.unapply` (`dryRun`): removes the applied profile group on the child.
  - Both are mutations: they need the grant **and** a `rw` child. Editing a child's profile hosts entries (`put` with `hosts`) is a profile edit and needs no grant.
- Enforcement, checked twice:
  - **Hub**: it validates the job, refuses `hosts.*` for an agent without the grant (403, "re-invite with --allow-hosts"), and refuses mutations for `ro` agents.
  - **Child**: it validates again, refuses without the grant in its own binding (`code: not_granted`), and refuses mutations when `ro`. Reads (`hosts.list`) are allowed for `ro` children that have the grant. A dry run counts as a mutation.
- **No path over the network**:
  - The protocol has no path field.
  - The admin API rejects unknown JSON fields (400).
  - The child ignores unknown fields and always uses its own hosts file: the system default, or its local `ENVMGR_HOSTS_FILE`.
- **No `--force` remotely**: critical names are always refused for a parent.
- **Child without write rights**: it answers with `code: permission_denied` and a message telling the user to run `envmgr agent run` elevated or with sudo on the child.
- **Audit**:
  - the child logs every hosts operation on stderr: parent label, op, profile, group, hostnames, dry run, ok, error;
  - the hub logs agent name, op, profile, group, hostnames, dry run and ok.

### Threat model additions

| Threat | Control |
| --- | --- |
| Parent (or stolen parent admin) redirects names on a child | Off by default; explicit `--allow-hosts` per invite; visible in `CAPS` and on the child; revocable; critical names never settable remotely; every change logged on both sides with a backup on the child. |
| Line or comment injection into the hosts file | IP parsed by `netip`, names restricted to RFC 1123 characters, group names to profile-name rules, at the CLI, at the hub and on the child. |
| Writing an arbitrary file on the child | No path in the protocol; the child resolves its own path locally. |
| Destroying user content | Only the managed block is rewritten; unknown content inside the block aborts the edit; backup before every write; restore. |
| Concurrent writers | Exclusive lock file next to the hosts file. |

## Privileges per OS

- **Windows**: the hosts file is writable only by Administrators. Run the terminal "as administrator". For remote edits, the child's `envmgr agent run` must run elevated. Antivirus tools sometimes lock or revert hosts edits.
- **Linux/macOS**: `/etc/hosts` is owned by root. Use `sudo envmgr hosts ...`. `sudo` usually resets the environment, so pass `--file` explicitly when testing with sudo, or use `sudo -E` / `sudo env ENVMGR_HOSTS_FILE=... envmgr ...`. For remote edits, the child's `agent run` must run as root, with the same `ENVMGR_HOME` (`sudo env ENVMGR_HOME=$HOME/.config/envmgr envmgr agent run`). macOS may need `sudo dscacheutil -flushcache; sudo killall -HUP mDNSResponder` to drop cached answers.
- Reading (`list`, `path`, every `--dry-run`) never needs elevation.

## Tests

- `internal/hosts/hosts_test.go` covers:
  - block creation;
  - byte-for-byte preservation with LF, CRLF, no final newline, BOM, and content after the block;
  - idempotent add, moves and merges;
  - remove; group enable/disable;
  - dry run writes nothing;
  - backup rotation (keeps 10), restore and undo;
  - the validation table (newline injection, `#`, bad IPs, zone ids, long names, critical names with and without force);
  - malformed blocks are refused;
  - lock and stale lock;
  - the permission-denied message (skipped when the OS ignores read-only);
  - the default path per GOOS; a missing file.
- `commands/envmgr/hosts/hosts_test.go`: the CLI end to end on a temp file.
- `internal/remote/hosts_remote_test.go` covers:
  - no grant means refused, at the hub and at the child;
  - full flow with the grant, including dry run, list, group, remove, validation and critical names;
  - read-only plus grant;
  - a path from the network is ignored or rejected;
  - revoke;
  - the structured permission error.
- `commands/envmgr/parent/hosts_test.go`: `parent invite --allow-hosts`, the `CAPS` columns and `parent hosts` end to end, including push of a profile with hosts, `parent show` with both sections, `apply-profile` (dry run, refused without the grant, unknown profile) and `unapply`.
- Profile hosts: `internal/hosts/profilegroup_test.go` (parse, `CheckGroup` refusal, remove skips profile groups, replace in place, validation), `internal/hostsapply/hostsapply_test.go`, `commands/envmgr/hosts/hosts_test.go` (`apply`, `unapply`, `applied`, mismatch warning, `--home`, `--pause`), `internal/remote/profile_hosts_remote_test.go` (hosts survive put/get, `apply-profile` refused without the grant / for `ro` / unknown profile / with network hosts data, critical names, audit line, unapply).
- Every test package that could resolve the default path sets `ENVMGR_HOSTS_FILE` to a missing temp file in `TestMain`, so no test can reach the real hosts file.

## Limits

- No dedicated TUI tab for the raw block (the profiles TUI edits and applies profile hosts).
- One applied profile per machine (by design: two profiles are never applied at once). Entry comments are not written to the hosts file.
- No fan-out across children.
- Ownership of the hosts file is not restored after the atomic replace (it is root's under sudo, which matches the default).
- The diff is a single hunk around the changed region.
