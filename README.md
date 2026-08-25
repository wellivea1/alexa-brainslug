# Alexa Brainslug

Alexa Brainslug is a self-hosted, single-owner service for sending selected Alexa questions to an explicitly chosen AI provider and hearing a concise answer through an Echo.

The current entry point is a classic Alexa Skills Kit (ASK) custom skill. The target design separates that Alexa transport from a platform-independent application core so the same use cases can later be exposed through another approved adapter. Alexa+ MCP is a possible future adapter, not a current dependency or deployment target.

The purpose is deterministic model choice for questions worth spending reasoning tokens on. It is not a general chatbot framework, a public LLM relay, or an API for controlling Alexa from another assistant.

## Scope

Current product surface:

- Private ASK custom skill named `brain slug`.
- Go service running locally or in Docker.
- OpenAI and Anthropic provider clients.
- Concise spoken answers and full local SQLite history.
- Local development dashboard.

Target architecture:

- ASK adapter responsible only for Alexa verification, intent mapping, and Alexa responses.
- Transport-neutral application service responsible for question submission, provider execution, persistence, identity scoping, and deferred work.
- Provider and SQLite packages treated as outbound adapters.
- Optional future MCP adapter exposing typed, domain-specific capabilities only after Amazon access, authentication, latency, and certification requirements are satisfied.

Explicit non-goals:

- Raw or continuous microphone access.
- Arbitrary out-of-session speech on Echo devices.
- A reverse MCP server that lets ChatGPT, Claude, or another client control Alexa.
- An Alexa+ MCP tool whose only operation is `chat(prompt)` or another generic relay to a third-party LLM.
- Skill Store publication before multi-user metering, privacy, retention, and production hosting exist.

## Current And Target Architecture

Current implementation:

```text
Alexa cloud
  -> POST /alexa
  -> internal/alexa
  -> internal/providers
  -> internal/store
  -> Alexa response + local history
```

The current Alexa handler directly owns provider invocation and persistence. That is workable for a first round trip, but it makes Alexa the application architecture rather than one interface to it.

Target implementation:

```text
                     +----------------------+
Alexa ASK ---------->| ASK adapter          |
                     | verify, map, render  |
                     +----------+-----------+
                                |
                                v
                     +----------------------+
                     | application core     |
                     | submit, query, jobs  |
                     | identity and policy  |
                     +----+------------+----+
                          |            |
                          v            v
                    AI providers     SQLite store

Future approved MCP client
  -> MCP adapter
  -> the same application core
```

The first architectural change is behavior-preserving: extract the question and history use cases from `internal/alexa` into `internal/app`. Do not add MCP, a capability registry, or another network listener during that extraction.

See [docs/architecture.md](docs/architecture.md) for the target boundaries and migration sequence.

## Current Status

Implemented:

- Go HTTP server with `/health`, `/alexa`, `/history`, and `/`.
- Alexa launch, ask, last response, help, fallback, stop, cancel, repeat, and session-end routing.
- OpenAI Responses API and Anthropic Messages API clients.
- Explicit provider and model selection.
- SQLite interaction history and migrations.
- Local dashboard.
- Docker Compose, Caddy example, CI, linting, and tests.

Blocking or materially incomplete:

- Alexa request signature verification is a fail-closed placeholder. With `ALEXA_VERIFY_REQUESTS=true`, Alexa is rejected. With it disabled, the endpoint is an unauthenticated route to a paid provider key.
- Provider timeouts default to 12 seconds while an ASK response must complete inside Alexa's roughly 8-second request budget.
- Slow reasoning requests are treated as failed voice turns instead of deferred jobs.
- `MAX_OUTPUT_TOKENS` covers reasoning or thinking tokens as well as visible text, so the current low default can produce no answer text.
- The shipped sample utterances produce awkward one-shot phrases such as `Alexa, ask Brain Slug ask ...`.
- `/`, `/history`, and `/alexa` share one listener. Proxy rules must currently prevent public access to the dashboard and history.
- `/alexa` has no rate limiting or global spend cap.
- History retention and multi-user scoping are incomplete.

There is currently no configuration that is both functional and safe for continuous public exposure. Use a short-lived tunnel only for controlled round-trip testing until request verification is implemented.

## Why MCP Is Future-Only

Amazon's documented Alexa+ MCP architecture makes Alexa+ the MCP client and the third party the MCP server. Production access is currently partner-gated, not a self-service replacement for ASK.

The future MCP adapter is therefore gated on all of the following:

- Amazon grants the project access to the relevant Alexa+ builder program.
- Brainslug has useful typed capabilities beyond forwarding arbitrary prompts to another LLM.
- The MCP server can meet Amazon's authentication and latency requirements.
- Voice-only and display behavior are defined for every exposed operation.
- The capability set can pass Alexa+ policy and certification review.

The current private ASK skill remains the practical route. MCP work must not delay request verification, natural invocation, deferred answers, or application-core extraction.

See [docs/alexa-platform.md](docs/alexa-platform.md) and [docs/mcp-roadmap.md](docs/mcp-roadmap.md).

## Requirements

- Go 1.26 or newer.
- Docker and Docker Compose for containerized use.
- An API key for the selected provider.
- A public HTTPS endpoint for ASK testing, using a path-scoped reverse proxy or tunnel.

Alexa requests originate in Amazon's cloud. A LAN-only address is not a valid skill endpoint.

## Local Development

PowerShell:

```powershell
Copy-Item .env.example .env
notepad .env
go mod download
go run ./cmd/brainslug
```

Set at least:

```text
AI_PROVIDER=openai
OPENAI_API_KEY=your_key_here
OPENAI_MODEL=gpt-5-mini
ALEXA_VERIFY_REQUESTS=false
```

