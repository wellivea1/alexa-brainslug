# Architecture

## Status

The adapter-based foundation is implemented.

Current runtime:

```text
Alexa cloud
  -> public ASK listener
  -> ASK request verifier
  -> internal/alexa adapter
  -> internal/app service
  -> provider port -> internal/providers adapter
  -> repository port -> internal/store adapter -> SQLite

loopback administrative listener
  -> health
  -> dashboard
  -> history JSON
```

The application core, provider adapter, repository adapter, namespaced principal type, request verifier, dialog response support, and listener split exist in code.

The durable deferred-job layer described later in this document does not exist yet. The current `Ask` operation is synchronous and uses the Alexa HTTP request context.

## Design Goals

1. Keep the private ASK custom skill as the first supported surface.
2. Keep provider execution and persistence policy outside Alexa protocol handlers.
3. Make security boundaries structural where possible.
4. Preserve explicit provider and model selection.
5. Keep prompts, answers, identities, and credentials private by default.
6. Permit another authenticated adapter to reuse application operations without importing ASK types.
7. Add durable jobs before attempting long reasoning work through a short voice request.
8. Keep Alexa+ MCP gated until there is access and a certifiable capability set.

## Non-Goals

- A microservice split.
- A dependency-injection framework.
- A dynamic plugin or tool registry.
- A public generic `chat(prompt)` MCP endpoint.
- Continuous audio or direct Echo control.
- Silent provider or model fallback.
- Public multi-user hosting in the current phase.

## Dependency Direction

```text
cmd/brainslug
  -> inbound adapters
  -> internal/app
  -> application-owned ports

internal/providers
  -> implements the application provider port

internal/store
  -> implements the application repository port
```

Rules enforced by the current code:

- `internal/app` contains no ASK request or response types.
- `internal/alexa` does not import provider implementations, prompt policy, or concrete store records.
- Provider implementations do not select fallback providers or persist history.
- The SQLite adapter maps application records to the legacy interactions schema.
- Process wiring in `cmd/brainslug` selects concrete adapters.

A future MCP adapter must follow the same direction and must not put MCP types into `internal/app`.

## Application Service

`internal/app.Service` currently owns two use cases:

```text
Ask
LatestAnswer
```

### Ask

Input:

```text
AskRequest
  Principal
  ConversationID
  Prompt
```

The service:

1. validates the prompt and principal;
2. applies configured model and output-token policy;
3. sends a provider-neutral request through the provider port;
4. normalizes provider failures into stable application categories;
5. redacts private diagnostics before persistence;
6. stores a success or failure when history is enabled;
7. returns a transport-neutral answer.

The ASK adapter decides the exact spoken wording for success and failure.

### LatestAnswer

The service reads the latest completed saved answer for the supplied principal. It distinguishes:

- no history;
- an empty saved answer;
- repository failure;
- a completed answer.

The current SQLite query returns the latest successful nonempty interaction. It does not know about pending jobs because job state does not exist yet.

## Principal Model

Application operations use:

```text
Principal
  Namespace
  Subject
```

The ASK adapter creates a principal in the `alexa` namespace. Missing Alexa identity is rejected rather than being assigned to a shared anonymous bucket.

The SQLite adapter preserves the original unprefixed Alexa storage key so existing history remains readable. Other future namespaces are prefixed. This is a compatibility bridge, not the final identity design.

The provider adapter derives a namespaced principal key. Provider clients hash it before placing it in a provider end-user field where supported. Raw Alexa request types do not cross the adapter boundary.

Future work:

- migrate every stored principal to an explicit namespace;
- add retention and deletion operations;
- define account linking rather than inferring cross-platform identity.

## Provider Boundary

The application provider port uses provider-neutral request and response types.

The provider adapter maps them to the existing provider package:

```text
app.ProviderRequest
  -> providers.Request
  -> OpenAI or Anthropic HTTP client
  -> providers.Response
  -> app.ProviderResponse
```

Provider adapters own:

- vendor HTTP formats and authentication;
- response parsing;
- vendor error classification;
- respecting context cancellation and deadline.

The application owns:

- selected provider and model policy;
- stable failure categories;
- persistence;
- future retry, quota, and job policy.

No provider silently falls back to another provider or model.

## Persistence Boundary

The application repository port currently requires:

```text
SaveInteraction
GetLatestResponse
```

`internal/store` implements it through an adapter over the existing SQLite `interactions` table.

The schema remains versioned, transactional, and append-only. The adapter preserves compatibility with rows written before the application extraction.

Deferred work should use an explicit jobs table or an equally explicit schema extension. Do not overload `status = ok|error` with queued and running semantics.

## ASK Adapter

`internal/alexa` owns:

