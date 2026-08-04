# Progress Checks

Use this checklist before calling a milestone done.

## Always Run

```powershell
go test ./...
go vet ./...
go build ./cmd/brainslug
```

Also check:

```powershell
rg -n "[^\x00-\x7F]" .
git status --short
```

## Code Quality Review

- Provider choice is explicit and visible in config, health output, and history.
- Model choice is explicit and not silently changed.
- Provider errors are stored only after redaction.
- No API keys, tokens, or request signatures are logged.
- Handlers stay small enough to test without a live provider.
- Provider tests use local HTTP test servers, not real API keys.
- SQLite tests use temp databases.
- Dashboard output escapes stored text through Go templates.
- Request body sizes are capped.

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

- `go test ./...`, `go vet ./...`, and `go build ./cmd/brainslug` pass.
- The code is clean; the problems are in configuration, deployment, and scope.
- The biggest blocker is Alexa request verification. Until it exists there is no
  configuration that both works and is safe to expose.
- The biggest design gap is that slow answers are treated as failures, when slow
  answers are the entire point of the project.
- The biggest operational gap was a reverse-proxy example that published the
  history feed. Fixed in `Caddyfile.example`; the structural fix (a separate
  loopback listener) is Phase 2.
- Docker deployment has not been verified end to end.
