# Project Plan

## Goal

Build a reliable private voice path from Alexa to an explicitly selected AI model while keeping Alexa as an adapter rather than the application architecture.

Current architecture:

```text
ASK adapter
  -> transport-neutral application service
  -> explicit provider and SQLite adapters
```

Alexa+ MCP remains optional and gated. It must not delay ASK usability or become a generic alternate-assistant relay.

## Product Position

Current intended use:

- one owner;
- private Alexa account;
- self-hosted service;
- explicit provider and model;
- concise spoken answer plus optional local history.

Not current product scope:

- public Skill Store hosting;
- unrestricted multi-user history;
- an open web API;
- a public Alexa+ add-on;
- external-agent control of Alexa.

## Current Implementation Snapshot

Implemented:

- `internal/app` question and latest-answer service;
- provider-neutral application request and result types;
- OpenAI and Anthropic adapters;
- SQLite application repository adapter;
- stable provider failure categories and diagnostic redaction;
- namespaced application principals;
- ASK signature, certificate-chain, timestamp, and skill-ID verification;
- bounded certificate retrieval and caching;
- separate public ASK and loopback administrative listeners;
- dialog response types and launch-to-question delegation;
- natural `AMAZON.SearchQuery` carrier phrases and a required elicited slot;
- OpenAI response-state and output-limit handling;
- Docker, Caddy, tests, linting, and CI definitions.

Not implemented:

- durable deferred jobs;
- adapter request idempotency;
- background workers and restart recovery;
- rate limits, provider concurrency limits, and spend caps;
- retention and deletion;
- MCP runtime code.

Not externally validated:

- Alexa Developer Console interaction-model build;
- actual Amazon-signed request verification;
- physical Echo recognition and complete round trip.

## Ordering Principle

The remaining order is:

1. validate the implemented ASK path against Amazon and a real Echo;
2. add rate limits and spend controls;
3. add durable deferred jobs and idempotency;
4. complete retention and identity migration;
5. add reusable capabilities only when needed;
6. evaluate MCP only after access and product gates exist.

## Architecture Principles

- ASK is an inbound adapter, not the core.
- Application use cases own provider execution and persistence policy.
- Provider clients are outbound adapters and never choose fallback policy.
- SQLite persists state but does not decide authorization.
- Protocol types remain inside protocol adapters.
- Model and provider choices remain explicit.
- Security-critical incomplete work fails closed.
- Structural isolation is preferred over proxy warnings.
- Slow work must become a durable job, not a longer HTTP timeout.
- MCP is an adapter option, not the internal architecture.

See `architecture.md`.

## Phase 0: Validate The Real ASK Round Trip

Purpose: prove the generated interaction model and custom verifier against Amazon's actual runtime.

Current code support:

- signed request verifier exists;
- public route is isolated;
- dialog flow exists;
- physical-device test has not been performed in this environment.

Procedure:

1. Create or open the private custom skill.
2. Import `skill/interaction-model.json` and build it.
3. Set the endpoint to the public HTTPS `/alexa` route.
4. Configure the exact `ALEXA_ALLOWED_SKILL_ID`.
5. Leave `ALEXA_VERIFY_REQUESTS=true`.
6. Test through the Developer Console.
7. Invoke the skill from the real Echo.
8. Save a sanitized signed fixture if Amazon's terms and the fixture-redaction rules permit it.

Exit criteria:

- The interaction model builds without modification or the required modification is committed.
- A real Amazon-signed request passes verification.
- A tampered or stale request fails.
- A real Echo completes the two-turn flow.
- Public `/`, `/history`, and `/health` remain unreachable.
- Invocation phrases that actually work are recorded.

Status: external validation required.

## Phase 1: Extract The Application Core

Purpose: separate product behavior from ASK before adding asynchronous work.

Completed implementation:

- `internal/app` owns question submission and latest-answer lookup.
- ASK depends on an application interface and no longer imports providers, prompts, or store records.
- Provider and repository adapters map existing packages to application-owned ports.
- Application tests use fakes.
- ASK tests use a fake application service.
- No MCP package or generic capability registry was introduced.

Remaining validation:

- complete compile, race, lint, formatting, and container checks in an allocated CI runner;
- review the final diff after those checks.

Status: implemented; automated validation pending.

## Phase 2: Safe ASK Ingress

Purpose: prevent the public endpoint from becoming an unauthenticated paid API route or an administrative-data leak.

Completed implementation:

- certificate URL normalization and Amazon host/path allowlisting;
- bounded certificate fetch and redirect validation;
- certificate-chain and Alexa signing-name verification;
- SHA-256 signature verification over the exact raw request body;
- timestamp freshness enforcement;
- allowed skill ID enforcement;
- HTTP 400 on verification failure before application execution;
- verifier tests using generated certificate chains and signed bodies;
- separate public and administrative listeners;
- loopback administrative default;
- Docker port separation.

Outstanding:

- real Amazon-signed request validation;
- per-source and per-principal rate limits;
- bounded provider concurrency;
- global request or spend cap;
- operational monitoring for rejected and accepted requests.

