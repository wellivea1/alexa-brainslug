# Progress Checks

Use these checks before merging or declaring a phase complete. Automated checks are necessary but cannot replace real Alexa validation.

## Automated Baseline

The October 8 Claude continuation and optional Codex implementation passed
`go build ./...`, `go vet ./...`, the full race-enabled test suite, module tidy,
formatter/lint checks (zero issues), and `git diff --check`. Default per-package
statement coverage is 62.3%; cross-package integration and browser checks add
verification beyond that instrumentation. The standalone CLI reports protocol 1,
the two new Windows startup scripts pass parser checks, and disposable dashboard
submission/cancellation plus desktop/mobile layout were checked in the browser.

No live Codex inference, physical multi-machine LAN, updated ASK publication,
physical Echo, or desktop placement proof was performed. These release gates are
listed in `codex-operations.md` and `codex-integration-plan.md`.

Run locally where Go 1.26 and dependencies are available:

```bash
go build ./...
go test ./...
go tool golangci-lint run ./...
go tool golangci-lint fmt ./...
```

CI is configured to check:

- build;
- vet;
- race-enabled tests;
- tidy `go.mod` and `go.sum`;
- linting;
- formatting;
- ASCII-only repository policy;
- Docker image build;
- container startup through `/healthz` and authenticated administrative `/health`.

Coverage is diagnostic, not a quota. A changed behavior needs a test that would fail against the previous behavior.

## Current Implementation Snapshot

### Implemented in code

- [x] Transport-neutral application service.
- [x] Provider adapter over the existing provider clients.
- [x] SQLite adapter over the existing interactions store.
- [x] ASK handler depends on the application interface.
- [x] ASK signature and certificate verification infrastructure.
- [x] Timestamp and skill-ID enforcement.
- [x] Missing Alexa user identity is rejected.
- [x] Public and administrative listeners are separate.
- [x] Interaction model uses natural carrier phrases.
- [x] Launch delegates into the required query dialog.
- [x] OpenAI response statuses and output-limit exhaustion are classified.

### Not implemented

- [x] Optional Codex durable jobs, idempotency, and confirmed job states.
- [ ] Rate limits.
- [ ] Provider concurrency limit.
- [ ] Global spend cap.
- [x] Codex operational retention; ordinary answer-history deletion remains open.
- [ ] MCP runtime adapter.

### External validation status

- [ ] CI runner completes all configured jobs.
- [x] Race-enabled tests pass locally with CGO and a Windows C compiler.
- [x] Previous direct-answer interaction model built in Alexa Developer Console.
- [ ] Updated Codex intent model builds and is published in Alexa Developer Console.
- [x] Real Amazon-signed request passes the verifier.
- [x] Developer Console question and last-response requests complete through provider and SQLite history.
- [ ] Physical Echo completes the two-turn flow.

## Application-Core Review

Architecture:

- [x] `internal/app` contains no ASK protocol types.
- [x] `internal/alexa` does not import provider implementations.
- [x] `internal/alexa` supplies voice instructions and shapes full answers for speech.
- [x] The application and providers preserve full text without a voice default.
- [x] Legacy short-only history remains readable.
- [x] `internal/alexa` does not construct SQLite records.
- [x] Provider and repository dependencies are application-owned ports.
- [x] Main process wiring selects concrete adapters.
- [x] No MCP package or dynamic tool registry was introduced.

Behavior:

- [x] Prompt is normalized and validated.
- [x] Principal is required.
- [x] Configured model and output-token budget reach the provider adapter.
- [x] Success and failure records respect `SAVE_HISTORY`.
- [x] Provider diagnostics are redacted before persistence.
- [x] History write failure does not suppress a successful spoken answer.
- [x] Latest answer distinguishes no history, empty answer, and repository failure.

Tests:

- [x] Application tests use fake provider and repository ports.
- [x] ASK tests use a fake application service.
- [x] Provider adapter mapping is tested.
- [x] SQLite adapter round trip and not-found mapping are tested.

## ASK Verification Review

Implementation:

- [x] Raw body is preserved for signature verification.
- [x] Certificate URL requires HTTPS.
- [x] Certificate host is restricted to Amazon's signing host.
- [x] Port and path constraints are enforced.
- [x] Credentials and invalid ports are rejected.
- [x] Fragments, dot segments, and duplicate slashes are canonicalized before validation.
- [x] Redirects are revalidated.
- [x] Certificate fetch has a timeout and size cap.
- [x] PEM chain is parsed.
- [x] Trust chain is verified.
- [x] Alexa signing name is required.
- [x] `Signature-256` is base64-decoded and checked with RSA SHA-256.
- [x] Timestamp freshness is enforced.
- [x] Allowed skill ID is enforced.
- [x] Failure returns HTTP 400 before application execution.
- [x] Signing certificates are cached only within validity and cache lifetime.