`ALEXA_VERIFY_REQUESTS=false` is only for local fixture tests or a short-lived controlled Alexa round-trip. Do not leave a public endpoint in that state.

Anthropic example:

```text
AI_PROVIDER=anthropic
ANTHROPIC_API_KEY=your_key_here
ANTHROPIC_MODEL=your_explicit_model_name
ALEXA_VERIFY_REQUESTS=false
```

Run checks:

```bash
go test ./...
go tool golangci-lint run ./...
```

Health:

```powershell
Invoke-RestMethod http://127.0.0.1:8080/health
```

Dashboard and history are local administrative surfaces:

```text
http://127.0.0.1:8080/
http://127.0.0.1:8080/history
```

Do not publish either route.

## Docker Deployment

```powershell
Copy-Item .env.example .env
notepad .env
docker compose up --build -d
docker compose logs -f brainslug
```

The compose file binds the service to `127.0.0.1:8080` and stores SQLite data under `./data`.

Leave `SQLITE_PATH` unset in `.env` for the provided container configuration. The image already points it at the mounted volume.

See [docs/local-deployment.md](docs/local-deployment.md).

## Alexa Skill Setup

1. Create a custom skill in the Alexa Developer Console.
2. Use a custom interaction model and invocation name `brain slug`.
3. Import `skill/interaction-model.json`.
4. Set the HTTPS endpoint to `https://your-domain.example/alexa`.
5. Record the skill application ID in `ALEXA_ALLOWED_SKILL_ID`.
6. Build and test the model.

The two-turn flow is the least fragile current test:

```text
Alexa, open Brain Slug
Brainslug is ready. What should I ask?
<question>
```

The interaction model still needs the one-shot utterance rewrite and slot-elicitation work described in [docs/invocation-design.md](docs/invocation-design.md).

Full setup instructions are in [docs/alexa-skill-setup.md](docs/alexa-skill-setup.md).

## Public HTTPS Routing

Expose only `POST /alexa` through Caddy or Cloudflare Tunnel. A whole-origin proxy also publishes the unauthenticated dashboard and history feed.

Required public behavior:

```text
POST /alexa  -> Brainslug ASK handler
GET  /       -> 404 from the public hostname
GET  /history-> 404 from the public hostname
GET  /health -> 404 from the public hostname
```

The target architecture moves administrative routes to a separate loopback-only listener so this property is structural rather than dependent on proxy configuration.

## Repository Layout

Current packages:

```text
cmd/brainslug      process lifecycle, routing, and middleware wiring
internal/alexa     ASK request verification, intent routing, and responses
internal/providers provider-specific AI HTTP clients
internal/speech    output shaping for spoken responses
internal/store     SQLite persistence and migrations
internal/config    environment loading and validation
internal/security  HTTP middleware and secret redaction
internal/prompts   provider system instructions
internal/web       local dashboard
skill/             ASK interaction model
```

Planned boundary after the application-core extraction:

```text
internal/app       platform-neutral use cases and job state
internal/alexa     ASK inbound adapter only
internal/providers outbound AI adapters
internal/store     outbound persistence adapter
internal/mcp       future optional MCP inbound adapter, not yet created
```

Package names may move during implementation, but the dependency direction is fixed: inbound adapters depend on application interfaces; the application layer does not import Alexa or MCP protocol types.

## Configuration

The authoritative list is `.env.example`. Important groups are:

- Runtime: `SERVER_ADDR`, `PUBLIC_BASE_URL`, `LOG_LEVEL`.
- Provider selection: `AI_PROVIDER` and the selected provider's model, key, token cap, and timeout.
- Persistence: `SQLITE_PATH`, `SAVE_HISTORY`.
- ASK security: `ALEXA_VERIFY_REQUESTS`, `ALEXA_ALLOWED_SKILL_ID`.

Provider model choice must remain explicit. Brainslug does not silently substitute a different model or provider.

## Security

- Do not expose `/alexa` with verification disabled.
- Do not expose `/`, `/history`, or provider credentials.
- Treat prompts, answers, user identifiers, and saved job state as private data.
- Add rate limits and a spend cap before continuous external use.
- Keep future MCP authentication separate from Alexa request-signature verification.
- Do not send raw Alexa or MCP account identifiers to model providers.

See [docs/security.md](docs/security.md).

## Documentation

- [Architecture](docs/architecture.md): target boundaries, request flows, identity, deferred work, and package migration.
- [Project plan](docs/project-plan.md): ordered implementation phases and exit criteria.
- [Alexa platform constraints](docs/alexa-platform.md): current ASK and Alexa+ MCP assumptions.
- [Invocation design](docs/invocation-design.md): one-shot, two-turn, and deferred-answer voice behavior.
- [Alexa skill setup](docs/alexa-skill-setup.md): private ASK configuration and test matrix.
- [Local deployment](docs/local-deployment.md): Docker, Caddy, Cloudflare Tunnel, and route isolation.
- [Security](docs/security.md): trust boundaries and current blockers.
- [MCP roadmap](docs/mcp-roadmap.md): future-only gates and tool-design rules.
- [Progress checks](docs/progress-checks.md): milestone acceptance checks.

## Roadmap Summary

1. Prove a real Echo round trip through a short-lived controlled tunnel.
2. Extract a transport-neutral application service without changing behavior.
3. Implement Alexa request verification and split public from administrative listeners.
4. Fix invocation phrasing and add deferred jobs for slow reasoning requests.
5. Complete identity scoping, retention, quotas, and operational limits.
6. Add explicit reusable capabilities only where the product actually needs them.
7. Evaluate an MCP adapter only after access and certification gates are real.

The detailed ordering and exit criteria are in [docs/project-plan.md](docs/project-plan.md).
