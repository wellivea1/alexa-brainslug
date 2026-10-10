# Codex Integration Plan

Status: local implementation and fake-runtime verification completed; operational
release evidence remains pending. Researched against the checkout, the installed
`codex-cli 0.162.0-alpha.2` schema, and official OpenAI documentation on October 8,
2026. Setup and limitations are in [Codex operations](codex-operations.md).

## Implementation Progress

| Sequence | Local implementation | Remaining external evidence |
| --- | --- | --- |
| 0. Integration boundary | Exact executable/version check; initialization, catalog, new/resumed thread, final status, bounded approval, cancel and uncertainty tested with a disposable JSON process. No stable `project/*` method in the inspected schema. | Real account inference and disposable-turn proof; supported desktop placement and idle ownership. |
| 1. Binary and durable layer | Controller/worker modes, isolated SQLite schemas, mTLS, versioned outbound WebSocket, idempotency, spool/receipt, limits, cancel, recovery and retention. Two fake workers use actual loopback TLS. | Two physical LAN machines and deployment-specific transport/firewall checks. |
| 2. Dashboard | Owner connection, host/project/profile/grant registration, identity rotation/revocation, start/continue/import, results, clarification, approval and cancellation controls. | Real account execution and imported-thread availability. |
| 3. Router | Independent tool-free strict adapter, explicit overrides, stable continuations, shadow records, evaluator and owner-gated automatic mode. | Representative live accuracy, latency and usage meeting the owner-set target. |
| 4. Voice | Verified request ID/principal, saved acceptance, status/result, follow-up, durable clarification, explicit task cancel, updated interaction model and sanitized tests. | Published ASK model and physical Echo timing/result proof. |
| 5. Operations | Separate startup scripts, pairing/rotation, availability, explanations/usage, import, retention and upgrade/probe instructions. Desktop placement is labeled unverified. | Supported desktop bridge, real startup/upgrade checks and container deployment. |

The sandbox limits writes and disables network access. The installed schema does
not establish read isolation to a selected directory; trusted host-read principals
or a separately verified isolation boundary are required. Live repositories,
accounts, public services and physical Echoes were not used as demonstrations.

## Recommended Design

Make Brainslug a voice entry point for Codex work. A small routing model proposes
which machine, project, conversation, model, and reasoning level should handle a
request. A separate `brainslug-codex` binary validates that proposal, persists a
job, and dispatches it to Codex on the selected machine. The existing Brainslug
server remains the Alexa and private-dashboard gateway.

Give the new binary two modes: `controller` for routing and durable orchestration,
and `worker` for supervising Codex on an execution machine. The controller can run
on its own LAN machine, independently of both the Brainslug server and workers.
The same modes can be colocated for a simpler installation.