Unit tests:

- [x] Valid generated certificate chain and signature.
- [x] Tampered body.
- [x] Stale timestamp.
- [x] Wrong skill ID.
- [x] Missing headers.
- [x] Disallowed certificate URL forms.
- [x] Invalid signing name.
- [x] Invalid root chain.
- [x] Certificate cache behavior.
- [x] Redirect behavior.

Real environment:

- [x] Valid Amazon production request.
- [x] Alexa Developer Console request.
- [ ] Physical Echo request.
- [x] Host clock and outbound certificate-fetch behavior confirmed.

## Listener Isolation Review

- [x] Public mux registers only `POST /alexa`.
- [x] Administrative mux exposes minimal `/healthz` and protects `/health`, `/history`, and `/` behind owner login.
- [x] `ADMIN_SERVER_ADDR` defaults to loopback.
- [x] Configuration rejects identical listener addresses.
- [x] Docker explicitly uses an in-container wildcard admin bind and maps it only to host loopback.
- [x] Docker maps both listeners to host loopback.
- [x] Container health check uses port 8081.
- [x] Caddy example proxies only `/alexa` to port 8080.
- [x] Route-isolation tests confirm public admin paths return 404.
- [ ] Any explicit non-loopback `ADMIN_SERVER_ADDR` override has been reviewed for the deployment topology.
- [ ] Public hostname is probed from outside the LAN.
- [ ] Port 8081 is confirmed unreachable from the public internet.

## Invocation And Dialog Review

Code and model:

- [x] `Query` uses `AMAZON.SearchQuery`.
- [x] `Query` is required.
- [x] One-shot samples use neutral carrier phrases.
- [x] Duplicated `ask` wording is removed.
- [x] Third-party model trademark is removed.
- [x] ASK response types support directives.
- [x] Launch returns `Dialog.Delegate` with an updated intent.
- [x] Missing query does not call the application service.
- [x] Help, fallback, stop, cancel, repeat, and last response remain routed.

Alexa platform:

- [x] Model builds in the selected locale.
- [x] Launch delegation is accepted.
- [x] Full follow-up utterance fills `Query`.
- [ ] Current one-shot phrases work on a real Echo.
- [ ] Activity history is reviewed for recognition failures.

Beta test:

- [x] Store metadata and skill icons are complete.
- [x] Privacy and compliance declarations are complete.
- [x] Required certification validation succeeds.
- [x] The 90-day beta test is running.
- [x] The beta invitation is accepted by the Amazon account registered to the Echo.
- [x] Launch and question flows are verified on the enrolled Echo.

## Provider Review

Common:

- [x] Provider and model selection are explicit.
- [x] No silent provider or model fallback.
- [x] Provider errors are mapped to stable application categories.
- [x] Raw Alexa identity is not sent to the provider.
- [x] Derived provider end-user identifier is hashed where supported.
- [x] Provider timeout defaults below Alexa's external budget.
- [x] Output-token cap is separate from spoken word shaping.
- [x] Provider metadata, configuration, credentials, and dashboard panels share a static table.
- [x] Compatible Responses and Chat Completions formats are selected explicitly and tested without fallback.
- [x] Provider-keyed settings preserve version 1 and 2 files and environment precedence.

OpenAI:

- [x] Responses API request is explicit.
- [x] Completed status is accepted.
- [x] Incomplete status is classified.
- [x] `max_output_tokens` exhaustion is distinct.
- [x] Failed response message is redacted.
- [x] Canceled, queued, and in-progress states do not masquerade as completed text.
- [x] Nested output-text fallback is tested.
- [ ] Real API request with the configured model is tested.
- [ ] Actual latency distribution is measured.

Anthropic:

- [x] Explicit model and token cap.
- [x] Provider adapter maps common failures.
- [ ] Current configured model is tested with a real key.
- [ ] Long-running or deferred strategy is defined before relying on it for deep mode.

## Current Synchronous ASK Review

- [x] Provider timeout default is six seconds.
- [x] Provider timeout is capped below Alexa's eight-second limit.
- [x] One response budget covers verification, provider call, and persistence.
- [x] Timeouts and caller hang-ups are still recorded in history.
- [x] Successful answer is spoken and optionally saved.
- [x] Timeout maps to a user-safe provider-busy response.
- [x] Empty visible output is diagnosable.
- [ ] Real Echo receives repeated synchronous answers before its timeout.

