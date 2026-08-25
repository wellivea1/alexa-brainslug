# Architecture

## Status

This document defines the target architecture and the migration path from the current working skeleton. It does not claim that the target packages or interfaces already exist.

Current implementation:

```text
ASK HTTP request
  -> internal/alexa.Handler
  -> providers.Provider
  -> store.InteractionStore
  -> Alexa response
```

Target implementation:

```text
inbound adapter
  -> application service
  -> outbound provider and persistence adapters
```

The reason for the change is practical, not theoretical. Alexa is the current interface, but it should not own provider policy, job state, persistence, or identity semantics. Those responsibilities must survive if a browser, a private API, or an approved MCP adapter is added later.

## Design Goals

1. Keep the working ASK custom skill as the first supported surface.
2. Move use-case logic out of Alexa protocol handlers.
3. Make slow model work an explicit job rather than an accidental HTTP timeout.
4. Keep provider and model selection explicit.
5. Keep prompts, answers, identities, and credentials private by default.
6. Allow another adapter to reuse application operations without importing Alexa types.
7. Avoid building a generic plugin framework before there are multiple real capabilities.
8. Treat Alexa+ MCP as a gated future integration, not as a reason to redesign everything around MCP today.

## Non-Goals

- A microservice split.
- A dependency-injection framework.
- A dynamic tool registry in the first refactor.
- A generic `chat(prompt)` MCP endpoint.
- Continuous audio, wake-word handling, or direct Echo control.
- Silent provider or model fallback.
- Public multi-user hosting in the initial phases.

## Target Dependency Direction

```text
cmd/brainslug
  -> inbound adapters
  -> internal/app
  -> application-owned ports

outbound adapters
  -> implement application-owned ports
```

More concretely:

```text
internal/alexa  ------+
                      |
internal/web    ------+----> internal/app ----> provider port
                      |                    ----> repository port
future internal/mcp --+

internal/providers implements provider port
internal/store     implements repository port
```

Rules:

- `internal/app` must not import Alexa request or response types.
- `internal/app` must not import MCP protocol types.
- `internal/alexa` must not call provider clients directly after the extraction phase.
- `internal/alexa` must not construct database records directly after the extraction phase.
- Provider packages do not decide fallback, retry, quota, retention, or voice policy.
- Store packages persist state but do not decide who is allowed to see it.
- The process wiring in `cmd/brainslug` selects concrete implementations.

## Application Service

Create a small transport-neutral package, provisionally `internal/app`. It owns the use cases the adapters need. Exact Go names may change, but the responsibilities may not drift back into adapters.

Initial operations:

```text
SubmitQuestion
GetQuestionStatus
GetLatestCompletedAnswer
GetAnswerByID
ListHistoryForPrincipal
```

Only the first three are required for the ASK flow. The others support the local dashboard and future adapters without exposing storage details.

The first extraction should not add a generic `InvokeTool` abstraction. Explicit methods are easier to understand, test, and authorize. A capability abstraction can be added later only when two or more actual adapters need it.

### Input model

A question submission needs platform-neutral data:

```text
Principal       stable internal caller identity
ConversationID  adapter-provided session or conversation correlation
RequestID       adapter request identifier for idempotency
Prompt          normalized user question
DeliveryMode    immediate-preferred or deferred-allowed
Surface         ask, web, private-api, or future-mcp
```

The application layer may also receive provider policy selected by configuration, such as model, token budget, reasoning effort, and deadline. Alexa protocol fields do not belong in this request.

### Output model

The result must distinguish completion from acceptance:

```text
Completed
  - answer ID
  - spoken answer
  - full answer
  - provider and model actually used
  - latency

Pending
  - job ID
  - accepted timestamp
  - current status

Failed
  - stable failure category
  - operator-safe diagnostic for local storage
  - user-safe message selected by the inbound adapter
```

The application layer returns stable failure categories. The Alexa adapter decides the exact spoken wording. A future MCP adapter returns structured error data. Neither should inspect provider-specific error strings.

## Principal And Identity Model

Do not use a raw Alexa user ID as the application's universal identity type.

The inbound adapter maps its platform identity to an internal principal:

```text
Principal {
  Namespace: "alexa"
  Subject:   stable derived identifier
}
```

