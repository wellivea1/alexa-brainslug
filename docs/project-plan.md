# Project Plan

## Goal

Build a reliable private voice path from Alexa to an explicitly selected AI model while keeping Alexa as an adapter rather than the application architecture.

The current product is a classic ASK custom skill. The target is:

```text
ASK adapter
  -> transport-neutral application service
  -> explicit provider and SQLite adapters
```

A future MCP adapter is optional and gated. It must not delay the ASK path, and it must not be designed as a generic alternate-assistant relay.

## Product Position

Brainslug exists for questions where deterministic provider and model choice matters more than Alexa's default answer path.

Current intended user:

- One owner.
- Private Alexa account.
- Self-hosted service.
- Local history.
- Explicit provider and model configuration.

Not currently planned as a product surface:

- Public Skill Store availability.
- Shared household history without explicit identity rules.
- An open web API.
- A public Alexa+ add-on.
- External control of Alexa through MCP.

Store publication and Alexa+ distribution are separate later decisions. They require different hosting, privacy, metering, identity, and certification work.

## Current State

Implemented:

- Go HTTP server with `/health`, `/alexa`, `/history`, and `/`.
- Alexa request routing for launch, ask, last response, help, fallback, stop, cancel, repeat, and session end.
- OpenAI Responses API client.
- Anthropic Messages API client.
- Explicit provider and model selection.
- SQLite history and transactional migrations.
- Local dashboard.
- Docker Compose and path-scoped Caddy example.
- Secret-file support and provider-error redaction.
- Basic tests, linting, CI, race testing, and container startup testing.

Current architectural coupling:

- `internal/alexa.Handler` invokes the provider directly.
- The Alexa handler selects prompts and constructs persistence records.
- The Alexa request context is also the provider-call context.
- Latest-answer semantics are implemented inside the Alexa adapter.

Known blocking gaps:

- Request verification is a fail-closed placeholder. Verification on rejects Alexa; verification off exposes a paid provider route.
- ASK has a short response deadline, while provider timeouts default to 12 seconds.
- Slow work has no durable job state or idempotency.
- Sample utterances are awkward and slot elicitation is absent.
- Public and administrative routes share one listener.
- No application rate limit, spend cap, retention policy, or complete multi-user scoping.
- Current token defaults can be consumed by model reasoning before any visible answer is produced.

## Ordering Principle

The previous plan put deferred answers ahead of request verification because slow answers are the product's purpose. That ordering is wrong for actual operation. Without verification there is no configuration that is both usable and safe to leave online.

The corrected order is:

1. Prove the real ASK round trip briefly.
2. Extract the application boundary without changing behavior.
3. Make ASK ingress safe.
4. Make slow model use usable.
5. Complete identity, retention, and operational controls.
6. Add reusable capabilities only when they are real.
7. Evaluate MCP only after access and product gates exist.

## Architecture Principles

- ASK is the current inbound adapter, not the core.
- Application use cases own provider execution, job state, identity scoping, and persistence policy.
- Provider clients are thin outbound adapters and never choose fallback policy.
- SQLite persists application state but does not decide authorization.
- Platform request types do not cross into the application package.
- Model and provider choices remain explicit and visible.
- Security-critical incomplete work fails closed.
- Structural isolation is preferred over warnings.
- Slow work is represented as a job, not as a longer HTTP timeout.
- Do not build a dynamic tool system before multiple concrete capabilities exist.
- MCP is an adapter option, not the internal architecture.

See `architecture.md`.

## Phase 0: Prove The Real ASK Round Trip

Purpose: validate the one assumption every later phase depends on.

Procedure:

- Create or use the private custom skill.
- Point it at a short-lived tunnel.
- Set `ALEXA_ALLOWED_SKILL_ID`.
- Temporarily disable verification only for this controlled test.
- Invoke the skill from the actual Echo.
- Tear down the public route immediately after the test.

Exit criteria:

- A real device invocation reaches the local service.
- The request envelope and skill ID are captured as sanitized fixtures.
- The working invocation phrase is recorded.
- The tunnel is removed or disabled after the test.
- No provider credential or raw user ID is committed in a fixture.

Status: not started.

## Phase 1: Extract The Application Core

Purpose: separate the product use cases from ASK before adding asynchronous behavior or another adapter.

Required changes:

- Add a transport-neutral application package, provisionally `internal/app`.
- Define application request, result, principal, and stable error types.
- Move provider invocation, prompt selection, result normalization, and persistence orchestration out of `internal/alexa`.
- Move latest-completed-answer lookup into the application service.
- Keep existing provider and store implementations as concrete dependencies.
- Rewire Alexa through a narrow application interface.

Explicit exclusions:

- No MCP package.
- No generic capability registry.
- No public API.
- No background worker yet.
- No schema change unless required to preserve current behavior.

Exit criteria:

- `internal/alexa` no longer imports provider implementations, prompt policy, or concrete store records.
- Alexa tests use a fake application service.
- Application tests use fake provider and repository ports and no Alexa JSON.
- Existing voice behavior and history behavior remain unchanged.
- CI remains green.

Status: not started.

## Phase 2: Make ASK Safe To Expose

Purpose: remove the current security blocker before continuous real-device use.

Required changes:

- Implement Alexa certificate URL validation.
- Validate the certificate chain and Alexa signing constraints.
- Verify the exact raw request body signature.
- Enforce timestamp freshness.
- Enforce `ALEXA_ALLOWED_SKILL_ID`.
- Return HTTP 400 for failed verification without disclosing the failed check.
- Split public ASK traffic from administrative traffic.
- Bind dashboard, history, and health to a loopback-only administrative listener.
- Add request rate limits and a global provider-spend or request cap.

Exit criteria:

- `ALEXA_VERIFY_REQUESTS=true` works with the Alexa Developer Console and a real Echo.
- Verification tests cover a valid fixture, tampered body, stale timestamp, wrong certificate host, invalid chain, and wrong skill ID.
- The public listener exposes only the ASK route.
- Public requests to `/`, `/history`, and `/health` cannot reach the administrative server even if a proxy rule is broad.
- Rate limits are tested and observable locally.
- The service can be left reachable without becoming an unauthenticated paid API proxy.

Status: not started.

## Phase 3: Make The ASK Experience Usable

Purpose: make the project work for the slow, reasoning-heavy questions it was built for.

### Invocation work

- Replace unnatural sample utterances with carrier phrases that read correctly after `ask Brain Slug`.
- Add dialog slot elicitation for the two-turn flow.
- Keep explicit invocation as the supported baseline.
- Do not depend on name-free routing for generic questions.

### Deferred work

- Add explicit accepted, running, completed, failed, and canceled job states.
- Add adapter request IDs and idempotency.
- Use a voice deadline shorter than Alexa's external request timeout.
- Return a completed answer when the provider finishes inside the voice deadline.
- Otherwise return a pending acknowledgement and continue under an independent job deadline.
- Add `last response` and status behavior for pending, completed, and failed jobs.
- Use bounded worker concurrency and defined shutdown recovery.

### Provider policy

- Make reasoning or thinking effort configurable per provider where supported.
- Set output-token defaults that leave room for both reasoning and visible text.
- Keep provider and model selection explicit.
- Keep provider errors categorized without speaking internal diagnostics.

Exit criteria:

- The one-shot phrases in `invocation-design.md` build and work on a real device.
- `Alexa, open Brain Slug` accepts an unconstrained follow-up question through slot elicitation.
- Fast requests answer in the same turn.
- Slow requests return a pending acknowledgement before Alexa times out.
- Completed deferred work is available through `last response`.
- A duplicate authenticated ASK request does not trigger a duplicate provider charge.
- Process shutdown does not leave unexplained permanent `running` jobs.

Status: not started.

## Phase 4: Complete Identity, History, And Retention

Purpose: make the local data model honest before more than one account or adapter can use it.

Required changes:

- Introduce namespaced internal principals rather than treating a raw Alexa ID as the universal key.
- Derive the stored principal identifier without exposing raw platform identifiers to providers.
- Scope every history and job read by principal.
- Add retention by age and maximum count.
- Add explicit deletion operations.
- Decide whether full prompts and answers are stored by default.
- Keep redacted errors separate from user-visible failure categories.
- Add per-principal quotas if more than one user is enabled.

Exit criteria:

- No history or job query can omit a principal accidentally.
- Alexa users cannot read each other's history in tests.
- Retention runs automatically and is testable with a fake clock.
- Raw platform identifiers are absent from provider requests and logs.
- The dashboard clearly states which principal scope it is showing.

Status: not started.

## Phase 5: Define Reusable Capabilities

Purpose: expose useful operations without prematurely turning the application into a generic plugin framework.

Start only after the application core and deferred jobs exist.

