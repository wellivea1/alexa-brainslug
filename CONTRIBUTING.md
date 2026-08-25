# Contributing

## Prerequisites

Go 1.26 or newer. Nothing else needs installing: the linter is pinned as a tool dependency in `go.mod`, so `go tool` fetches and runs the same version CI does.

There is no Makefile. The project is developed on Windows as often as not, and `make` is not reliably present.

## Development Loop

```bash
go build ./...
go test ./...
go tool golangci-lint run ./...
go tool golangci-lint fmt ./...
```

Before opening a pull request, run what CI runs where the host supports it:

```bash
go mod tidy && git diff --exit-code -- go.mod go.sum
go vet ./...
go test -race -coverprofile=coverage.out -covermode=atomic ./...
go tool cover -func=coverage.out | tail -1
go tool golangci-lint run ./...
```

The race detector requires cgo. On a stock Windows machine it may be unavailable, so CI runs it on Linux. Run plain `go test ./...` locally and let CI cover the platform-specific race job when necessary.

## What CI Enforces

A build fails for:

1. Build, vet, or race-test failure.
2. Linter failure using `.golangci.yml`.
3. Formatting changes produced by `golangci-lint fmt`.
4. Non-ASCII text in Go, Markdown, JSON, or YAML.
5. Untidy `go.mod` or `go.sum`.
6. Docker build or container health failure.

Coverage is reported as a signal, not a target. A behavior change needs a test that would fail against the old behavior.

## Architecture Boundaries

The target architecture is described in `docs/architecture.md`.

The dependency rule is:

```text
inbound adapters
  -> application service
  -> application-owned provider and repository ports

outbound provider and SQLite packages
  -> implement those ports
```

### ASK adapter

Alexa code owns:

- ASK request verification.
- Alexa JSON parsing.
- Intent and slot mapping.
- Dialog directives.
- ASK response JSON and spoken wording.

Alexa code does not own:

- Provider invocation policy.
- Job lifecycle.
- Persistence records.
- Retention.
- Cross-surface identity.

After the application-core extraction, do not add a new direct import from `internal/alexa` to provider implementations, prompt policy, or concrete store records.

### Application service

Application code owns:

- Question submission.
- Immediate versus deferred execution.
- Provider and model policy.
- Stable failure categories.
- Principal scoping and authorization.
- Job state and idempotency.
- Persistence orchestration.

Application code must not import ASK or MCP protocol types.

### Provider adapters

Provider clients translate the application's provider request to one vendor's HTTP API and translate the response back.

They do not:

- Choose fallback providers or models.
- Store history.
- Write Alexa speech.
- Know which inbound adapter made the request.

### Future MCP adapter

Do not add MCP scaffolding speculatively. `docs/mcp-roadmap.md` defines the entry gates.

When an MCP adapter is justified, protocol types remain inside that adapter. Do not redesign the application core around a generic tool registry merely because MCP supports tools.

## Comments Explain Why

The code says what it does. A comment earns its place by recording something the code cannot, such as an external API constraint, a trade-off, or a bug the line prevents.

Good:

```go
// The immediate deadline stays below Alexa's external request budget so the
// adapter can return a pending acknowledgement instead of timing out.
```

Noise:

```go
// Increment the counter.
i++
```

Every exported symbol needs a doc comment. Every package needs a package comment. The linter enforces both.

## Errors

Wrap with `%w` so callers can match.

Define a sentinel or typed error when a caller needs different behavior. Adapters map stable application errors to protocol-specific responses; they do not parse provider error strings.

Never return or store a provider error without passing private diagnostics through secret redaction.

User-facing Alexa speech and future MCP errors must not contain:

- Credentials.
- Provider response bodies.
- Raw platform identifiers.
- Internal database or network details.

## Configuration

Configuration is explicit and validated at startup.

A variable that is present but unparseable is a startup error naming the variable, never a silent fallback.

Provider and model selection remain explicit. Do not add silent substitution because one provider is unavailable.

ASK and future MCP authentication must use separate configuration. Do not reuse one boolean verification switch across protocols.

## Anything Spoken Aloud

Text that reaches Alexa may have no screen behind it.

- Keep it short.
- Lead with the useful result.
- Do not refer the listener to a dashboard, log, or URL.
- Distinguish completed, pending, and failed work clearly.
- Do not promise a completion time for deferred work.
- Put reusable spoken text in named constants or an ASK presentation component rather than scattering literals through branches.

The application layer returns structured state. The ASK adapter chooses the wording.

## Identity And Secrets

Never log:

- Provider credentials.
- OAuth tokens.
- Alexa request signatures.
- Raw Alexa user IDs.
- Raw future MCP OAuth subjects.

Inbound adapters map platform identity to a namespaced internal principal. Providers receive only a derived identifier where they require an end-user field.

Prompts and answers are private data. Store them only in the local database according to configured history and retention policy.

## Deferred Work

A background job must be durable and bounded.

Required properties:

- Accepted state is persisted before provider execution.
- Adapter request IDs provide idempotency.
- Worker concurrency and queue length are bounded.
- Provider work has its own deadline.
- Shutdown and restart leave an explicit recoverable state.
- Status and result reads enforce principal ownership.

Do not start a goroutine from an HTTP handler without a defined owner, limit, shutdown path, and persisted job state.

## Database Migrations

`internal/store/migrations.go` is append-only.

Once released, a migration's version number and statements are frozen. Correct mistakes with a new migration. Do not reuse or reorder version numbers.

There are no down migrations.

## Tests

Prefer table-driven tests, named subtests, and assertion messages that state the expectation.

Boundary expectations:

- Application tests use fake provider and repository ports.
- ASK tests use a fake application service and fixture envelopes.
- Provider tests use `httptest.NewServer`, not live credentials.
- SQLite tests use temporary databases.
- Verification tests use sanitized signed fixtures.
- Deferred-job tests cover immediate completion, pending return, background completion, duplicate requests, provider failure, and shutdown recovery.

A regression test should fail against the old behavior. A test that would have passed before the fix documents code but does not protect the bug.

## ASCII Only

No smart quotes, em dashes, ellipses, or Unicode arrows anywhere in the repository.

Use:

```text
"quoted text"
-
...
->
```

CI checks this because browser and chat output commonly introduce Unicode punctuation unnoticed.

## Pull Requests

Keep a change and its tests together.

Explain why in the pull request description; the diff already shows what changed.

For architecture work:

- State which boundary moves.
- State which direct dependency disappears.
- State what behavior remains unchanged.
- Keep behavior-preserving extraction separate from new deferred or MCP behavior where practical.

If unrelated work is discovered, record it rather than expanding the branch silently.