A future MCP adapter would use a separate namespace and the authenticated OAuth subject. The internal identifier may be a keyed hash or another one-way derivation suitable for local scoping.

Requirements:

- Raw Alexa user IDs are not sent to model providers.
- Raw MCP OAuth subjects are not sent to model providers.
- History queries always require a principal.
- A principal from one namespace cannot read another namespace's data by accidental string collision.
- Public distribution requires a deliberate account-linking or identity-merging design. Do not infer that two platform accounts belong to the same person.

## Immediate And Deferred Execution

### ASK request budget

A classic custom skill must return inside Alexa's short request budget. The application service therefore accepts a voice deadline shorter than Alexa's external timeout.

Recommended flow:

```text
1. ASK adapter validates the request.
2. ASK adapter calls SubmitQuestion with a bounded immediate deadline.
3. Application starts or reuses an idempotent job.
4. If the provider completes before the deadline, return Completed.
5. If not, preserve the job and return Pending before Alexa times out.
6. ASK adapter speaks an acknowledgement.
7. The provider work continues under a separate configured job deadline.
8. A later `last response` or status request reads the stored result.
```

The Alexa HTTP request context must not be the lifetime of a deferred provider job. Canceling the request after the voice response must not cancel accepted background work.

### Job state

Use an explicit state machine:

```text
accepted -> running -> completed
                    -> failed
                    -> canceled
```

A retry creates a new attempt or follows a documented retry policy. It must not silently rewrite a completed answer.

Minimum stored fields:

- Job ID.
- Principal ID.
- Adapter surface.
- Adapter request ID.
- Conversation or session ID where available.
- Prompt.
- Status.
- Provider and requested model.
- Actual model.
- Created, started, and completed timestamps.
- Spoken and full answer.
- Redacted failure category and diagnostic.
- Latency and token-budget configuration needed for diagnosis.

Do not overload the existing interaction row until it becomes ambiguous. Either extend it through an append-only migration with explicit job fields or add a dedicated jobs table. The schema decision should be made during the deferred-work phase, not during the first behavior-preserving extraction.

### Idempotency

Alexa or a proxy may retry a request. `SubmitQuestion` must accept an adapter request ID and avoid spending twice for the same authenticated request.

Idempotency scope is:

```text
surface + principal + request ID
```

A duplicate request returns the existing job or completed answer.

## Provider Boundary

The existing `providers.Provider` interface is close to a usable outbound port. The main change is who calls it.

The application service owns:

- Provider selection from validated configuration.
- Model and reasoning policy.
- Immediate and background deadlines.
- Retry policy, if one is ever added.
- Mapping provider failures to stable application failure categories.
- Persistence of accepted, completed, and failed work.

Provider implementations own:

- Provider-specific HTTP request and response formats.
- Authentication headers.
- Parsing provider model and output fields.
- Provider-specific error classification where needed.
- Respecting context cancellation and deadline.

Provider implementations must not:

- Fall back to another provider or model.
- store history.
- emit Alexa speech.
- know whether the caller is Alexa or MCP.

## Persistence Boundary

The application package should define the repository interfaces it needs. `internal/store` implements them with SQLite.

Recommended split:

```text
QuestionRepository
  SaveAccepted
  MarkRunning
  MarkCompleted
  MarkFailed
  GetByID
  GetByIdempotencyKey
  GetLatestCompletedForPrincipal

HistoryRepository
  ListForPrincipal
  DeleteExpired
```

These may be one Go interface initially if that keeps the implementation smaller. The important constraint is that the application package owns the vocabulary and `internal/store` implements it.

All schema changes remain versioned, transactional, and append-only.

## ASK Adapter Responsibilities

`internal/alexa` remains the current inbound adapter. It owns:

- Alexa request signature and certificate verification.
- Timestamp freshness and allowed skill ID checks.
- Alexa request JSON parsing.
- Intent and slot extraction.
- Mapping Alexa user and session identifiers to application values.
- Dialog directives and slot elicitation.
- Mapping application results to concise speech and ASK response JSON.
- ASK-specific fallback, help, stop, cancel, and reprompt behavior.

It does not own:

- Provider request construction.
- System prompt selection.
- Persistence records.
- Background job lifecycle.
- Cross-surface identity policy.
- Generic capability discovery.

## Local Web Adapter Responsibilities

The dashboard is an administrative adapter, not a product surface.

It should:

- Bind only to a private or loopback listener.
- Query application read operations rather than reading SQLite tables directly where practical.
- Scope data to the configured owner or require local authentication if remote administration is ever added.
- Render stored text safely.

It should not share the public ASK listener after the listener-split phase.

## Future MCP Adapter Responsibilities

No MCP package should be created until the gates in `mcp-roadmap.md` are satisfied.

When it exists, the adapter owns:

- Streamable HTTP and MCP protocol handling.
- Service and user authentication required by the target MCP host.
- Mapping authenticated subjects to internal principals.
- Tool schemas, descriptions, resources, and optional UI metadata.
- Mapping structured application results to MCP results.
- Adapter-specific latency and cancellation handling.

It does not own:

- Provider calls.
- job persistence.
- authorization policy that should be shared across adapters.
- a generic alternate-assistant personality.

An Alexa+ production profile must expose only capabilities that are distinct from a generic LLM relay and can satisfy Amazon's certification requirements.

## Package Migration

### Step 1: Introduce application types and interfaces

Add `internal/app` with request, result, principal, and failure types. Add a service with dependencies expressed as narrow interfaces.

No routing or behavior changes in this step.

### Step 2: Move the ask use case

Move provider invocation, prompt selection, error categorization, answer shaping coordination, and persistence out of `internal/alexa.Handler` into the application service.

The Alexa handler should call one application method and map the result to speech.

### Step 3: Move latest-answer lookup

Move `GetLatestResponse` semantics into the application service. Alexa supplies the principal and asks for the latest completed answer.

### Step 4: Rewire the dashboard

Route dashboard reads through application queries or a read service. Do not expose new public routes.

### Step 5: Add safe ingress

Complete ASK verification and split public and administrative listeners before continuous external use.

### Step 6: Add deferred jobs

Add job state, idempotency, bounded immediate execution, background completion, and status retrieval.

### Step 7: Add capability-specific adapters only when justified

Do not create MCP scaffolding as a placeholder. Add it when there is an approved host and a concrete typed capability set.

## Testing Strategy

Application tests must run without Alexa JSON, a live provider, or a real database.

Required test layers:

- Application service with fake provider and repository implementations.
- Alexa adapter with fake application service.
- Provider clients with local `httptest` servers.
- SQLite repository integration tests with temporary databases.
- End-to-end HTTP tests that use fixture ASK requests after verification is implemented.
- Deferred-job tests covering completion before deadline, pending return, background completion, duplicate requests, provider failure, and shutdown.

A test for the application core should not need to know the name `AskGPTIntent`. A test for the Alexa adapter should not need to know the OpenAI response format.

## Shutdown And Recovery

Accepted background jobs introduce process-lifecycle requirements:

- Stop accepting new work on shutdown.
- Allow in-flight immediate requests a bounded drain period.
- Persist job state before starting provider work.
- Mark interrupted jobs in a recoverable state.
- On startup, either resume eligible work or mark it failed with an explicit reason. Do not leave permanent `running` rows.
- Do not spawn unbounded goroutines. Use a bounded worker queue or equivalent concurrency control.

Single-owner deployment does not excuse losing paid work or duplicating provider charges.

## Architecture Acceptance Criteria

The application-core extraction is complete when:

- `internal/alexa` no longer imports `internal/providers`, `internal/prompts`, or concrete store record types.
- Provider and persistence behavior is covered by application-service tests that do not construct Alexa envelopes.
- Alexa adapter tests use a fake application interface.
- Current launch, ask, last response, help, fallback, stop, cancel, and repeat behavior remains intact.
- No MCP code or generic plugin framework was added as part of the extraction.

The deferred-work architecture is complete when:

- A slow provider call produces a pending acknowledgement before the ASK deadline.
- The job completes under an independent deadline.
- Duplicate Alexa requests do not create duplicate provider charges.
- `last response` reads the latest completed answer for the authenticated principal.
- Shutdown does not strand jobs in an unexplained state.
