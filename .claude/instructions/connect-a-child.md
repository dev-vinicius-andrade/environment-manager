# Connect a child machine to a parent

The **parent** controls the profiles of its **children**. The child dials the parent, so only the parent needs a reachable port. For background, see `.claude/docs/remote.md`.

The example uses:
- **parent**: LAN IP `192.168.1.10`;
- **child**: a laptop that can reach `192.168.1.10:7777`.

## 1. On the parent: run the hub

```sh
envmgr parent serve --addr 192.168.1.10:7777
```

- It prints the certificate fingerprint and a warning, because the address is not loopback.
- Keep it running (foreground); Ctrl+C stops it.
- Allow inbound TCP 7777 from the children only (firewall).
- For a child on the same machine, the default `envmgr parent serve` (127.0.0.1:7777) is enough.
- If the hub listens on `0.0.0.0`, or children reach it through NAT/DNS, add `--addr-public <host:port>` to every `invite` below.

## 2. On the parent: create a join code

In a second terminal:

```sh
envmgr parent invite --name laptop                 # read-write, valid 15 minutes
envmgr parent invite --name kiosk --read-only --expires 1h
envmgr parent invite --name laptop --addr-public parent.example.lan:7777
```

- stdout is one line: `envmgr join <code>`.
- Send it to the child over a channel you trust. Treat it like a password until it is used or expires.
- It works **once**.

## 3. On the child: join

```sh
envmgr join <code>                 # enroll, then stay connected (Ctrl+C stops)
envmgr join <code> --no-run        # only enroll
envmgr agent run                   # (re)connect later using the saved binding
envmgr agent status                # parent, role, fingerprint, still accepted?
```

- If the parent did not fix `--name`, the child may choose one: `envmgr join <code> --name laptop`.
- A child that already has a parent refuses to join; add `--force` to switch parents.
- A fingerprint mismatch means stop: it is not the parent that issued the code, or the parent's identity changed.

## 4. On the parent: use it

```sh
envmgr parent agents                         # NAME ROLE ENROLLED LAST SEEN ONLINE
envmgr parent profiles laptop
envmgr parent show laptop dev                # values masked
envmgr parent show laptop dev --reveal
envmgr parent push laptop dev                # local "dev" -> laptop (overwrites)
envmgr parent pull laptop dev [--force]      # laptop "dev" -> local store
envmgr parent set laptop dev MY_API_URL https://api.dev.example
envmgr parent unset laptop dev MY_API_URL
envmgr parent delete laptop dev
```

- Commands for a child that is not connected fail right away with "agent is offline".
- Commands for a read-only child fail on every mutation.

## Restrict, revoke, rotate

- **Read-only**: invites cannot be changed after the fact. To change a child's role:
  1. `envmgr parent revoke <agent>`;
  2. issue a new invite with or without `--read-only`;
  3. the child runs `envmgr join <new code> --force`.
- **Revoke**: `envmgr parent revoke laptop`.
  - It takes effect within about 2 s while the hub runs, or at the next start.
  - The child's loop stops with "the parent revoked this machine's access".
- **Rotate a child's credential**: revoke, re-invite with the same `--name`, and the child runs `envmgr join <code> --force`.
- **Pending invites**: `envmgr parent invites` to list them, `envmgr parent cancel <invite-name>` to withdraw one.
- **Child leaves on its own**: `envmgr agent leave`. It deletes `agent/parent.json` and tells the parent best-effort.
- **Rotate the parent identity**, for example if the key leaked:
  1. stop the hub;
  2. delete `<ENVMGR_HOME>/parent/server.crt` and `server.key`;
  3. start the hub, which prints a new fingerprint;
  4. re-invite every child; each joins with `--force`.

## Two homes on one machine (testing)

Set a different `ENVMGR_HOME` per role, for example in bash:

```sh
ENVMGR_HOME=.demo/parent envmgr parent serve &
ENVMGR_HOME=.demo/parent envmgr parent invite --name kid      # copy the code
ENVMGR_HOME=.demo/child  envmgr join <code>
```