Candidate application capabilities may include:

- Submit an analysis job.
- Read the status of the caller's job.
- Retrieve a completed saved answer.
- Search the caller's saved answers.
- Delete the caller's saved answer or history range.

These are internal application operations first. They are not automatically MCP tools and do not imply Alexa+ approval.

Rules:

- Each capability has typed input and output.
- Each capability declares whether it reads or mutates state.
- Authorization is enforced in the application layer.
- Long-running work returns accepted state rather than blocking an adapter indefinitely.
- No capability silently selects a different provider or model.
- No generic dynamic `Invoke(name, map[string]any)` entry point unless concrete adapters demonstrate a real need.

Exit criteria:

- ASK and the local administrative surface reuse the same application operations where appropriate.
- Capability tests contain no ASK or MCP protocol types.
- Authorization and job ownership are tested at the application boundary.

Status: deferred until Phases 1 through 4 are complete.

## Phase 6: Evaluate An MCP Adapter

Purpose: add an MCP surface only when it is technically and commercially real.

Entry gates:

- Amazon has granted relevant Alexa+ builder access, or another concrete MCP client is the actual target.
- The target host's protocol version and authentication profile are known.
- There is a capability set with value beyond forwarding arbitrary prompts to an LLM.
- The service can satisfy the host's latency requirements.
- Voice-only behavior exists for every Alexa+ capability.
- Policy and certification requirements have been reviewed against the exact tool set.

Implementation rules:

- Add MCP as an inbound adapter over application operations.
- Keep ASK and MCP authentication separate.
- Map the authenticated OAuth subject to a namespaced principal.
- Expose narrow tools with typed schemas.
- Return structured results and stable errors.
- Do not expose the raw provider interface.
- Do not publish a production Alexa+ profile centered on `chat(prompt)` or `ask_model(prompt)`.
- Do not assume MCP provides external control of Alexa.

Latency rule:

- Fast reads and deterministic operations may complete synchronously.
- Slow model work must return accepted state quickly and use a separate status or retrieval operation if the host experience supports it.
- Do not block an Alexa+ MCP request on a frontier-model response and hope it stays under Amazon's documented latency target.

Exit criteria:

- MCP protocol tests pass against the target client or official tooling.
- Service and user authentication are both tested where required.
- Tool discovery exposes only approved capabilities.
- Every tool enforces principal scoping.
- No Alexa protocol type appears in the MCP adapter and no MCP type appears in the application package.
- The exact production tool set has a written certification rationale.

Status: gated; do not start speculatively.

## Phase 7: Decide Distribution

Private use can stop before this phase.

Public ASK or Alexa+ distribution requires:

- Production hosting outside a home connection.
- Privacy policy and data-use disclosures.
- Per-user metering and quotas.
- Abuse controls and global spend limits.
- Account deletion and data export or deletion behavior.
- Availability monitoring.
- Certification-ready invocation, content, and failure handling.
- A decision about whether user prompts and model outputs are stored at all.

Exit criteria are intentionally not specified until distribution is chosen. Building all of this for a private single-owner skill would be wasted work.

## Engineering Standards

The service accepts authenticated internet requests, spends money, and stores private text. The following remain requirements:

- Configuration is explicit and validated at startup.
- Errors wrap with `%w`; callers match stable sentinels or typed errors.
- Credentials and raw platform identifiers never reach logs or history.
- Schema migrations are append-only and transactional.
- Provider clients use local HTTP test servers.
- Application tests use fakes, not live APIs.
- ASK adapter tests use request fixtures and a fake application service.
- Shutdown is bounded and does not skip cleanup.
- CI enforces build, vet, race tests, linting, formatting, ASCII, and container startup.

See `../CONTRIBUTING.md` and `progress-checks.md`.

## Dropped Or Rejected Approaches

### Generic OpenCode provider

Dropped. It adds another path to a general model response without adding a distinct product capability and cannot fit a normal voice-turn budget reliably.

### Alexa+ MCP first

Rejected. Access is partner-gated, the latency budget is stricter than ASK, and Amazon policy rejects a generic third-party LLM relay. It is not a substitute for making the current ASK skill work.

### One universal protocol handler

Rejected. ASK and MCP have different authentication, deadlines, conversation ownership, and response models. They should share application use cases, not protocol middleware.

### Longer ASK timeout

Rejected. The external deadline is controlled by Alexa. Slow work needs deferred execution, not a larger local timeout.