Codex CLI is the agent runtime, not the routing model. Use a separately configured
model for routing and select a worker model for each accepted task. Begin with a
tool-free structured inference call for routing; use app-server for Codex work.
OpenAI recommends app-server for product integrations needing persistent
conversations, streaming, and approvals. [Integration guidance](https://developers.openai.com/blog/codex-as-a-platform)

```text
Machine A: brainslug
  Alexa verification, voice responses, private dashboard
       |
       | authenticated HTTPS: submit / status / reply / cancel
       v
Machine B: brainslug-codex controller
  routing model, project registry, durable queue, policy, results
       ^                             ^
       | authenticated outbound      | authenticated outbound
       | worker connection           | worker connection
Machine C                         Machine D
  brainslug-codex worker             brainslug-codex worker
    -> local Codex app-server          -> local Codex app-server
    -> local repos and credentials     -> local repos and credentials
```

Expose two routing settings: `manual` and `automatic`. Manual preserves the
current explicit provider/model behavior. Automatic explicitly authorizes choices
within an owner-configured catalog and budget. Record every choice; an unavailable
model must not silently become a different model or billing route.

## Separate Binary And LAN Boundary

Use one additional Go binary with illustrative commands:

```text
brainslug-codex controller --config controller.json
brainslug-codex worker --config worker.json
```

The controller exposes a versioned HTTPS task API to Brainslug. Workers establish
outbound authenticated WebSocket connections to the controller, receive typed
operations, and return progress and results. Each worker talks to its local
Codex process over stdio. No machine needs to expose an unrestricted shell or
the full Codex protocol to the LAN.

Suggested controller API: submit task, get task, list eligible projects/hosts,
reply to clarification, respond to approval, cancel, and subscribe to events.
Use stable request IDs, bounded messages, and reconnectable event sequence
numbers. Polling status remains sufficient for Alexa. The authenticated
dashboard proxies controller operations through the existing Brainslug listener;
its browser does not receive worker credentials.

Enroll the Brainslug gateway and each worker with separate revocable identities.
Use TLS with pinned trust and mutual authentication for the first version. Pair
hosts explicitly; do not trust LAN membership or automatically enroll machines
found through discovery. Bind only the controller's dedicated service endpoint
to the LAN. Brainslug's admin port stays on loopback and its public ASK listener
retains the current path restriction.

Ownership is explicit:

| Process | Owns |
| --- | --- |
| Brainslug server | ASK verification, voice session mapping, owner dashboard, existing manual direct-answer provider. |
| Controller | Routing credentials, routing policy, authoritative job database, host/project registry, task results. |
| Worker | Local project paths, local Codex account, process lifecycle, active execution ledger, runtime limits. |

Package and update the new binary independently of the Brainslug server. Reuse
the Windows installer's per-user startup pattern with separate controller and
worker configuration, logs, and health checks. Updating the voice gateway must
not stop running Codex jobs. Localhost deployments use the same versioned API.

Give the controller a separate SQLite file and schema. Never share a SQLite file
over the LAN. Brainslug may cache task summaries, but the controller determines
whether a task was accepted or completed. Workers persist enough execution state
to reconcile reconnects; they do not independently choose new destinations.

In automatic mode, the controller owns the entire routed request, including
ordinary answers through its explicitly configured provider. This keeps one
authoritative task lifecycle. Existing manual direct answers can stay on the
Brainslug server. Configure controller credentials independently; do not copy
secrets from the Brainslug host automatically.

## What "Project" And "Thread" Mean

A project selects files, instructions, and execution policy. A thread selects
conversation history. A new task can start a new thread inside an existing
project; these are independent choices.

The CLI associates work with a directory. It does not expose the desktop
ChatGPT Projects view. [Projects documentation](https://learn.chatgpt.com/docs/projects)

Plan for two capabilities:

1. **Host-local project execution:** the controller selects a registered host
   and project; its worker resolves the canonical directory and runs or resumes
   a Codex conversation there.
2. **Desktop project placement:** the resulting chat appears in the intended
   existing desktop project and can be continued from the app.

The first has a documented implementation path. The second needs an early
compatibility experiment. The installed CLI is `0.162.0-alpha.2`; its generated
non-experimental client schema contains thread and turn methods but no
`project/*` method. This does not establish desktop project registration or
cross-client live ownership.

Tools available to an assistant inside the Codex desktop app, such as project
listing or chat creation, are not automatically callable APIs for an external
Brainslug process. Do not build against private desktop databases or assume a
shared directory or Codex home guarantees sidebar placement. Keep an optional
desktop bridge behind its own interface if a supported integration is verified.
Cloud ChatGPT projects and local Codex projects are separate future targets.

## Fit With The Existing Code

| Current component | Planned change |
| --- | --- |
| `internal/app/service.go` | Keep direct answering; add a task-controller port for submission, state, and replies. |
| `internal/alexa/handler.go` | Pass verified request ID and principal to submission; render pending, clarification, status, and completed states. |
| `internal/inference/manager.go` | Preserve manual provider settings; keep controller profiles separate from this global provider. |
| `internal/config/runtime_settings.go` | Persist explicit routing mode and non-secret controller connection settings. |
| New `internal/control` | Own routing, durable scheduling, host selection, authorization, and agent-runtime ports. |
| New `internal/controlstore` | Own the controller's independent append-only migrations and job/project/conversation tables. |
| New `internal/controlapi` | Implement authenticated gateway/worker transport and protocol version negotiation. |
| New `internal/codex` | Implement worker process supervision, local protocol handling, event translation, and cancellation. |
| `internal/web` | Add project registration, connection health, task activity, clarification, and approval controls. |
| `cmd/brainslug/main.go` | Wire the remote task-controller client. |
| New `cmd/brainslug-codex/main.go` | Wire controller or worker mode and own each mode's shutdown. |

The current `Ask` call is synchronous. Alexa has a seven-second application
budget, and provider timeouts are capped at six seconds. `ConversationID` records
the Alexa session but is not provider conversation memory. A durable queue and
explicit conversation mapping are prerequisites, not later enhancements.

Keep Codex outside the existing single-response `Provider.Ask` abstraction:
agent work has progress, approvals, cancellation, and resumable conversations.
Use a narrow controller-owned `AgentRuntime` port for worker execution. Keep
network and Codex protocol types out of application use cases. No Alexa+ MCP
integration is required for this design.

## Request Routing

Run deterministic rules first for explicit project aliases, job references,
status, cancel, and owner-specified model/effort. These should not consume an
extra model call. Classify remaining task requests with a fixed routing model.

Give the router only:

- the current request and a small relevant conversation summary;
- authorized host/project IDs, aliases, descriptions, and availability;
- a bounded shortlist of eligible thread IDs, titles, summaries, and status;
- allowed worker model/effort pairs and configured task limits.

The router returns a schema-validated proposal. For example, this is a Brainslug
decision object, not an app-server message:

```json
{
  "action": "codex_new",
  "host_id": "dev-desktop",
  "project_id": "brainslug",
  "conversation_id": "",
  "profile": "engineering",
  "model": "gpt-6.1-sol",
  "effort": "medium",
  "title": "Repair Windows startup",
  "reason": "Repository change with a clear project and a new objective",
  "clarification": null
}
```

Allowed actions: `answer`, `codex_new`, `codex_continue`, `status`, `cancel`, and
`clarify`. Use strict schemas and separate validation rules for each action.
The router cannot invent a path, grant access, run a command, select an arbitrary
thread, or change account credentials. The controller resolves IDs and enforces
policy; the worker independently validates the local project and permission scope.
Treat request text and retrieved summaries as data, not routing instructions.

Recommended router transport: a direct structured-output call with no tools,
through a dedicated routing adapter and explicitly selected credentials. Do not
assume the existing provider port already supports structured routing output.
If the owner wants everything to use Codex authentication, prototype an isolated
Codex router with tools disabled and a schema-constrained final response.
`codex exec --output-schema` supports the latter output contract, but process
startup and tool isolation need measurement before selecting it for every
request. [Non-interactive mode](https://learn.chatgpt.com/docs/non-interactive-mode)

Malformed or ambiguous routing must produce a retryable routing error or a short
clarification. It must never guess a filesystem destination. Permit at most one
bounded repair attempt for invalid output, recorded as routing usage.

## Model And Reasoning Policy

Initial profiles to evaluate, conditional on account access:

| Role | Candidate model | Starting effort | Intended use |
| --- | --- | --- | --- |
| Router | `gpt-6-luna` | `low` | Destination and task classification. |
| Quick answer | Configured direct-answer profile | Configured setting | Preserve the existing selection for manual mode; configure the controller separately for automatic mode. |
| Small Codex task | `gpt-6-luna` | `low` | Narrow inspection or a simple change. |
| Engineering | `gpt-6.1-sol` | `medium` | Typical implementation, debugging, and multi-file work. |
| Deep analysis | `gpt-6-astra` | `high` | Difficult diagnosis or architecture under an explicit larger budget. |

These are proposed starting points, not measured Brainslug results. OpenAI's
current guidance places Luna toward efficiency, Sol toward complex technical
work, and Astra toward demanding analysis; benchmark representative requests
before choosing defaults. [Model selection](https://developers.openai.com/api/docs/guides/model-selection)

Discover model and effort choices separately for each host/account and intersect
them with the owner's allowlist. Keep API model catalogs separate from Codex catalogs.
Catalog presence alone does not prove inference entitlement; the documented
ChatGPT-plan app-server configuration can return a bundled catalog.
[Catalog and access behavior](https://developers.openai.com/siwc/token-sharing-open-source/codex-app-server)

Precedence: explicit request override, locked project policy, existing thread
profile for a continuation, then automatic selection. Reject conflicts rather
than silently relaxing a project restriction. Let automatic mode choose the
initial profile; keep continuations stable unless the user requests a change or
an explicitly enabled escalation rule applies at a turn boundary.

Give router and worker separate timeout and usage limits. Start with one active
job per host plus a small global concurrency cap, a bounded queue, and a maximum
job duration. Track routing and execution usage separately. Enforce admission
limits and interrupt at runtime
limits, while allowing for usage-reporting lag; do not promise an exact dollar
ceiling for subscription usage or delayed token reports.

## Project And Conversation Selection

Maintain a controller project registry containing ID, name, spoken aliases,
description, execution host, permitted principals, default profile, allowed
models, and permission policy. Workers register owner-approved canonical roots;
the controller stores their association but sends project IDs, not arbitrary
paths, for execution. Start with owner-registered directories. Unscoped work gets
a designated workspace on an explicit default host.

Select host by explicit request first, then a continuation's recorded host,
then the project's registered host. For replicated projects, select only among
explicitly registered replicas with compatible profiles and permitted accounts.
Queue or report an unavailable host. Do not silently move work to another machine:
its checkout, uncommitted changes, credentials, and thread history may differ.
There is no automatic file synchronization or cross-machine thread migration in
the first release. An explicit host that conflicts with a continuation's saved
host requires a new task or a future explicit migration operation.

Store a separate conversation record mapping the owner, host, project, Brainslug
task name, and Codex thread ID. An Alexa session ID may point to an active
conversation, but it is not the durable conversation ID.

Selection rules:

1. Honor an explicit project or thread reference after authorization checks.
2. Continue an unambiguous active conversation for a follow-up such as "add tests
   to that change."
3. Use recent thread summaries only to shortlist candidates. A shared project
   alone is insufficient evidence that two requests belong in the same thread.
4. Start a new thread for a new objective or an explicit "new task."
5. Ask a short clarification when several destinations fit. Persist it so a
   later Alexa session can answer.

For the first release, resume only threads created by Brainslug or explicitly
imported by the owner. Verify project, ownership, archive state, and runtime
availability on each continuation. Missing threads produce a recovery choice,
not silent replacement conversations.

Allow only one active turn per thread and one writing job per checkout. Queue
normal follow-ups; add active-turn steering later when explicitly requested.
If desktop and Brainslug cannot safely share a live thread, use a supported
handoff or require it to be idle. Separate worktrees are a later option for
parallel editing, with an explicit lifecycle and cleanup policy.

## Codex Runtime Contract

Each worker launches a pinned, supported executable as a hidden child process
using argument arrays, with JSON over stdio and redacted diagnostic output.
Supervise it from the worker lifecycle, not the gateway HTTP handler. Do not
expose a public Codex control listener. Validate these protocol operations against the installed
schema during development:

| Operation | Protocol |
| --- | --- |
| Connect | `initialize`, followed by `initialized` |
| Discover choices | `model/list` |
| New conversation | `thread/start` with the resolved project directory |
| Continue conversation | `thread/resume` with the saved ID |
| Execute | `turn/start` with `model` and `effort` |
| Observe | Item events and `turn/completed` |
| Recover | `thread/read` and supported listing operations |
| Cancel | `turn/interrupt` |

Handle server requests for approval as well as notifications and replies.
An accepted turn is not a completed task. Final status determines success.
Pass the validated model and effort on every turn so prior overrides do not
accidentally determine a later job. [App-server protocol](https://learn.chatgpt.com/docs/app-server)

Workers advertise protocol version, Codex version, eligible project IDs,
model/effort catalog, and supported operations. Reject incompatible versions
before accepting work for that host. Keep credentials on the execution host;
approvals and credential expiry return structured states to the controller.

Use native Windows workers first, with OS-specific process handling isolated
for later platforms. The Brainslug server or controller can run elsewhere,
including in a container, without mounting worker repositories or credentials.

## Durable Jobs And Recovery

Add application operations such as `SubmitRequest`, `GetJob`, `GetLatestJob`,
`ReplyToClarification`, and `CancelJob`. Submission returns structured
`completed`, `pending`, `needs_input`, or `failed` state for adapters to present.

Persist controller acceptance before routing or execution spends resources. Use a unique
key over principal, ingress source, and verified adapter request ID. Store a
payload hash so a reused key with different content is rejected. Duplicate
delivery returns the original job rather than starting another worker.

Proposed states:

```text
accepted -> routing -> queued -> dispatching -> running -> completed
                |                               |
                +-> awaiting_input              +-> awaiting_approval

Nonterminal states may fail or be canceled.
Uncertain execution after a crash -> needs_attention.
```

Persist principal, prompt, routing decision and policy version, host/project/thread
IDs, turn ID, attempt ID, worker lease, timestamps, usage, final result, and
redacted errors. Keep a bounded event history. Snapshot settings at acceptance;
recheck authorization before dispatch so permission revocation takes effect.

Brainslug acknowledges saved work only after controller acceptance, within the
existing response budget. Routing can continue after that acknowledgement.
If the controller is unavailable, say the request could not be queued; do not
claim acceptance or add a second independent local queue. Retry a lost acceptance
reply using the same idempotency key. Workers use independent deadlines.

Persist a dispatch attempt ID before transmission. Each worker records accepted
attempts and returns the existing execution handle for duplicate delivery.
This deduplicates transport retries but cannot make local Codex dispatch atomic.
Workers retain bounded event/result spools until the controller acknowledges them.

On restart, reconcile dispatched work with Codex before retrying. A crash after
`turn/start` but before saving its reply can leave uncertain execution. A local
attempt ID or protocol message ID is not evidence of server-side deduplication.
Do not replay a potentially mutating turn automatically when reconciliation is
inconclusive. Persist `needs_attention` and retain the original conversation.
Interrupted work may already have changed files; cancellation is not rollback.

During a LAN partition, an already-running worker may finish within its original
deadline and spool the result. It must not accept new authority or extend limits
while disconnected. Approval-required actions wait and expire without approval.
The controller reports last-contact time and uncertain status, not failure or
completion inferred from silence. A cancel request remains `cancel_requested`
until acknowledged or reconciled. Do not reassign an in-flight task because its
heartbeat or controller lease expired.

Operational persistence is required even when ordinary answer history is off.
Document short-lived prompt/result retention and purge completed operational
records under policy. Codex's own conversation storage needs a separate retention
decision; deleting a Brainslug row does not delete that history.

## Identity, Authentication, And Permissions

Enroll the Alexa principal for Codex control through the authenticated owner
dashboard, then assign allowed projects. Signed ASK delivery and a valid skill
ID do not establish which local repositories that principal may control. Keep
status, results, clarification, and cancellation scoped to the same owner.
Alexa account identity also does not prove which person spoke on a shared Echo.

Select a supported Codex sign-in or API credential path on each worker during setup. Do not
copy desktop tokens, depend on private credential files, or assume Brainslug's
existing ChatGPT connector can supply interchangeable tokens. Evaluate the
documented Sign in with ChatGPT integration independently; it requires the
application to manage its own token refresh and app-server lifecycle.
[Authentication integration](https://developers.openai.com/siwc/token-sharing-open-source/codex-app-server)

The gateway forwards its verified principal using its enrolled service identity;
the controller authorizes that principal, and workers accept only scoped controller
operations. Do not accept caller-supplied ownership fields from public HTTP input.

Start the pilot with read-only tasks, then permit bounded repository edits under
registered project policies. The router cannot expand those permissions. Handle
approval requests in the authenticated dashboard, bound to the exact pending
host/job/turn/action, with expiration and no approval while disconnected. Later
voice approval support must have deliberate identity and action-binding semantics.

Preauthorized ordinary work should proceed without repeated confirmation. Require
separate authority for actions such as publishing or deleting data when the
project policy does not already grant it. Preserve existing management-session,
redaction, and listener-isolation mechanisms.

## Delivery Sequence And Acceptance Gates

### 0. Prove The Integration Boundary

Build a disposable Go app-server client against a test workspace. Verify login,
catalog discovery, an explicit model/effort pair, a new thread, continuation,
cancellation, and reconnection. Use the installed schema, then pin the release
chosen for implementation; the currently installed alpha is not a compatibility
guarantee. Prove the gateway/controller/worker topology across at least two LAN
machines, including an authenticated connection and a rejected unknown client.

Test whether those threads appear in an existing desktop project, survive app
restart, and can be continued without concurrent ownership problems. Record
exact supported behavior. If desktop placement is unavailable, label the MVP
as Brainslug-managed Codex conversations and track the desktop bridge as an
unresolved requirement. Do not claim full desktop control from a successful
directory-based run.

### 1. Build The Separate Binary And Durable Submission Layer

Add controller/worker entry points, the authenticated task API, separate database,
append-only migrations, idempotent acceptance, queue limits, worker leases, deadlines,
job status/result reads, cancellation, and recovery. Implement against a fake
runtime first. Preserve the current direct-answer path in manual mode.

Gate: duplicate requests create one job; restarting Brainslug leaves controller
jobs intact; disconnecting Alexa does not cancel accepted work; controller and
worker restart do not blindly repeat an uncertain operation.

### 2. Add Explicit Codex Control In The Dashboard

Register hosts and their projects, choose a model/effort pair, start a thread,
continue it, inspect progress, respond to approval, and cancel. Add authenticated
local endpoints as needed; keep all project and thread authorization in the
application layer.

Gate: tasks reach two distinct execution machines; a two-turn test stays on its
selected host/project with the saved thread ID, correct profile, and durable
result. An unauthorized host or project is rejected.

### 3. Add And Evaluate The Router

Add structured decisions, host/project/thread retrieval, profile validation, and
manual overrides. Begin in shadow mode: record proposed routes while the explicit
selection remains authoritative. Evaluate normal questions, repository tasks,
ambiguous project names, follow-ups, model overrides, and hostile instructions.

Gate: no invalid destination or model/effort pair is dispatched; ambiguity becomes
clarification. Set a routing accuracy target on labeled examples before enabling
automatic execution, and measure router latency and usage separately.

### 4. Connect The Voice Workflow

Extend ASK intents and application mappings for task submission, status, result,
continuation, clarification, and cancellation. Speak saved acceptance first;
do not claim a worker has started when it is still queued.

Example behavior:

| Utterance | Intended outcome |
| --- | --- |
| "Ask Codex to inspect startup in Brainslug." | New task in the registered project; short acknowledgement. |
| "Use my desktop for a new task in Brainslug." | Resolve the registered desktop host and project before dispatch. |
| "What is the status of the startup task?" | Read persisted state without a routing model call. |
| "Tell that task to add tests." | Continue the identified thread, or ask which task if ambiguous. |
| "Start a new task in Brainslug using deep reasoning." | New thread with an allowed explicit profile. |
| "Cancel the startup task." | Request cancellation and report its confirmed state. |

Gate: a real Echo round trip acknowledges within the existing seven-second
budget, survives the voice session ending, and retrieves the correct later
result. Generic "stop" should end the voice session; canceling durable work
must identify the task. Do not promise unsolicited completion announcements in
the initial release.

### 5. Finish Desktop Placement And LAN Operations

Ship verified desktop project placement or clearly surface its limitation.
Add owner-approved import of existing threads, retention controls, routing
explanations, usage visibility, host availability, credential rotation, worker
reconnection, and supported upgrade checks. Add parallel worktrees and explicit
cross-machine migration only after independent host execution is reliable.

## Verification

Use fake routing/runtime ports for application tests, a fake JSON protocol
process for adapter tests, temporary SQLite databases for migrations and crash
recovery, and sanitized ASK fixtures for voice behavior. Exercise invalid routes,
cross-principal access, model unavailability, approval expiry, process death,
duplicate delivery, cancel races, and a lost dispatch acknowledgement.
Also test controller unavailability before acceptance, LAN loss during execution,
result replay after reconnect, revoked host credentials, incompatible protocol
versions, same-named projects on different machines, and host-local model access.

Run the repository build, vet, tests, and lint checks for implementation changes.
Keep live Codex and physical Echo checks opt-in, with a disposable project and
known credentials. Local verification uses fake inference and disposable state.
Operational live checks remain explicit opt-in release evidence.
