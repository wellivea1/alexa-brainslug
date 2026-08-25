# Alexa Brainslug

Alexa Brainslug is a self-hosted, single-owner Alexa custom skill gateway. It sends a question to an explicitly selected AI provider, speaks a concise answer through an Echo, and stores the complete result locally in SQLite when history is enabled.

The current entry point is a classic Alexa Skills Kit (ASK) custom skill. Alexa is an inbound adapter over a platform-neutral application service; provider clients and SQLite are outbound adapters. Alexa+ MCP remains a gated future option, not a current dependency or deployment target.

Brainslug is not a public chatbot service, an Alexa control API, or a generic MCP relay.

## Current Architecture

```text
Alexa cloud
  -> public HTTPS POST /alexa
  -> ASK request verification
  -> internal/alexa adapter
  -> internal/app service
  -> selected provider adapter
  -> SQLite repository adapter
  -> concise Alexa response + local history

127.0.0.1:8081
  -> dashboard
  -> history JSON
  -> health
```

The public ASK listener and local administrative listener are separate. A broad proxy rule pointed at the ASK port cannot expose the dashboard or history routes because those routes are not registered on that listener.

Future approved MCP client:

```text
MCP client
  -> future authenticated MCP adapter
  -> the same internal/app operations
```

No MCP server is implemented today.

## Implemented

- Transport-neutral `internal/app` service for asking questions and reading the latest saved answer.
- ASK adapter that does not import provider implementations, prompt policy, or concrete store records.
- OpenAI Responses API and Anthropic Messages API clients.
- Stable application error categories and provider error redaction.
- SQLite interaction history with transactional, append-only migrations.
- Namespaced application principals with Alexa history-key compatibility.
- Full ASK web-service verification infrastructure:
  - certificate URL normalization and allowlisting;
  - bounded certificate retrieval and cache;
  - certificate-chain and Alexa signing-name validation;
  - `Signature-256` verification over the exact raw request body;
  - timestamp freshness enforcement;
  - allowed skill application ID enforcement.
- Separate public and administrative HTTP listeners.
- Dialog-based two-turn question capture and natural one-shot carrier phrases.
- Explicit handling of OpenAI completed, incomplete, failed, canceled, queued, and in-progress response states.
- Distinct handling for output-token exhaustion and provider failures.
- Docker Compose, Caddy example, CI workflow, lint configuration, and unit/integration tests.

## Not Yet Implemented Or Validated

- Real Alexa Developer Console and physical Echo validation of a signed request.
- Durable deferred/background jobs for slow reasoning requests.
- Request idempotency and restart recovery.
- Pending, completed, failed, and canceled job status retrieval.
- Per-principal rate limiting, provider concurrency limits, and a global spend cap.
- Retention and deletion controls.
- Public multi-user hosting or Skill Store publication.
- Alexa+ MCP runtime code.

The current provider path is synchronous. Provider timeouts default to six seconds so the service has a chance to return inside Alexa's external request budget. A slow reasoning request can still fail. The proper long-term fix is a durable deferred-job path, not a longer ASK timeout.

Request verification is implemented and unit-tested, but the current branch has not been validated against an actual Amazon-signed request. Keep the public route controlled until that test succeeds. Continuous exposure also requires rate limits and a spend cap.

## Repository Layout

```text
cmd/brainslug      process lifecycle and listener wiring
internal/alexa     ASK verification, request mapping, dialog, and responses
internal/app       platform-neutral question and history use cases
internal/providers outbound AI provider adapters
internal/store     outbound SQLite repository and migrations
internal/speech    model-output shaping for spoken responses
internal/config    environment loading and startup validation
internal/security  HTTP middleware and secret redaction
internal/prompts   provider system instructions
internal/web       loopback-only dashboard
skill/             ASK interaction model
```

Dependency direction:

```text
inbound adapters
  -> internal/app
  -> application-owned provider and repository ports

internal/providers and internal/store
  -> implement those ports
```

See [docs/architecture.md](docs/architecture.md).

## Requirements

- Go 1.26 or newer.
- Docker and Docker Compose for containerized use.
- An API key for the selected provider.
- A public HTTPS endpoint for Alexa testing.
- A private or loopback path to the administrative listener.

Alexa requests originate in Amazon's cloud. A LAN-only URL is not a valid skill endpoint.

## Configuration

Copy the example file:

```powershell
Copy-Item .env.example .env
notepad .env
```

Important defaults:

```text
SERVER_ADDR=127.0.0.1:8080
ADMIN_SERVER_ADDR=127.0.0.1:8081
AI_PROVIDER=openai
OPENAI_MODEL=gpt-5.6-terra
OPENAI_MAX_OUTPUT_TOKENS=25000
OPENAI_TIMEOUT_SECONDS=6
ALEXA_VERIFY_REQUESTS=true
SAVE_HISTORY=true
```

`OPENAI_MAX_OUTPUT_TOKENS` is reasoning and output headroom, not a desired answer length. Reasoning models may consume the same budget before producing visible text.

Set the real skill ID before testing:

```text
ALEXA_ALLOWED_SKILL_ID=amzn1.ask.skill.example
```

