# Goals

## 1. Environment profiles (per shell session)

Named profiles of environment variables (`local`, `dev`, `external`, `test1`...). Activating a profile makes each variable (e.g. `MY_API_URL`) take that profile's value in the current shell only. Use cases: multiple GCP accounts, multiple auth keys per service. Activation emits shell code for the shell to evaluate (a child process cannot change its parent's environment); deactivate restores previous values.

## 2. Remote control between machines

One machine should be able to control another (stated by the owner, details not yet defined). Candidate meanings, to be decided with the owner:

- Apply/switch a profile on a remote machine (remote `use`, `set`, `list`).
- Manage the remote machine's hosts file and environment variables.
- Sync profiles between machines (push/pull) without remote execution.
- Run arbitrary commands remotely (largest scope, highest risk).

### Design constraints (security-sensitive)

- Needs explicit pairing/trust between machines and authenticated, encrypted transport; no unauthenticated listener.
- Profiles contain secrets (keys, tokens): never transmit or store them in plain text without protection.
- Prefer a narrow, allow-listed command set (profile/hosts operations) over generic remote execution.
- Transport options to evaluate: SSH (reuse existing auth, no daemon of our own) vs. a custom agent/daemon with mTLS.

### Open questions

- Which of the candidate meanings above is the target for the first version?
- Same network only, or across the internet / NAT?
- One controller to many machines, or peer to peer?
- Operating systems involved (Windows, Linux, both)?
