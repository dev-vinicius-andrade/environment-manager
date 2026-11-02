# Remote control: parent (hub) and children

Goal 2 in `goals.md`, as decided by the owner: one machine, the **parent**, controls the **profiles** of other machines, the **children**.

- **The child dials the parent** (hub model). Children open no port, so this works behind NAT. Only the parent listens.
- **A child joins with a single-use, expiring join code** that the parent prints.

Scope is deliberately narrow. The only operations are: list, get, put and delete a profile, and set or unset one variable. There is **no remote command execution, no shell and no arbitrary paths**.

Code:
- `internal/remote/`: identity, hub state, hub, admin API, child agent, operations.
- `commands/envmgr/parent/` (`envmgr parent ...`).
- `commands/envmgr/join/` (`envmgr join`).
- `commands/envmgr/agent/` (`envmgr agent ...`).

Standard library only. This model replaced the earlier `serve`/`remote add` shared-token design completely; there is no legacy path.

## Who can control whom

- A child is bound to **exactly one parent**: the one whose code it used. `join` refuses a second parent unless you pass `--force`, which replaces the binding and tells the old parent best-effort.
- A child takes instructions only from responses on its **own outbound, authenticated TLS connection** to the **pinned** parent certificate. Nothing else can reach it.
- On the parent, only the **local admin** can drive children:
  - every `envmgr parent ...` command reads the state files, or talks to the hub's loopback admin API with a per-start admin token;
  - nothing on the network can create invites, list agents or relay operations.
- Each invite grants a role, and the stricter rule always wins:
  - `rw`: read and write.
  - `ro` (`invite --read-only`): read only. The hub refuses mutations for `ro` children, and the child refuses them again on its side.

## Lifecycle

1. **Parent** runs `envmgr parent serve` (foreground).
   - First run creates a self-signed ECDSA P-256 certificate in `<ENVMGR_HOME>/parent/`.
   - Every start prints the SHA-256 fingerprint.
   - It binds `127.0.0.1:7777` by default. Any other `--addr` prints a warning.
2. **Parent** runs `envmgr parent invite [--name laptop] [--expires 15m] [--read-only] [--addr-public host:port]`.
   - It creates a 32-byte random secret and stores only its SHA-256, with expiry, name and role.
   - It prints `envmgr join <code>`.
   - The code is `base64url(JSON{addr, fp, secret, name?, exp})`.
   - `addr` defaults to the running hub's listen address. Pass `--addr-public` when children must use another address, such as a LAN IP, a DNS name or a port forward.
3. **Child** runs `envmgr join <code> [--name n]`.
   - It pins `fp` and refuses any other certificate.
   - It POSTs `/v1/enroll {secret, name}`.
   - The hub consumes the invite atomically under a lock, so it is single use. Expired, used, cancelled or unknown codes all get a bare 401.
   - The hub returns a long-lived per-child credential (32 random bytes; the hub stores only its hash).
   - The child saves `<ENVMGR_HOME>/agent/parent.json` (0600) and starts the loop. Use `--no-run` to only enroll.
   - If the parent fixed `--name`, the child cannot change it. Otherwise the child's `--name` is used, or a generated `agent-xxxxxx`.
4. **Loop** (`envmgr agent run`, or the tail of `join`):
   - It pings, then long-polls `GET /v1/agent/poll` (held about 25 s).
   - It runs only allow-listed operations, validating every name, and POSTs `/v1/agent/result/{id}`.
   - Network errors reconnect with capped exponential backoff (1 s up to 60 s).
   - A 401 means revoked: the loop stops and tells the user.
   - Ctrl+C stops cleanly.
5. **Parent operations** go through the hub, which relays them to the child and waits about 15 s for its answer:

   ```text
   parent profiles|show|push|pull|set|unset|delete <agent> ...
   ```

   - An offline child fails at once ("agent is offline").
6. **Revoke** with `envmgr parent revoke <agent>`.
   - It removes the child from `agents.json`.
   - A running hub notices within 2 s, even during an open long-poll, and the child's next request gets 401.
   - The child side can also run `envmgr agent leave`, which deletes the local binding and tells the parent.
7. **Re-invite / rotate**: revoke, then issue a new invite. The child joins again with `--force` if it still has the old binding.

`envmgr parent invites` lists pending invites (name, role, created, expires; never secrets). `envmgr parent cancel <invite-name>` withdraws one. `envmgr parent agents` shows name, role, enrolled-at, last-seen and online; when the hub is down everything shows offline. `envmgr agent status` shows the parent, role, fingerprint and whether the parent still accepts this child.

Exact commands for two machines are in `.claude/instructions/connect-a-child.md`.

## Files

| Machine | File | Content | Notes |
| --- | --- | --- | --- |
| parent | `parent/server.crt`, `server.key` | hub TLS identity | key 0600. Deleting both changes the fingerprint and every child must re-join. |
| parent | `parent/invites.json` | pending invites: name, role, secret **hash**, expiry | 0600 |
| parent | `parent/agents.json` | children: name, role, credential **hash**, enrolled-at | 0600 |
| parent | `parent/agents-seen.json` | last-seen per child | 0600, written only by the hub |
| parent | `parent/admin.json` | loopback admin address, **admin token**, fingerprint, public address, pid | 0600. New token on every start, removed on clean shutdown. |
| parent | `parent/state.lock` | cross-process lock for state changes | transient |
| child | `agent/parent.json` | parent address, pinned fingerprint, **this child's credential**, name, role | 0600 |

