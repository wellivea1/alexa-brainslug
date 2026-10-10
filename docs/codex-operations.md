# Brainslug-managed Codex tasks

The implementation supplies a separate `brainslug-codex` controller and worker,
an enrolled HTTPS gateway client, private dashboard controls at `/codex`, and
explicit Alexa task intents. Automatic routing stays disabled until the owner
configures a separate router and approves its evaluation. Manual direct answers
continue to use the existing gateway provider.

This is host-local Codex execution. Desktop sidebar placement, desktop project
registration, safe concurrent desktop ownership, physical multi-machine LAN
operation, real account inference access, and physical Echo timing remain release
gates. Directory selection does not establish desktop project placement.

## Build and install independently

```powershell
go build -o bin/brainslug-codex.exe ./cmd/brainslug-codex
bin/brainslug-codex.exe version
```

Install the same independent binary on a controller machine and each worker.
The controller owns its local database and routing credentials. Each worker owns
its local ledger, repositories, Codex executable, and supported local sign-in.
The gateway owns its distinct client identity. Never share a SQLite file, mount
worker repositories on the controller, or copy desktop token files.

Copy the example JSON files under `docs/examples` into a private configuration
directory outside the checkout. Replace every placeholder. Relative paths are
resolved from the working directory, so run from the configuration directory or
use absolute paths. The examples intentionally have no enrolled identities or
usable model/directory defaults.

## Pair trust and service identities

From the private configuration directory:

```powershell
brainslug-codex.exe init-certs --directory pki --server-name 127.0.0.1 --workers dev-desktop
```

For a separate LAN controller, choose its TLS DNS name or private IP instead.
Bind `listen_addr` to a specific private LAN IP. Wildcard and public IP bindings
are rejected. Only this dedicated TLS service endpoint belongs on the LAN; keep
the gateway management listener private and its public ASK path restriction.

Pairing creates a private CA, a controller server identity, a gateway client
identity, and one distinct identity per worker. Import `pki/identities.json` into
the controller's `registry.identities`. Keep the CA private key offline. Transfer
only each machine's own certificate/key and the CA certificate. Pairing refuses
to overwrite existing files. Leaf certificates expire after six months; review
their validity and rotate before expiry.

The server requires TLS 1.3, a trusted client chain, and an enabled enrolled
leaf fingerprint. CA trust alone does not enroll a client. Gateway grants bind
exact `namespace:subject` keys. Worker identities bind exact host IDs.
Controller configuration and dedicated data directories are restricted to their
owner. Use private directories for PKI, configuration, and launcher logs too.

## Establish the local runtime contract

The adapter was developed against the installed `codex-cli 0.162.0-alpha.2`
non-experimental schema. Configure both worker `codex_version` and process
`expected_version` to the exact chosen version. The executable's `--version`
must match even when custom process arguments are configured. Use an absolute
executable path to prevent a desktop update from silently substituting a binary.

The default process is `codex app-server --stdio`. Initialization, paginated
model discovery, thread start/read/resume, explicit per-turn model and effort,
sandbox policy, approvals, interruption, and final status are exercised by a
disposable JSON protocol process in tests. Raw process stderr and protocol error
messages never cross the worker boundary. Windows uses an owned job object to
terminate descendants; Unix uses an owned process group.

Sign in through the supported Codex CLI workflow on each worker. Then explicitly
probe its local account and catalog:

```powershell
brainslug-codex.exe probe --config worker.json --live
```

The probe starts no turn. Catalog presence does not prove inference entitlement.
Choose a catalog model/effort pair explicitly, put it in the controller host
allowlist and project profile, and put it in the worker's local project profiles.
An unavailable pair produces a saved error or attention state; there is no
machine, model, reasoning, account, or endpoint fallback.

First run a disposable read-only task with known credentials to prove inference,
continuation, cancellation, and saved-state recovery. Before upgrading Codex,
repeat schema inspection, the probe, and that disposable test. Do not point the
worker at a live repository as an installation test.

The installed schema's read-only sandbox prevents writes; it does not prove
filesystem read isolation to the selected directory. Workspace-write adds the
registered writable root and disables network access. Principals granted to a
worker must be trusted for that host's read access. A deployment needing stronger
read isolation requires an independently verified OS/runtime boundary before use.
Command escalation and external write roots cannot be approved through this pilot.
Only an exact pending file-change approval within an existing registered root
and a workspace-write profile can be accepted. Ordinary preauthorized sandbox
work proceeds without repeated confirmation.

