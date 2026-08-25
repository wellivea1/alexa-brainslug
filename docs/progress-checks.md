# Progress Checks

Use these checks before calling a project phase complete. Automated checks remain necessary but are not sufficient; most failures that matter here are architectural, security, or real-device behavior failures.

## Automated Baseline

Run locally:

```bash
go build ./...
go test ./...
go tool golangci-lint run ./...
go tool golangci-lint fmt ./...
```

CI additionally checks:

- `go vet`.
- Race-enabled tests on Linux.
- Tidy `go.mod` and `go.sum`.
- Formatting.
- Repository ASCII policy.
- Docker image build.
- Container startup and `/health` response.

Coverage is diagnostic, not a quota. A changed behavior needs a test that would fail against the previous behavior.

## Current Baseline Snapshot

Known implemented baseline:

- Go server, provider clients, SQLite, dashboard, Docker, and CI exist.
- Provider and model selection are explicit.
- Provider error redaction exists.
- Alexa launch, ask, last response, help, fallback, stop, cancel, repeat, and session end are routed.
- Caddy example limits the public route to `/alexa`.

Known incomplete baseline:

- Request verification does not accept real Alexa requests when enabled.
- Alexa handler still owns provider calls and persistence orchestration.
- Provider timeout can exceed the ASK deadline.
- Deferred jobs and idempotency do not exist.
- One-shot sample utterances are awkward.
- Public and administrative routes share one listener.
- Rate limiting, spend caps, complete principal scoping, and retention do not exist.
- MCP does not exist and is intentionally gated.

Do not mark a later phase complete merely because the baseline tests still pass.

## Phase 0: Real ASK Round Trip

- [ ] A private custom skill exists in the developer console.
- [ ] The actual skill ID is configured locally.
- [ ] A path-scoped short-lived HTTPS route reaches `/alexa`.
- [ ] Public `/`, `/history`, and `/health` return 404.
- [ ] A real Echo invocation reaches the local log.
- [ ] A low-cost provider answer is spoken.
- [ ] A sanitized valid request fixture is saved for verification tests.
- [ ] Raw user IDs, signatures, certificates, and credentials are removed from fixtures.
- [ ] The public route is disabled after verification-disabled testing.

## Phase 1: Application-Core Extraction

Architecture:

- [ ] A transport-neutral application package exists.
- [ ] Application request and result types contain no ASK JSON types.
- [ ] `internal/alexa` does not import provider implementations.
- [ ] `internal/alexa` does not select system prompts or construct store records.
- [ ] Latest-answer semantics live in the application service.
- [ ] Provider and repository dependencies are narrow interfaces owned at the application boundary.
- [ ] No MCP package, dynamic registry, or public API was added during the extraction.

Tests:

- [ ] Application tests use fake provider and repository implementations.
- [ ] Alexa adapter tests use a fake application service.
- [ ] Provider tests still use local HTTP servers.
- [ ] SQLite tests still use temporary databases.
- [ ] Existing voice behavior remains intact.

## Phase 2: Safe ASK Ingress

Verification:

- [ ] Certificate URL constraints are enforced.
- [ ] Certificate retrieval is bounded.
- [ ] Trust chain and signing identity are validated.
- [ ] Signature is verified against the exact raw body.
- [ ] Timestamp freshness is enforced.
- [ ] Allowed skill ID is enforced.
- [ ] Verification failure returns HTTP 400 and does not call the application service.

Verification tests:

- [ ] Valid fixture.
- [ ] Tampered body.
- [ ] Stale timestamp.
- [ ] Wrong skill ID.
- [ ] Disallowed certificate URL.
- [ ] Invalid or expired certificate.
- [ ] Oversized request body.

Exposure:

- [ ] ASK and administrative listeners are separate.
- [ ] Administrative listener binds to loopback by default.
- [ ] Public hostname cannot reach `/`, `/history`, or `/health` even under a broad proxy rule.
- [ ] Per-source and per-principal rate limits exist.
- [ ] Provider concurrency is bounded.
- [ ] Global request or spend cap fails closed.
- [ ] Real Echo works with verification enabled.

## Phase 3: ASK Usability And Deferred Work

Invocation:

- [ ] Interaction model builds with natural carrier phrases.
- [ ] `Alexa, open Brain Slug` elicits the full question.
- [ ] `AMAZON.SearchQuery` captures the follow-up utterance.
- [ ] At least `about`, `why`, `how`, and `to explain` one-shot forms work on a real Echo.
- [ ] Help explains the one-shot and two-turn routes briefly.
- [ ] No sample utterance names a third-party model product.

Immediate result:

- [ ] A fast provider response is spoken in the same turn.
- [ ] The application deadline is shorter than Alexa's external timeout.
- [ ] The spoken answer is concise and voice-safe.
- [ ] Full answer remains locally retrievable when history is enabled.

Deferred result:

- [ ] Job states include accepted, running, completed, failed, and canceled.
- [ ] Accepted state is persisted before provider execution.
- [ ] Slow work returns a pending acknowledgement before Alexa times out.
- [ ] Background work uses an independent bounded deadline.
- [ ] Worker concurrency and queue length are bounded.
- [ ] Duplicate authenticated requests reuse the existing job.
- [ ] `last response` distinguishes no history, pending, completed, and failed.
- [ ] Shutdown or restart does not leave unexplained permanent running jobs.

Provider policy:

- [ ] Output-token defaults leave room for reasoning and visible text.
- [ ] Reasoning or thinking effort is explicit where supported.
- [ ] Provider and model do not silently change.
- [ ] Empty visible output is a distinct diagnosable failure.

## Phase 4: Identity And Data Controls

- [ ] Every saved-data operation requires a namespaced principal.
- [ ] Raw Alexa user IDs are transformed at the adapter boundary.
- [ ] Raw platform identities are not sent to providers.
- [ ] History, status, result, search, and delete operations enforce ownership inside the application layer.
- [ ] Cross-principal reads fail in tests.
- [ ] Retention by age and count exists.
- [ ] Deletion is explicit and tested.
- [ ] `SAVE_HISTORY=false` remains supported.
- [ ] Dashboard states which principal scope is displayed.
- [ ] Logs omit prompts and answers by default.

## Application Quality Review

- [ ] Configuration is validated at startup.
- [ ] Model and provider are explicit in config, health output, and saved metadata.
- [ ] Stable error categories do not depend on parsing strings in adapters.
- [ ] Provider diagnostics are redacted before storage or logging.
- [ ] Request and job IDs support correlation without exposing credentials or raw platform IDs.
- [ ] Schema migrations are append-only, transactional, and tested from older versions.
- [ ] Shutdown drains listeners and workers under bounded deadlines.
- [ ] No unbounded goroutine, queue, request body, result body, or retry loop exists.

## Local Deployment Review

- [ ] Container starts as the intended unprivileged user.
- [ ] SQLite data persists in the mounted volume.
- [ ] `SQLITE_PATH` resolves to a writable mounted location.
- [ ] Local `/health` reports status, provider, and model without secrets.
- [ ] Public routing is limited to `/alexa`.
- [ ] Administrative routes are loopback-only after the listener split.
- [ ] Provider keys are supplied through a noncommitted environment file, mounted secret, or secret manager.
- [ ] Public route state is documented after testing.

## Future Capability Review

Do not perform this review until Phases 1 through 4 are complete.

- [ ] Each capability represents one user-visible operation.
- [ ] Input and output schemas are typed.
- [ ] Authorization is application-owned.
- [ ] Long-running operations return accepted state.
- [ ] No generic dynamic invocation interface was added without a concrete adapter need.
- [ ] ASK and local administration reuse the same application operations where appropriate.

## MCP Entry Gate

All applicable items must be true before MCP implementation begins:

- [ ] A named target MCP host exists.
- [ ] Amazon access exists if Alexa+ is the target.
- [ ] The target protocol version is confirmed.
- [ ] Service and user authentication requirements are confirmed.
- [ ] Proposed tools provide value beyond a generic LLM relay.
- [ ] Synchronous tools have a credible latency path.
- [ ] Voice-only behavior exists for Alexa+ tools.
- [ ] Exact policy and certification requirements have been reviewed.
- [ ] The application core already supplies principal scoping, idempotency, jobs, retention, and stable errors.

If any item is false, keep MCP gated.

## MCP Adapter Review

When the gate is met:

- [ ] MCP protocol types remain inside the adapter.
- [ ] ASK verification and MCP OAuth are separate.
- [ ] Authenticated subjects map to a separate principal namespace.
- [ ] Tool discovery exposes only the selected profile.
- [ ] No tool accepts a caller-supplied user ID as authorization.
- [ ] No production Alexa+ profile is centered on `chat(prompt)` or `ask_model(prompt)`.
- [ ] Slow model work does not block a synchronous tool beyond the host budget.
- [ ] Structured results and stable errors contain no private diagnostics.
- [ ] Optional UI has a complete voice-only fallback.
- [ ] MCP listener is separate from ASK and administration.

## Manual Smoke Test

With no public exposure, local health:

```powershell
Invoke-RestMethod http://127.0.0.1:8080/health
```

Expected:

- `status` is `ok`.
- `provider` matches `AI_PROVIDER`.
- `model` matches the configured model.
- No credentials are returned.

For a real Echo smoke test, follow `alexa-skill-setup.md`. Do not leave verification disabled or the tunnel running after the controlled test.

## Review Discipline

A milestone is not complete when its code exists. It is complete when:

- The exit criteria in `project-plan.md` are satisfied.
- The relevant checks above pass.
- Real-device behavior is documented where the platform cannot be simulated reliably.
- Known gaps are moved forward explicitly rather than hidden in prose.
- The documentation describes the code that actually shipped, not only the target design.
