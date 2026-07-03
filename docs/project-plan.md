# Project Plan

## Reviewed Inputs

- Original project request in this repo's attachment.
- The open ChatGPT conversation titled "Alexa Skill with ChatGPT" in Edge.
- Current Alexa Brainslug code and tests.
- Prior-art reviews of `jackmcguire1/alexa-chatgpt` and NoobBoard.

## Goal

Alexa Brainslug is a private Alexa custom skill gateway. Alexa calls a public HTTPS endpoint, the local Go service verifies and routes the request, the selected provider answers, Alexa receives a concise spoken response, and the full answer is stored locally.

The supported provider direction is:

- OpenAI API.
- Anthropic API.
- OpenCode Go API behind the local gateway.

OpenAI and Anthropic are hosted model providers. OpenCode is a local agent/provider integration and must never be exposed directly to Alexa or the internet.

## Current State

Implemented:

- Go HTTP server with `/health`, `/alexa`, `/history`, and `/`.
- Alexa request/response routing for launch, ask, last response, help, fallback, stop, cancel, repeat, and session end.
- SQLite interaction storage.
- Minimal local dashboard.
- Docker Compose and Caddy example.
- OpenAI provider using the Responses API.
- Anthropic provider using the Messages API.
- Provider selection with `AI_PROVIDER=openai|anthropic|opencode`.
- OpenCode provider placeholder that fails closed.
- Secret-file support for provider credentials.
- Browser hardening headers, request body cap, and provider error redaction.
- Basic unit tests and SQLite integration tests.

Known gaps:

- Full Alexa request signature verification is not implemented.
- OpenCode Go API provider is not implemented.
- `/` and `/history` have no authentication.
- `/alexa` has no app-level rate limiting.
- Long provider latency is not handled asynchronously.
- History retention controls are not implemented.
- Provider-specific model and error metadata are still minimal.

## Architecture Principles

- Keep the service local-first and private.
- Keep the Alexa endpoint narrow: it should only accept Alexa-compatible JSON and return Alexa-compatible JSON.
- Do not assume LAN-only access works for Alexa.
- Do not hide the selected provider or model.
- Store full answers locally, but speak concise answers.
- Fail closed for security-critical incomplete work.
- Keep OpenCode behind the gateway, loopback-bound, and permission-limited.
- Prefer boring Go interfaces and explicit env configuration over framework abstractions.

## Phases

### Phase 1: Reliable Single-Turn MVP

Exit criteria:

- OpenAI and Anthropic providers both work with real credentials.
- Local dashboard shows recent successful and failed interactions.
- `last response` returns the latest successful response for the Alexa user ID.
- Tests pass with no real provider keys.

Status: mostly complete.

### Phase 2: Alexa Production Safety

Exit criteria:

- `/alexa` validates Alexa signatures, certificate URL, certificate chain, timestamp freshness, and allowed skill ID.
- `ALEXA_VERIFY_REQUESTS=true` works with the Alexa Developer Console.
- Request verification has unit tests using captured or fixture requests.
- `/alexa` has rate limiting by IP and Alexa user ID.

Status: not started; highest priority.

### Phase 3: Dashboard Safety And Retention

Exit criteria:

- Dashboard authentication or documented reverse-proxy auth is in place.
- History retention can be configured by age and count.
- Stored errors remain redacted.
- Prompts and answers remain clearly documented as sensitive local data.

Status: not started.

### Phase 4: OpenCode Provider

Exit criteria:

- OpenCode provider uses the Go API/client path selected for this project.
- OpenCode is only called over loopback or a private local endpoint.
- Credentials are read from env or secret file.
- Provider cannot run arbitrary commands from Alexa text.
- Timeouts, output caps, and audit/history metadata are enforced.
- Tests cover disabled/misconfigured OpenCode, successful response parsing, and provider errors.

Status: placeholder only.

### Phase 5: Latency Handling

Exit criteria:

- Alexa can receive a quick "available shortly" response when a provider call exceeds a voice-safe threshold.
- The full answer is completed in the background and retrievable via `last response`.
- No external queue is required for the local MVP.

Status: planned.