## Run controller, worker, and gateway

```powershell
brainslug-codex.exe controller --config controller.json
brainslug-codex.exe worker --config worker.json
```

Each worker connects outbound over authenticated WebSocket. The hello must match
the enrolled host, protocol version, exact Codex version, registered project IDs,
and allowed model/effort catalog. No shell, arbitrary path, or raw Codex protocol
is exposed over the LAN. Worker roots are canonicalized locally and checked again
at execution. The controller sends project IDs only.

Configure the gateway independently:

```dotenv
CODEX_ENABLED=true
CODEX_ROUTING_MODE=manual
CODEX_CONTROLLER_URL=https://127.0.0.1:9091
CODEX_CLIENT_CERT=C:/private-gateway/gateway.pem
CODEX_CLIENT_KEY=C:/private-gateway/gateway.key
CODEX_CA_FILE=C:/private-gateway/ca.pem
CODEX_SERVER_NAME=127.0.0.1
```

These non-secret connection references and routing mode can also be saved in the
authenticated dashboard. Per-field runtime overrides preserve environment
precedence and serialize through the same settings writer as inference settings.
The browser receives no certificate key bytes or worker credentials.

Use `/codex` to register hosts, add/update project profiles and principal grants,
enroll/revoke fingerprints, choose explicit execution settings, import an existing
thread, start/continue work, inspect saved results, reply to clarification,
respond to a bounded pending approval, and request cancellation. Imported threads
are verified for canonical directory, archive state, and an idle turn before
resuming. An incompatible or missing thread is not silently replaced.

The page shows active executions plus twenty recent requests; open a task for its
full retained result. IDs, routing reasons, shadow choices, router latency and
tokens, execution token reports, and host last-contact time are visible. Direct
answer provider token usage is currently unavailable and labeled accordingly.

On Windows the per-user launcher is independent from the gateway:

```powershell
scripts/windows/install-codex-startup.ps1 -Mode controller -Config C:/private-controller/controller.json -Executable C:/tools/brainslug-codex.exe
scripts/windows/install-codex-startup.ps1 -Mode worker -Config C:/private-worker/worker.json -Executable C:/tools/brainslug-codex.exe
```

This explicitly installs `Brainslug-Codex-controller` or `Brainslug-Codex-worker`
with limited, interactive user authority, hidden processes, and bounded restart.
It does not install or stop the gateway task. Linux controllers can use the same
binary/config contract under an operator-managed service. This checkout has no
Docker runtime; container operation has not been exercised here.

## Routing and evaluation

The controller's optional `router` object uses these fields:

```json
{
  "base_url": "https://api.openai.com/v1",
  "model": "SET_EXPLICIT_ROUTER_MODEL",
  "effort": "low",
  "api_key_env": "BRAINSLUG_ROUTER_API_KEY",
  "max_output_tokens": 1024,
  "timeout_seconds": 6
}
```

Choose exactly one `api_key_env` or `api_key_file`. No gateway secret is copied.
The adapter makes one strict Chat Completions JSON-schema call with tools
disabled, bounded catalog/input/output, explicit model/effort, no redirect,
no repair retry, and no alternate endpoint. HTTPS is required except for a local
loopback inference server supporting that exact format.

An optional `answer` object separately configures `provider`, `model`, `base_url`,
`api_key_env` or `api_key_file`, `api_format` when compatible, `max_output_tokens`,
and `timeout_seconds`. It uses API-key authentication independently from worker
Codex accounts. Automatic ordinary answers belong to the controller lifecycle.

Begin with manual selections in shadow mode. Invalid proposals are recorded as
routing failure and cannot change the explicit destination. Automatic mode is
disabled by default and requires both an accuracy target (suggested evaluation
target: 0.95) and owner acknowledgement that representative shadow results meet
it. This acknowledgement is a release decision, not an automatically verified
accuracy claim. Explicit action/project/conversation overrides bypass automatic
selection; continuation profiles stay fixed unless explicitly changed.

The evaluator consumes JSON `Example` records containing `name`, `prompt`, an
authorized `catalog`, and `expected` decision. Label normal questions, repository
tasks, same-named projects on different hosts, ambiguous references, follow-ups,
model overrides, and hostile instruction attempts. It reports exact destination
accuracy, failed case names, latency, and token usage without dispatching work:

```powershell
brainslug-codex.exe evaluate-router --config controller.json --cases labeled-examples.json --live
```

This opt-in call can consume the dedicated routing API budget. No live router
accuracy, latency, or dollar-cost result has been measured in this implementation.

## Voice behavior and explicit grants

Publish the updated `skill/interaction-model.json` through the ASK setup workflow.
Enroll the exact skill-scoped Alexa account ID as `alexa:<id>` on the gateway
identity and grant it selected projects. A valid Alexa signature/skill does not
grant repository access or establish which person spoke on a shared Echo.

Examples: "ask Codex to inspect startup in pilot", "latest task status", "read
the latest task result", "continue Codex with add tests", "reply to Codex pilot",
and "cancel the latest Codex task". Named status/cancel references must resolve
unambiguously. Follow-ups to multiple conversations ask for clarification rather
than guessing. Generic stop/cancel ends only the voice session. Approvals belong
to the authenticated dashboard, not voice. No unsolicited completion announcement
is promised.

The gateway forwards the verified ASK request ID and principal. Saved acceptance
is acknowledged within the existing seven-second budget; execution has its own
deadline and survives the Alexa session ending. If acceptance is lost, the client
retries once with the identical key and payload. If the controller cannot confirm
acceptance, the gateway says so and creates no local queue.

## Recovery, cancellation, and retention

The controller persists acceptance before inference and its attempt ID before
transmission. The worker persists the attempt and thread binding before turn
submission. Identical ingress deliveries return one job; changed payloads conflict.
Worker attempt deduplication never treats a JSON-RPC ID as Codex turn deduplication.
Controller and worker databases reject the other service's schema and newer
versions instead of silently downgrading.

One active attempt owns each host and each thread; a bounded global concurrency
and admission queue apply. Accepted permission/budget snapshots never expand when
policy changes, and current grants are checked again at dispatch. Deadlines and
observed token limits interrupt execution; token reporting lag prevents an exact
spend ceiling.

During a partition, accepted execution may finish within its original limits and
spool results. Disconnected workers cannot acquire new authority or extend limits.
Approvals expire locally. Offline/dispatched work becomes uncertain without
reassignment. Cancellation remains requested until a confirmed interruption or
saved-turn reconciliation; it does not roll back edits.

On restart, only routing may be retried. Possibly submitted turns are read from
saved history. Missing or inconclusive turn evidence becomes `needs_attention`,
holds capacity, and is not replayed. The owner can inspect the project/thread and
acknowledge uncertain stopped work through the exact worker. An absent worker
ledger entry is recorded as a terminal tombstone before acknowledging, preventing
a late original dispatch from executing afterward.

Worker spools keep ordered facts until acknowledgement, including lost-result
replay. Terminal controller jobs become purgeable only after the worker confirms
it durably acknowledged its result; this prevents retention racing a lost reply.
The controller retains at most 256 recent events per job. Worker storage admits
at most 512 retained attempts and 1024 unacknowledged facts per attempt, preserving
room for final evidence by dropping excess noncritical progress. Full storage
refuses new authority. Retention removes acknowledged terminal operational data,
never unresolved attempts. Codex's own history is independent and is not deleted
by either purge.

Rotate a client by enrolling its replacement fingerprint, changing that process's
certificate/key, verifying reconnection, then disabling the old fingerprint.
Registry updates close worker sessions and renegotiate policy. Revocation does
not silently move or stop an already accepted bounded job; reconnect/reconcile
its original host to determine the outcome.

## Remaining release evidence

Local tests use fake routers/runtimes, an actual disposable JSON subprocess,
temporary SQLite, mutually authenticated loopback HTTPS/WebSocket, and sanitized
ASK envelopes. They exercise two host identities, partitions, replay, scope,
version negotiation, approval limits, cancellation, death, and uncertainty.

Before operational use, separately record: two physical LAN machines with real
pairing and revocation; a chosen supported Codex release and account's real
inference/continuation/cancel/recovery in a disposable project; representative
live shadow-router accuracy/latency/usage; desktop placement/restart/idle ownership
or a clearly accepted limitation; an actual Echo acceptance/result round trip;
and startup/container behavior on the selected deployment machines.
