# Windows Docker startup recovery — 2026-10-10

Scope: the project's shared startup helper and the local safe-start shortcut.
State: `VERIFIED` for this named startup/recovery scope, tested source
`1f7cae04f1878f64b760e02873b5b7c541475afb`. Baseline:
`3eb2c13cc4cd0009a8e33df1beabc1b992f96f03`.

## Failure and repair

Docker Desktop 4.67.0 reported two startup crashes at the Inference listener:
`dockerInference`, Windows error 1920. Repairing that IPC directory exposed the
same error at the Secrets Engine's `engine.sock` during a genuine restart.
These are observed local failures, not an inference from service status.
`com.docker.service` being stopped is permitted for this WSL2 Linux setup;
[Docker's permission documentation](https://docs.docker.com/desktop/setup/install/windows-permission-requirements/)
explains the distinction. Similar socket failures remain reported in
[Docker's tracker](https://github.com/docker/desktop-feedback/issues/531).

`scripts/rag-agent-e2e-runtime.ps1` now checks the actual engine before launching
Desktop. A running backend is reused; a short startup mutex prevents project
callers from archiving IPC while another caller launches it. When both Desktop
and backend are absent, only these known transient directories are considered:

- `%LOCALAPPDATA%/Docker/run`: `dockerInference`, `userAnalyticsOtlpHttp.sock`;
- `%LOCALAPPDATA%/docker-secrets-engine`: `engine.sock`.

The root and parent directories must not be redirected. Unknown entries,
directories, nonempty accessible files and errors other than 1920 are refused.
Accepted IPC is renamed into a unique sibling archive, preserving its contents.
The helper does not delete containers, volumes, images, settings or WSL disks,
force-stop Desktop, restart WSL or change system proxy/service configuration.
All existing project callers reuse this helper.

A local desktop shortcut, `Docker安全启动.lnk`, invokes the same helper; its
target and arguments are read back and verified. The vendor's original direct
launch does not run this recovery. No vendor binary, installed version or
Windows kernel was changed, and reboot/login behavior has not been verified.

## Genuine runtime evidence

```powershell
$env:CODE_AGENT_RUN_DOCKER_RESTART_E2E = '1'
powershell.exe -NoProfile -ExecutionPolicy Bypass -File tests/e2e/docker_desktop_restart.ps1 -Rounds 3
```

Final run: `docker-restart-642926a1f12f47289f7f8d2271052897`.
Receipt: `output/playwright/<run>/receipt.json`, SHA-256
`486815d6e07d464cc1f7b2060e941190f11bd831a2ae5d8829c19abb8194a0d6`.
**3 planned / 3 completed, exit 0, source unchanged**; Windows PowerShell 5.1.
Each round orderly-stopped Desktop, archived actual residual IPC, cold-started
the Linux engine 29.3.1 and executed an offline container. Startup times:
19,908 / 20,273 / 19,837 ms.

The test reads back a fixed marker in its own persistent volume through an
unprivileged, read-only container mount after every restart. Before each stop,
running user containers cause refusal. The 61 preexisting container IDs and
stable configuration hash, 620 preexisting volume names and Desktop settings
hash all match after each round. Existing volume contents are not mounted,
read or hashed; this is not bytewise verification of every user's data file.
Owned test containers and volumes are retained; no cleanup deletion is used.

Cached input image, no implicit pull:
`alpine@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc`.
Model calls: **0**. This is Docker lifecycle/data-persistence evidence, not a
Go/Python/tool trace or a repository coding-task acceptance receipt.

## Regression and failed runs

```text
python -m pytest -q tests/test_docker_desktop_startup.py tests/test_rag_agent_e2e_script.py tests/eval/test_release_gate_workflow.py
```

**33 passed, exit 0**. Contracts include cold-start archive preservation,
healthy-engine reuse, backend-in-progress reuse, unknown/nonempty content and
junction refusal. The producer's failure contract injects an unavailable
readiness boundary in a child process, without executing Docker commands:
planned 3/completed 0, exit 1, unsampled counts null and probe state unknown.
Windows CI runs these startup contracts; existing Linux Go/Python/frontend
jobs remain enabled. PowerShell parsing and `git diff --check` exit 0.

The initial real restart failed after the Inference-only repair at Secrets
Engine: `docker-restart-130ffed77f104393a2b04ee2ced86e70`, planned 3/completed 0,
exit 1, receipt SHA-256
`ba855d59aed9da62ea9f291a651a49111073b0909293f47cfef123ecacc0b6f4`.
That early producer unconditionally described preservation in its footer;
this text is not accepted evidence of preservation. The original is retained,
and the final producer records per-round checks and null/unknown explicitly.

Intermediate three-round successes `docker-restart-1dc5a2626dda47ce8bea2217e7069151`
and `docker-restart-917fabb62fbc4e8a81dbb3d911504cbe` are retained. Their producer
or test hashes differ from the final files; they do not substitute for the
final frozen-source run. The first full Python regression failed at an old
CI-platform assertion: 2,423 passed/17 skipped/1 failed, exit 1, log and XML
in the `1dc5...` run. Its assertion was updated to preserve the three Linux
jobs and explicitly require the new Windows job. RED test/tool excerpts are
saved separately as diagnostics, not manufactured runtime receipts.

Standards and Spec reviews: `AI_REVIEWED`, no remaining actionable findings.

Final complete Python regression:

```text
python -m pytest -q --junitxml=<artifact>/python.xml
```

**2,426 passed / 17 skipped / 0 failed, 31 dependency warnings, exit 0**.
Run `docker-python-55e7b7d9-d85b-45b0-9f22-9ca28fc70e2b`; **883** source/config
hashes unchanged. Receipt `output/playwright/<run>/receipt.json`, SHA-256
`6228d063abafbd3809a1fa30d10cda61324402bf1c63cc38789111cbcd091bb6`.
Go/frontend production source is unchanged in this slice; their full tests
were not repeated locally. Hosted CI still runs them.

Failure excerpts: `output/playwright/docker-startup-e69d9758cbcc4f37b0b6f818ca5d9ea7/backend-crashes.json`,
SHA-256 `f32c12087295ed23994ee22ef4a2e7c2659e404a67aa027f261b1ac69c517b4e`;
separate tool/test diagnostics SHA-256
`a6f19df2ae8723046d78e1ed1ef86ead4e54245df7d72e372a20d852648ab887`.
The latter is an excerpt collection; it is not a complete runtime receipt.

Post-commit source binding checked all **4/883** raw hashes and committed blobs
(only Git CRLF conversion normalized). Original receipts are not rewritten.
Binding: `output/playwright/docker-startup-e69d9758cbcc4f37b0b6f818ca5d9ea7/source-binding.json`,
SHA-256 `3cc37dbdac9e47996c80e46df451754824db15f6a9f882b729cac05264511217`.
The following evidence-only commit changes no tested source/configuration.