All writes are atomic (temp file plus rename). Both the hub and the CLI re-read the state on every use, so `invite`, `cancel` and `revoke` take effect on a running hub without a restart. Raw invite secrets and child credentials are never stored on the parent; a test reads every parent file to check this. The admin token in `admin.json` and the child's own credential in `parent.json` are the only stored bearer secrets, by necessity. On Windows the 0600 mode is not enforced, so protection relies on the user-profile ACLs.

## API

Public listener (TLS 1.3, `--addr`):

| Method | Path | Auth | Purpose |
| --- | --- | --- | --- |
| POST | `/v1/enroll` | invite secret in body | `{secret, name?}` returns `{name, role, credential, parent}`. 401 bare / 409 name in use / 429 lockout. |
| GET | `/v1/agent/poll` | Bearer credential | 200 `{id, op, args}`, or 204 after the hold time. 401 once revoked. |
| POST | `/v1/agent/result/{id}` | Bearer credential | `{ok, error?, notFound?, profiles?, profile?}`. 404 if the job is not this child's. |
| GET | `/v1/agent/ping` | Bearer credential | `{name, role, parent}` |
| POST | `/v1/agent/leave` | Bearer credential | removes this child |

Operations (`op`): `list`, `get`, `put`, `delete`, `set`, `unset`. `args` holds `profile`, `var`, `value` and `vars`.

Admin listener (TLS on `127.0.0.1:<random>`; the peer must be loopback; Bearer admin token from `admin.json`):
- `GET /admin/v1/agents`
- `POST /admin/v1/relay {agent, job}`. Errors: 404 unknown agent, 409 offline, 403 read-only, 504 child timeout.

The admin API is not served on the public listener.

## Threat model and controls

| Threat | Control |
| --- | --- |
| Eavesdropping | TLS 1.3 on both listeners; no plain HTTP. |
| Rogue or MITM parent | The child pins the SHA-256 of the parent's leaf certificate from the join code (`InsecureSkipVerify` plus a constant-time `VerifyPeerCertificate`). It never uses system CAs, ignores proxies and does not follow redirects. A mismatch stops the loop for good. |
| Stolen or guessed join code | 256-bit secret, single use (consumed under a lock), expires (default 15 min), cancellable. Expired, used and unknown codes are indistinguishable (401, no detail). Treat a code like a password until it is used. |
| Enrollment or credential brute force | Per-IP lockout after 5 consecutive failures: 1 s, doubling up to 5 min (429), also against valid secrets during the lockout. The table is bounded. |
| Stolen child credential | Revoke on the parent; it takes effect within 2 s on a running hub. Credentials are per child, so one leak does not affect the others. |
| One child acting on another | Jobs are bound to the agent name. A child can only fetch its own queue and answer its own job ids (404 otherwise). |
| Network clients driving children | No network endpoint creates invites or relays operations. The admin API listens on loopback only, checks the peer address and needs a per-start random token. |
| Path traversal / injection | `profile.ValidName` / `ValidVar` checked at the CLI, at the hub before relaying, and again on the child before executing. Unknown operations are rejected. |
| Remote code execution | The child maps each allowed operation 1:1 to `profile.Store`. There is no exec, shell or file path in the protocol. |
| Resource exhaustion | 1 MiB request body limit, 64 KiB headers, server timeouts (ReadHeader 10 s, Read 30 s, Write 45 s, Idle 90 s), client response limit 8 MiB, per-child job queue of 8. |
| Secret leakage | Logs carry agent name, operation and profile name, never values, secrets or tokens. Failed auth logs only IP and path. `parent show` masks values (`********`, length hidden) unless `--reveal`. Listings never print secrets. |

## Limits

- `parent serve`, `agent run` and `join` run in the foreground; there is no service or daemon installation yet.
- One parent per child. A parent has many children, but there is no fan-out command; run one command per agent.
- Last-seen is recorded at most once a minute per child. "Online" means a poll is open, or one ended less than 10 s ago.
- A parent whose identity is regenerated, or whose address changes, needs every child to re-join.
- `parent set` takes the value as an argument, so it lands in shell history. Prefer `parent push` of a local profile for secrets.
- The lock file is treated as stale after 30 s; a crashed CLI can block state changes until then.

## Tests

The test command:

```sh
go test ./internal/remote/ ./commands/envmgr/parent/ ./commands/envmgr/join/
```

The tests use a real TLS hub on `127.0.0.1:0` with separate temporary homes for the parent and two children. They cover:

- full relay of every operation, and two children isolated from each other;
- read-only enforced at the hub and at the child;
- single-use, expired and cancelled invites;
- wrong fingerprint refused, both at join and in the loop;
- the one-parent binding and `--force`;
- revoke stopping the loop, including during an open long-poll;
- fast offline errors;
- the admin API's token, loopback and not-public checks;
- no raw secrets in any parent file, plus file modes;
- traversal names, body limit, rate limit and lockout;
- masked output;
- the CLI end to end: `parent` invite/invites/cancel/agents/push/pull/show/set/unset/delete/revoke, and `join`, `agent status` and `agent leave`.
