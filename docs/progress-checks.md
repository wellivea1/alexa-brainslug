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
- No API keys, OpenCode passwords, tokens, or request signatures are logged.
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
- Dashboard is not exposed publicly without authentication.
- HTTPS is provided by Caddy, Cloudflare Tunnel, or equivalent reverse proxy.
- OpenCode is not exposed directly.
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

OpenCode:

- `AI_PROVIDER=opencode` must fail closed until the provider is implemented.
- OpenCode service must bind to loopback or a private local interface.
- Gateway must enforce timeouts, output limits, and a narrow request shape.

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

- `go test ./...` passes.
- `go vet ./...` passes.
- The service aligns well with the original local/private gateway goal.
- The biggest blocker is Alexa request verification.
- The biggest provider gap is OpenCode Go API implementation.
- The biggest operational gap is dashboard authentication or reverse-proxy auth.
