# End-to-end demo (parent and children on one host)

Simulates one parent and two children with separate `ENVMGR_HOME` dirs under `.demo/` (git-ignored). Detailed steps for real machines: `connect-a-child.md`.

1. Build: `go build -buildvcs=false -o .bin/e2e/envmgr.exe main.go`
2. Seed children: `ENVMGR_HOME=.demo/child1 .bin/e2e/envmgr.exe set dev MY_API_URL https://dev.child1`
3. Parent: `ENVMGR_HOME=.demo/parent .bin/e2e/envmgr.exe parent serve --addr 127.0.0.1:7788` (keep running).
4. Invite: `ENVMGR_HOME=.demo/parent .bin/e2e/envmgr.exe parent invite --name child1 --addr-public 127.0.0.1:7788 [--read-only]` prints `envmgr join <code>`.
5. Child: `ENVMGR_HOME=.demo/child1 .bin/e2e/envmgr.exe join <code>` (keep running).
6. Operate from the parent: `parent agents`, `parent profiles child1`, `parent show child1 dev [--reveal]`, `parent set child1 dev X 1`, `parent push|pull child1 <profile>`, `parent revoke child1`.
7. Activate in a shell: bash `eval "$(envmgr use dev --shell bash)"`; PowerShell `envmgr use dev --shell powershell | Invoke-Expression`; TUI: `eval "$(envmgr tui)"`.
8. Negative checks: reusing a join code -> rejected; `join` on an already bound machine -> refused without `--force`; read-only child refuses mutations; revoked child stops within seconds; `use nope` -> exit 1 with empty stdout.

Last full run: all of the above behaved as expected.