Known limitations:

- [ ] No accepted background state.
- [ ] No idempotency against Alexa retries.
- [ ] No way for `last response` to report pending work.
- [ ] Slow reasoning is not reliable.

## Deferred-Job Gate

The optional Codex controller/worker now satisfies these local checks through
fake runtime/protocol and actual loopback mTLS tests. Physical Echo, real accounts,
multi-machine LAN deployment and desktop placement remain external gates; see
`codex-operations.md`. Manual direct answers still use the synchronous path.

Do not call deferred work complete until all are true:

- [x] Independent transactional schema with version rejection.
- [x] Adapter ID and principal/source/request idempotency are stored.
- [x] Acceptance precedes inference; voice and execution deadlines are separate.
- [x] Queue, concurrency, execution and event spools are bounded.
- [x] Final states require facts; restart never blindly replays an uncertain turn.
- [x] Duplicates reuse the saved job and reads enforce current principal grants.
- [x] Pending, completed, failed, canceled and uncertain voice wording is tested.

## Identity And Data Review

- [x] Application operations require a namespaced principal.
- [x] Missing Alexa identity is rejected.
- [x] Latest-answer read is principal-scoped.
- [x] Legacy Alexa history remains readable.
- [x] Raw platform identity is not sent to providers.
- [x] `SAVE_HISTORY=false` works.
- [ ] Stored Alexa keys are migrated to explicit namespace form.
- [ ] Retention by age and count exists.
- [ ] Per-principal deletion exists.
- [ ] Dashboard principal scope is explicit.
- [ ] Backups follow the same retention and access policy.

## Rate And Spend Gate

Before continuous public exposure:

- [ ] Per-source network limit.
- [ ] Per-principal limit.
- [ ] Maximum prompt length.
- [ ] Bounded provider concurrency.
- [ ] Global daily request, token, or cost cap.
- [ ] Fail-closed behavior when the cap is reached.
- [ ] Limits are observable through private administration.
- [ ] Limit errors do not reveal private counters publicly.

## Local Deployment Review

- [x] Container runs with dropped capabilities.
- [x] Filesystem is read-only outside the data volume.
- [x] SQLite data persists under `./data`.
- [x] ASK port is host-loopback only.
- [x] Administrative port is host-loopback only.
- [x] Authenticated administrative `/health` reports provider and model without secrets.
- [x] Dashboard provides overview, searchable activity, and integrated provider management.
- [x] Provider/model changes activate without restart and persist without credentials.
- [x] OpenAI API-key and ChatGPT account authentication are distinct, with PKCE browser login, code fallback, refresh, disconnect, and secret-free dashboard state.
- [x] Dashboard is visually checked at desktop and phone breakpoints.
- [x] Public port has no administrative routes.
- [ ] Real Caddy or Tunnel route is tested.
- [ ] Real certificate retrieval works from the container.
- [ ] Restart preserves history.

## MCP Entry Gate

All applicable items must be true before MCP implementation begins:

- [ ] Named target MCP host.
- [ ] Amazon access if Alexa+ is the target.
- [ ] Confirmed protocol version.
- [ ] Confirmed service and user authentication.
- [ ] Tool set has value beyond generic LLM relay.
- [ ] Credible latency path for synchronous tools.
- [ ] Voice-only behavior for Alexa+.
- [ ] Exact policy and certification review.
- [ ] Durable jobs, principal scoping, idempotency, retention, and stable errors already exist.

If any item is false, keep MCP gated.

## Manual Smoke Test

Administrative health:

```powershell
Invoke-RestMethod http://127.0.0.1:8081/healthz
```

Expected for `/healthz`:

- `status` is `ok`;
- no credentials are returned.

After owner login, authenticated `/health` also reports the selected provider,
model, and Alexa verification state.

Public route probes:

```powershell
Invoke-WebRequest http://127.0.0.1:8080/ -SkipHttpErrorCheck
Invoke-WebRequest http://127.0.0.1:8080/history -SkipHttpErrorCheck
Invoke-WebRequest http://127.0.0.1:8080/health -SkipHttpErrorCheck
```

All should return 404.

For real Alexa testing, follow `alexa-skill-setup.md` and leave verification enabled.

## Completion Discipline

A milestone is complete only when:

- the corresponding exit criteria in `project-plan.md` are satisfied;
- automated checks run successfully;
- real-device behavior is documented where simulation is insufficient;
- known gaps remain explicit;
- documentation describes the code that actually exists.