Exit criteria:

- real Alexa requests pass with verification enabled;
- invalid, stale, foreign-skill, and tampered requests fail before provider use;
- administrative routes are inaccessible through the public listener;
- rate limits and a global cap fail closed;
- provider concurrency is bounded.

Status: verifier and listener isolation implemented; operational limits and real-runtime validation outstanding.

## Phase 3: ASK Usability And Deferred Work

### Invocation

Completed implementation:

- natural one-shot carrier phrases;
- required `AMAZON.SearchQuery` slot;
- launch response delegates into `AskGPTIntent`;
- dialog directive response types;
- help, fallback, stop, cancel, repeat, and last-response routing.

Outstanding:

- Developer Console model build;
- real Echo recognition testing;
- wording adjustment based on actual device behavior.

### Immediate response path

Completed implementation:

- provider timeout defaults to six seconds;
- concise spoken response shaping;
- full answer storage when history is enabled;
- explicit handling of OpenAI incomplete and failed statuses;
- output-token exhaustion is a stable diagnosable category;
- provider and model do not silently change.

Limitation:

- the provider call is still synchronous and uses the ASK request context.

### Deferred response path

Not implemented.

Required work:

- job table and append-only migration;
- accepted, running, completed, failed, and canceled states;
- adapter request ID and idempotency key;
- immediate deadline shorter than the external ASK deadline;
- independent background job deadline;
- bounded worker queue and concurrency;
- restart recovery;
- pending and failed `last response` semantics;
- provider cancellation where supported.

Status: invocation and synchronous hardening implemented; durable deferred execution not started.

## Phase 4: Identity, History, And Retention

Completed or partial:

- application operations require a namespaced principal;
- missing Alexa user identity is rejected;
- provider end-user identifiers are derived and hashed;
- history reads are principal-scoped;
- legacy Alexa storage keys remain readable;
- `SAVE_HISTORY=false` remains supported.

Outstanding:

- migrate stored Alexa keys to an explicit namespace;
- retention by age and count;
- per-principal deletion;
- dashboard principal filtering or owner-only authentication;
- per-principal quotas;
- explicit cross-platform account-linking policy.

Status: principal boundary implemented; lifecycle controls outstanding.

## Phase 5: Define Reusable Capabilities

Start only after durable jobs and data controls exist.

Candidate application operations:

- submit an analysis job;
- read caller-owned job status;
- retrieve a completed answer;
- search caller-owned saved answers;
- delete caller-owned saved data.

Rules:

- typed input and output;
- application-owned authorization;
- idempotent mutations;
- accepted state for long-running work;
- no generic dynamic `Invoke(name, map[string]any)` entry point without a concrete need.

Status: deferred.

## Phase 6: Evaluate An MCP Adapter

Entry gates:

- a named target MCP host exists;
- Amazon grants access if Alexa+ is the target;
- authentication and protocol version are known;
- the tool set provides value beyond relaying arbitrary prompts to an LLM;
- synchronous tools have a credible latency path;
- voice-only behavior exists for Alexa+;
- policy and certification review covers the exact tool set;
- durable jobs, principal scoping, idempotency, retention, and stable errors already exist.

Implementation rules:

- MCP remains an inbound adapter over application operations;
- ASK verification and MCP OAuth stay separate;
- authenticated OAuth subject maps to a separate principal namespace;
- slow model work uses accepted/status/result operations rather than blocking;
- no production Alexa+ profile is centered on `chat(prompt)`.

Status: gated; no runtime code should be added speculatively.

## Phase 7: Decide Distribution

Private use can stop before this phase.

Public distribution requires:

- production hosting;
- privacy policy and data disclosures;
- per-user metering and global spend controls;
- deletion procedures;
- abuse controls;
- monitoring and incident response;
- certification-ready invocation and content behavior;
- a deliberate decision about whether prompts and answers are stored.

Status: not selected.

## Engineering Standards

- Configuration is explicit and validated at startup.
- Errors wrap or join stable sentinels; adapters do not parse strings.
- Credentials and raw platform identifiers do not reach logs or provider payloads.
- Migrations are append-only and transactional.
- Provider tests use local HTTP servers.
- Application tests use fakes.
- Verifier tests do not depend on Amazon network access.
- Shutdown is bounded.
- CI enforces build, vet, race tests, linting, formatting, ASCII, and container startup when a runner is allocated.

See `../CONTRIBUTING.md` and `progress-checks.md`.

## Rejected Approaches

### Alexa handler as the application

Removed. ASK now maps protocol data into `internal/app` operations.

### Alexa+ MCP first

Rejected. Access and policy are gated, and MCP does not solve ASK verification or voice deadlines.

### One universal protocol handler

Rejected. ASK and MCP have different authentication, timing, and response models.

### Longer ASK timeout

Rejected. Alexa controls the external deadline. Slow work needs durable deferred execution.

### Generic OpenCode provider

Dropped. It adds another general model path without a distinct product capability.
