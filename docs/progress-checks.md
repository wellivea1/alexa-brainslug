# Progress Checks

Use this checklist before calling a milestone done.

Most of what used to be listed here by hand is now enforced automatically.
Formatting, linting, the ASCII rule, the race detector, and a container that
actually starts are all checked by CI on every push, so this document covers
what a machine cannot check: whether the thing behaves correctly and whether
it is safe to expose.

## Automated

Run locally before pushing:

```bash
go test ./...
```

```bash
go tool golangci-lint run ./...
```

CI additionally runs `go vet`, `go test -race`, a tidy check on `go.mod`, a
formatting check, the non-ASCII scan, and a Docker build that has to answer
`/health`. See `../CONTRIBUTING.md`.

Coverage is reported per package on every run. It is a signal, not a target:
`internal/speech` is at 100 percent because it is pure functions, and
`cmd/brainslug` is at zero because it is process wiring. Neither number means
much on its own. What matters is whether the behaviour a change touches has a
test that would fail without it.

## Code Quality Review

- Provider choice is explicit and visible in config, health output, and history.
- Model choice is explicit and not silently changed.
- Provider errors are stored only after redaction.
- No API keys, tokens, or raw Alexa user IDs are logged. Providers receive a
  hash of the user ID.
- Handlers stay small enough to test without a live provider.
- Provider tests use local HTTP test servers, not real API keys.
- SQLite tests use temp databases.
- Dashboard output escapes stored text through Go templates.
- Request body sizes are capped.
- New exported symbols have doc comments, and new packages have package
  comments. Comments explain why, not what.
- Migrations are appended, never edited. A released version number is frozen.

## Alexa Behavior Review

- `LaunchRequest` asks what to send.
- `AskGPTIntent` handles missing `Query`.
- `AskGPTIntent` stores success and failure records when history is enabled.
- `LastResponseIntent` is scoped to the Alexa user ID.
- Stop and cancel end the session.
- Fallback gives a short clarification.
- Alexa speech stays concise and voice-safe.
- Full answer remains available in local history.

## Security Review

- `ALEXA_VERIFY_REQUESTS=true` is the default.
- Public `/alexa` is not used until real Alexa verification is implemented.
- `ALEXA_ALLOWED_SKILL_ID` is set before serious testing.
- The reverse proxy routes `/alexa` only. Confirm by requesting `/history` and
  `/` against the public hostname and checking for a 404.
- HTTPS is provided by Caddy, Cloudflare Tunnel, or equivalent reverse proxy.
- Rate limiting exists before serious use.
- History retention is configured before storing sensitive long-lived prompts.

## Provider Review

OpenAI:

- `AI_PROVIDER=openai`.
- `OPENAI_MODEL` is set.
- `OPENAI_API_KEY` or `OPENAI_API_KEY_FILE` is set.
- `OPENAI_BASE_URL` is blank unless using an OpenAI-compatible endpoint intentionally.

Anthropic:

- `AI_PROVIDER=anthropic`.
- `ANTHROPIC_MODEL` is set.
- `ANTHROPIC_API_KEY` or `ANTHROPIC_API_KEY_FILE` is set.
- `ANTHROPIC_BASE_URL` is blank unless using an Anthropic-compatible endpoint intentionally.

Both:

- The output token cap leaves room for reasoning/thinking tokens as well as
  visible text. A cap that a reasoning model can exhaust before writing anything
  surfaces as "response did not include text output".
- The provider timeout is shorter than Alexa's 8-second budget, or the deferred
  answer path is in place.

## Manual Smoke Test

With verification disabled for local-only testing:

```powershell
$env:ALEXA_VERIFY_REQUESTS = "false"
go run ./cmd/brainslug
```

Then:

```powershell
Invoke-RestMethod http://127.0.0.1:8080/health
```

Expected:

- `status` is `ok`.
- `provider` matches `AI_PROVIDER`.
- `model` matches the selected provider model.
- No secrets are returned.

## Current Review Snapshot

As of this review:

- Build, vet, tests, and the linter all pass clean.
- The biggest blocker is Alexa request verification. Until it exists there is no
  configuration that both works and is safe to expose.
- The biggest design gap is that slow answers are treated as failures, when slow
  answers are the entire point of the project.
- The biggest operational gap was a reverse-proxy example that published the
  history feed. Fixed in `Caddyfile.example`; the structural fix (a separate
  loopback listener) is Phase 2.
- Docker deployment is now exercised by CI, which builds the image and requires
  the container to answer `/health`. It has still not been run against a real
  provider key.

Fixed during the architecture review, worth knowing about:

- `created_at` was stored with `time.RFC3339Nano`, which drops trailing zeros.
  The column is TEXT and sorted as text, so `...T12:00:00Z` compared greater
  than `...T12:00:00.1Z` and an older row could win `ORDER BY created_at DESC`.
  That is the query behind `last response`, so it could speak a stale answer.
  Timestamps are now fixed width, and migration 2 repairs existing rows.
- `os.Exit` in `main` skipped every deferred cleanup, including the database
  handle. Startup now returns an error instead.
- There was no graceful shutdown and no write or idle timeout on the server.
- The system prompt told the model to refer listeners to the local dashboard,
  which voice users cannot see.