Provider and model selection remain explicit. Brainslug does not silently fall back to another provider or model.

## Local Development

PowerShell:

```powershell
Copy-Item .env.example .env
notepad .env
go mod download
go run ./cmd/brainslug
```

OpenAI minimum configuration:

```text
AI_PROVIDER=openai
OPENAI_API_KEY=your_key_here
OPENAI_MODEL=gpt-5.6-terra
```

You may set `OPENAI_API_KEY_FILE` instead of `OPENAI_API_KEY`. The file should contain exactly one non-comment secret value.

Anthropic example:

```text
AI_PROVIDER=anthropic
ANTHROPIC_API_KEY=your_key_here
ANTHROPIC_MODEL=your_explicit_model_name
```

Run local checks:

```bash
go build ./...
go test ./...
go tool golangci-lint run ./...
```

Administrative health:

```powershell
Invoke-RestMethod http://127.0.0.1:8081/health
```

Dashboard and history:

```text
http://127.0.0.1:8081/
http://127.0.0.1:8081/history
```

The ASK listener at port 8080 intentionally has no health, history, or dashboard route.

### Verification-disabled fixture testing

`ALEXA_VERIFY_REQUESTS=false` exists for local fixture tests. Do not combine it with a publicly reachable route.

For actual Alexa testing, use:

```text
ALEXA_VERIFY_REQUESTS=true
ALEXA_ALLOWED_SKILL_ID=<your actual skill ID>
```

## Docker Deployment

```powershell
Copy-Item .env.example .env
notepad .env
docker compose up --build -d
docker compose logs -f brainslug
```

The provided compose file:

- binds container port 8080 to host loopback port 8080 for ASK proxying;
- binds container port 8081 to host loopback port 8081 for administration;
- stores SQLite data under `./data`;
- uses a read-only filesystem outside the data volume;
- drops Linux capabilities and enables `no-new-privileges`.

Leave `SQLITE_PATH` unset in `.env` for the provided container deployment. The image already points it at `/data/brainslug.db`.

See [docs/local-deployment.md](docs/local-deployment.md).

## Alexa Skill Setup

1. Create a custom skill in the Alexa Developer Console.
2. Use a custom interaction model and invocation name `brain slug`.
3. Import `skill/interaction-model.json`.
4. Set the HTTPS endpoint to `https://your-domain.example/alexa`.
5. Record the skill ID in `ALEXA_ALLOWED_SKILL_ID`.
6. Keep `ALEXA_VERIFY_REQUESTS=true`.
7. Build the model and test it in the Developer Console and on the real Echo.

Two-turn flow:

```text
Alexa, open Brain Slug
Brainslug is ready. What should I ask?
<question>
```

One-shot forms represented in the interaction model include:

```text
Alexa, ask Brain Slug about the halting problem
Alexa, ask Brain Slug regarding SQLite write-ahead logging
Alexa, tell Brain Slug to answer this question: why is the sky blue
```

Exact recognition still requires Developer Console and physical-device testing.

See [docs/alexa-skill-setup.md](docs/alexa-skill-setup.md) and [docs/invocation-design.md](docs/invocation-design.md).

## Public HTTPS Routing

Expose only the ASK listener:

```text
POST https://brain-slug.example.com/alexa
  -> 127.0.0.1:8080/alexa
```

Expected public behavior:

```text
POST /alexa   -> ASK handler
GET  /        -> 404
GET  /history -> 404
GET  /health  -> 404
```

The administrative listener stays on host loopback port 8081 and must not be routed publicly.

Use Caddy or a path-scoped Cloudflare Tunnel. `Caddyfile.example` routes only `/alexa`.

## Security

- Leave ASK verification enabled for real requests.
- Set the exact skill ID.
- Do not expose the administrative listener.
- Do not log or commit provider credentials.
- Treat SQLite, prompts, answers, and backups as private data.
- Add rate limits and a global spend cap before continuous public exposure.
- Keep future MCP authentication separate from ASK request verification.
- Do not send raw Alexa account identifiers to providers; the provider adapter receives a derived principal key and hashes it where an end-user identifier is supported.

See [docs/security.md](docs/security.md).

## Documentation

- [Architecture](docs/architecture.md)
- [Project plan](docs/project-plan.md)
- [Alexa platform constraints](docs/alexa-platform.md)
- [Invocation design](docs/invocation-design.md)
- [Alexa skill setup](docs/alexa-skill-setup.md)
- [Local deployment](docs/local-deployment.md)
- [Security](docs/security.md)
- [MCP roadmap](docs/mcp-roadmap.md)
- [Progress checks](docs/progress-checks.md)

## Next Implementation Phases

1. Validate the interaction model and signed request verifier against the Alexa Developer Console and a physical Echo.
2. Add per-principal limits and a global spend cap.
3. Add durable jobs, request idempotency, bounded background workers, and restart recovery.
4. Make `last response` distinguish pending, completed, and failed jobs.
5. Add retention and deletion controls.
6. Re-evaluate MCP only after a concrete host, access path, and domain-specific capability set exist.
