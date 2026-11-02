# End-to-end demo (two machines on one host)

Simulates machine A (controller) and B (agent) with two `ENVMGR_HOME` dirs under `.demo/` (git-ignored).

1. Build: `go build -buildvcs=false -o .bin/e2e/envmgr.exe main.go`
2. Seed B: `ENVMGR_HOME=.demo/machineB .bin/e2e/envmgr.exe set dev MY_API_URL https://dev.example.com`
3. Run the agent: `ENVMGR_HOME=.demo/machineB .bin/e2e/envmgr.exe serve --addr 127.0.0.1:7777` (prints fingerprint and token; `--show-token` re-prints).
4. Pair A: `ENVMGR_HOME=.demo/machineA .bin/e2e/envmgr.exe remote add b 127.0.0.1:7777 --token <T> --fingerprint <FP>`
5. Exercise: `remote profiles b`, `remote pull b dev`, `remote set b dev X 1`, `remote show b dev --reveal`, `remote push`, `remote delete`.
6. Activate: bash `eval "$(envmgr use dev --shell bash)"`; PowerShell `envmgr use dev --shell powershell | Invoke-Expression`; undo with `deactivate`.
7. Negative checks: wrong token -> 401, wrong fingerprint -> refused, no token via curl -> 401, `use nope` -> exit 1 with empty stdout.

Last full run: all of the above behaved as expected (bash and PowerShell round trips; an injection payload in a value stayed inert).