- Alexa request JSON parsing;
- certificate URL normalization and validation;
- bounded signing-certificate retrieval and caching;
- certificate-chain and Alexa signing-name validation;
- RSA SHA-256 body-signature verification;
- timestamp freshness;
- allowed skill application ID checks;
- intent and slot mapping;
- dialog directives and reprompts;
- Alexa-specific speech and response JSON.

It does not own:

- provider request construction;
- system prompt selection;
- persistence records;
- provider error parsing;
- future background job lifecycle.

`LaunchRequest` currently delegates into `AskGPTIntent` with a required `AMAZON.SearchQuery` slot. One-shot carrier phrases are defined in `skill/interaction-model.json`.

## Listener Topology

The process runs two HTTP servers:

```text
SERVER_ADDR
  public ASK listener
  POST /alexa only

ADMIN_SERVER_ADDR
  loopback administrative listener
  GET /health
  GET /history
  GET /
```

Defaults:

```text
SERVER_ADDR=127.0.0.1:8080
ADMIN_SERVER_ADDR=127.0.0.1:8081
```

Docker overrides the in-container binds to `:8080` and `:8081`, then maps both ports back to host loopback.

The listener split removes the previous failure mode in which a broad reverse-proxy rule exposed the dashboard and full history feed.

## Current Synchronous Execution

Current request flow:

```text
1. ASK adapter verifies the signed request.
2. ASK adapter maps the Alexa user, session, and query.
3. Application calls the selected provider under the request context.
4. Application stores the result.
5. ASK adapter speaks the short answer.
```

The configured provider timeout defaults to six seconds. This improves the chance of returning inside Alexa's external deadline, but it does not make slow reasoning reliable.

Current limitations:

- no accepted or pending state;
- no idempotency key;
- no background worker;
- no restart recovery;
- no way for `last response` to report a running job;
- a timeout is a failed request even if the same question could finish later.

## Target Deferred Execution

The next major architecture phase is:

```text
1. Verify and normalize the inbound request.
2. Persist an idempotent accepted job.
3. Try immediate execution under a short voice deadline.
4. Return Completed if the provider finishes.
5. Otherwise return Pending before Alexa times out.
6. Continue in a bounded worker under a separate job deadline.
7. Retrieve status and result later.
```

Required job states:

```text
accepted -> running -> completed
                    -> failed
                    -> canceled
```

Minimum requirements:

- adapter request ID and idempotency scope;
- persisted accepted state before provider spending;
- bounded queue and worker concurrency;
- provider handle where asynchronous polling is supported;
- explicit shutdown and restart recovery;
- principal-scoped status and result reads;
- rate limits and global spend controls.

The Alexa HTTP request context must not become the lifetime of an accepted background job.

## Local Web Adapter

The dashboard is an administrative surface, not a public product interface.

It is registered only on the administrative listener. It currently reads the SQLite store directly. A later read-service extraction may route it through application queries, but listener isolation is the immediate security property.

Remote administration would require separate authentication. It must not be added by exposing the current administrative port.

## Future MCP Adapter

No MCP package or route exists.

A future adapter would own:

- Streamable HTTP and MCP protocol handling;
- service and user authentication for the selected host;
- OAuth subject to principal mapping;
- tool schemas and structured MCP results;
- host-specific latency and cancellation behavior.

It would reuse application operations and would not own provider calls, persistence, retention, or cross-user authorization.

Do not add MCP scaffolding until the gates in `mcp-roadmap.md` are satisfied.

## Testing Strategy

Implemented test layers include:

- application service tests with fake provider and repository ports;
- ASK adapter tests with a fake application service;
- verifier tests with generated certificate chains and signed bodies;
- provider tests with local HTTP servers;
- SQLite integration tests with temporary databases;
- listener and route-isolation tests;
- interaction-model and dialog-response tests.

Still required:

- actual Alexa Developer Console model build;
- actual Amazon-signed request verification;
- physical Echo invocation and recognition tests;
- deferred-job and restart-recovery tests once that layer exists.

## Implementation Status

### Complete in code

- Application-core extraction.
- Provider and repository adapters.
- Stable application errors.
- ASK signature, certificate, timestamp, and skill-ID verifier.
- Dialog response support and interaction-model rewrite.
- Separate public and administrative listeners.
- OpenAI response-state and incomplete-output hardening.

### Partially complete

- Principal scoping: namespaced in the application, legacy-compatible in SQLite.
- Provider timing: bounded for synchronous ASK, but no deferred path.
- Security: authenticated ingress exists, but rate limits, spend caps, and real signed-request validation remain.

### Not started

- Durable jobs and idempotency.
- Retention and deletion.
- Public multi-user hosting.
- MCP runtime adapter.
