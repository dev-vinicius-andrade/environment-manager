# Remote control (v1): `envmgr serve` and `envmgr remote`

First version of goal 2 in `goals.md`: one machine (A, the controller) manages the **profiles** of another machine (B, the agent). Scope is deliberately narrow: list/get/put/delete profiles and set/unset single variables. There is **no remote command execution and no shell access**, and nothing in the API can activate a profile in a shell on B.

Code: `internal/remote/` (server, client, identity, remotes file), `commands/envmgr/serve/`, `commands/envmgr/remote/`. Standard library only.

## Files

All under `<ENVMGR_HOME>/remote/` (`ENVMGR_HOME` defaults to `<user config dir>/envmgr`, see `internal/profile`):

| File | Machine | Content | Mode |
| --- | --- | --- | --- |
| `server.crt` | B | self-signed ECDSA P-256 cert, 10 years, SAN localhost/127.0.0.1/::1/hostname | 0600 |
| `server.key` | B | PKCS#8 private key | 0600 |
| `token` | B | 32 random bytes (crypto/rand), hex, plain text so `--show-token` can re-print it | 0600 |
| `remotes.json` | A | paired remotes: address, token, pinned fingerprint | 0600 |

Files are created on first use and reused afterwards. If only one of cert/key exists, `serve` refuses to start rather than regenerating silently (regeneration changes the fingerprint and breaks every pairing). On Windows the 0600 mode is not enforced; protection relies on the ACLs of the user profile directory.

Rotation: stop `serve`, delete `token` (new token) and/or both `server.crt` + `server.key` (new fingerprint), start `serve` again, re-pair with `remote add <name> ... --force`.

## Pairing flow (two machines)

On machine B (agent), for example `10.0.0.5`:

```sh
envmgr serve                              # loopback only: 127.0.0.1:7777
envmgr serve --addr 10.0.0.5:7777         # reachable from the LAN (prints a warning)
envmgr serve --addr 10.0.0.5:7777 --read-only   # A can read but every mutation gets 403
```

The first run prints the certificate fingerprint and the token once, plus a ready-made pairing line. Later runs print only the fingerprint; re-print both with:

```sh
envmgr serve --show-token
```

Move token and fingerprint to A over a channel you trust (in person, password manager, existing SSH session). On machine A (controller):

```sh
envmgr remote add b 10.0.0.5:7777 --token <TOKEN> --fingerprint <FINGERPRINT>
# or keep the token out of shell history:
envmgr remote add b 10.0.0.5:7777 --token-stdin --fingerprint <FINGERPRINT> < token.txt

envmgr remote list                        # name, address, pinned fingerprint (never the token)
envmgr remote profiles b                  # profile names on B
envmgr remote show b dev                  # KEY=******** (values masked)
envmgr remote show b dev --reveal         # clear-text values
envmgr remote push b dev                  # local dev -> B, overwrites B's dev
envmgr remote pull b dev [--force]        # B's dev -> local store; refuses to overwrite without --force
envmgr remote set b dev MY_API_URL https://api.dev.example
envmgr remote unset b dev MY_API_URL
envmgr remote delete b dev
envmgr remote remove b                    # forget the pairing (deletes the saved token)
```

To reach a loopback-only agent from another machine without exposing a port, tunnel it: `ssh -L 7777:127.0.0.1:7777 userB@10.0.0.5`, then `envmgr remote add b 127.0.0.1:7777 ...` on A.

Data goes to stdout and status messages to stderr. `set` takes the value as an argument, so it lands in shell history; prefer `push` of a local profile for secrets.

## API (JSON over HTTPS, TLS 1.3)

Every request needs `Authorization: Bearer <token>`.

| Method | Path | Body | Success |
| --- | --- | --- | --- |
| GET | `/v1/profiles` | | 200 `{"profiles":["dev",...]}` |
| GET | `/v1/profiles/{name}` | | 200 `{"name":"dev","vars":{...}}` |
| PUT | `/v1/profiles/{name}` | `{"vars":{...}}` (`name` optional, must match path) | 204, overwrites |
| DELETE | `/v1/profiles/{name}` | | 204 |
| PUT | `/v1/profiles/{name}/vars/{var}` | `{"value":"..."}` | 204, creates profile if missing |
| DELETE | `/v1/profiles/{name}/vars/{var}` | | 204 |

Errors are `{"error":"..."}`: 400 invalid name/variable/JSON (unknown fields rejected), 401 `{"error":"unauthorized"}` with no detail, 403 read-only server, 404 profile not found or unknown path, 413 body over 1 MiB, 429 auth lockout (with `Retry-After`), 500 `internal error` (detail only in the server log).

## Threat model and controls

Assets: profile values (API keys, tokens), the bearer token, the server private key.

| Threat | Control |
| --- | --- |
| Network eavesdropping | TLS 1.3 always on, no plain-HTTP listener. |
| Man in the middle / rogue server | Client pins the SHA-256 of the server leaf certificate (`InsecureSkipVerify` + `VerifyPeerCertificate` constant-time compare). No fingerprint, no connection; system CAs are never trusted. Mismatch error: possible MITM or regenerated identity. Client ignores proxies and does not follow redirects. |
| Unauthorized client | 256-bit random bearer token; server keeps only its SHA-256 and compares with `subtle.ConstantTimeCompare`. Auth runs before routing, read-only checks and body reads, so unknown paths also return 401. |
| Token brute force | Per source IP: after 5 consecutive failures the IP is locked out 1s, doubling per further failure up to 5 min (429, applies even to the right token during lockout); success resets. Table bounded at 10k entries. |
| Path traversal / injection | Profile names checked with `profile.ValidName`, variables with `profile.ValidVar`, on client and server (path values are unescaped before checking, so `..%2F` is rejected). |
| Resource exhaustion | 1 MiB request body limit, 64 KiB headers, ReadHeader 10s / Read 30s / Write 30s / Idle 60s timeouts, client response limit 8 MiB. |
| Accidental exposure | Loopback bind by default; any other address must be passed explicitly and prints a warning. `--read-only` rejects every non-GET with 403. |
| Remote code execution | No endpoint executes anything; operations map 1:1 to `profile.Store` calls. |
| Secret leakage in logs/output | Server logs method, path, profile/variable names, source IP; never values or the token. `remote show` masks values (fixed `********`, length not revealed) unless `--reveal`; `remote list` never prints tokens. |
| Concurrent writes | Server serializes store mutations with a mutex. |

Residual risks / limits:

- Anyone with the token and network reach controls B's profiles; anyone reading A's `remotes.json` or B's `token` file gets that power. Protect both home directories.
- No per-client identities or revocation list: one token per agent; rotate by deleting the `token` file.
- No audit log beyond stderr lines; no TLS client certificates (mTLS); no NAT traversal (same network or an SSH tunnel).
- Lockout is per IP, so many source IPs can still try tokens in parallel (impractical against a 256-bit token, but it can fill logs).
- `serve` runs in the foreground; no service/daemon install yet.

## Tests

`go test ./internal/remote/ ./commands/envmgr/remote/ ./commands/envmgr/serve/`: real TLS listener on `127.0.0.1:0` with separate temp homes for A and B; push/pull/list/show/set/unset/delete roundtrip, wrong/missing token 401, wrong fingerprint refused, traversal names 400, oversized body 413, read-only 403, auth lockout 429, masked output, identity reuse, remotes file validation.
