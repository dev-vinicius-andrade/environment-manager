# Goals

## 1. Environment profiles (per shell session)

Named profiles of environment variables (`local`, `dev`, `external`, `test1`...). Activating a profile makes each variable (e.g. `MY_API_URL`) take that profile's value in the current shell only. Use cases: multiple GCP accounts, multiple auth keys per service. Activation emits shell code for the shell to evaluate (a child process cannot change its parent's environment); deactivate restores previous values.

Owner request (implemented): a profile may contain environment settings, hosts settings, or both, and the user applies whatever they want: environment only (default), hosts only, or both (`use --hosts`, `use --only hosts|env|all`, `hosts apply`, the TUI scope dialog, the tray's scopes). The hosts part is machine-wide and written by envmgr into its managed block (one applied profile at a time, needs elevation); see `profiles.md` and `hosts.md`.

## 2. Remote control between machines

One machine (the **parent**) controls the profiles of other machines (its **children**). Implemented; details in `remote.md`, steps in `../instructions/connect-a-child.md`.

### Decisions (owner)

- **Hub model: the child dials the parent.** The parent runs `envmgr parent serve`; children make outbound HTTPS long-polls and expose no port, so it works behind NAT. One parent controls many children; each child has exactly one parent.
- **Pairing is a one-time join code.** `envmgr parent invite` prints `envmgr join <code>`; the code is single-use, expires (default 15 min), carries the parent address and certificate fingerprint (pinned by the child), and is exchanged for a per-child credential. Revoke per child with `envmgr parent revoke`.
- **Scope: profile operations** (list/get/put/delete profile, set/unset variable), with optional read-only grant. No remote command execution, no shell, no arbitrary paths.
- **Hosts file editing** (owner request): local `envmgr hosts` edits only an envmgr-managed block of the hosts file. Remotely it needs a separate, explicit grant (`parent invite --allow-hosts`, off by default) because it can redirect names (DNS hijack). Profiles carry their hosts entries over the hub (a profile edit, no grant); applying them on a child (`parent hosts <agent> apply-profile`) needs the grant and a read-write child and uses the child's own profile. See `hosts.md`.
- Transport: custom TLS 1.3 hub with certificate pinning (no CA, no SSH dependency). The earlier shared-token `serve`/`remote add` design was replaced entirely.

### Design constraints (still apply)

- Explicit pairing/trust and authenticated, encrypted transport; no unauthenticated mutation endpoint.
- Profiles contain secrets: values travel only inside TLS, are masked in output by default, and raw invite secrets/credentials are never stored on the parent.
- Narrow, allow-listed operations over generic remote execution.

### Open questions

- Running the hub/agent as a background service (today: foreground commands).
- Fan-out commands across many children.

## 3. Dotfiles

The owner keeps a dotfiles repository (`E:\repos\github\dev-vinicius-andrade\dotfiles`) and wants envmgr to configure the machine from it: `envmgr dotfiles clone <source> <dest>`, `envmgr dotfiles link`... Implemented for the local machine (clone, register, status, link, unlink, pull; manifest in the repo's `.dotfiles.config.json`); see `dotfiles.md` and `../instructions/dotfiles.md`. Open: `adopt`, profiles choosing which links to apply, parent control of a child's dotfiles (behind an explicit grant).
