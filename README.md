# Alexa Brainslug

Alexa Brainslug is a self-hosted Alexa custom skill gateway. Alexa sends a captured question to a local Go service, the service calls an explicitly configured AI provider, and Alexa receives a short spoken response. Full responses are stored locally in SQLite for the dashboard and the "last response" intent.

The point is to route a hard question to a model you choose, deterministically, instead of whatever Alexa decides to answer with. Alexa handles the easy things; this handles the ones worth spending reasoning tokens on.

It is not a monetized wrapper, an AWS Lambda sample, or a general chatbot framework.

Store publication is an intended direction, not a current state. It is gated on work that does not exist yet: per-user metering, per-user history scoping and retention, a privacy policy, and somewhere to run it that is not a home internet connection. Until then, treat this as single-owner infrastructure.

## Architecture

```text
Alexa cloud
  -> HTTPS domain, reverse proxy, or tunnel
  -> local Go server
  -> selected provider API
  -> concise Alexa speech response
  -> local SQLite history
```

## Current Status

This is a first working skeleton. Alexa routing, SQLite history, Docker setup, the local dashboard, OpenAI, and Anthropic provider calls are implemented.

**There is currently no configuration in which this both works and is safe to expose.** Full Alexa request signature verification is not implemented. With the default `ALEXA_VERIFY_REQUESTS=true`, `/alexa` rejects everything, including Alexa. With `ALEXA_VERIFY_REQUESTS=false` it works, but the endpoint is an unauthenticated proxy to your paid API key for anyone who finds the hostname. Signature verification is the blocker for everything else.

Other known gaps that will bite before that one is fixed:

- Provider timeouts default to 12 seconds. Alexa gives up at 8. Slow answers produce a spoken error *and* a successful history row.
- `MAX_OUTPUT_TOKENS` covers reasoning/thinking tokens as well as visible text on both providers. At 600 a reasoning model can spend the whole budget thinking and return nothing, which surfaces as "response did not include text output".
- `ANTHROPIC_MODEL` defaults to `claude-sonnet-4-5`, which is legacy. Current models are not a drop-in swap: they run adaptive thinking by default, so raising the token budget has to land first.
- The sample utterances in `skill/interaction-model.json` produce "Alexa, ask brain slug ask what is X". See [docs/invocation-design.md](docs/invocation-design.md).
- `/alexa` has no rate limiting.
- `/` and `/history` have no authentication and must not be routed publicly. See the Caddyfile.

See [docs/project-plan.md](docs/project-plan.md) for the ordering.

## Repository Layout

```text
cmd/brainslug      process lifecycle, routing, middleware wiring
internal/alexa     skill endpoint: verification, intent routing, responses
internal/providers AI backends; one thin HTTP client per provider
internal/speech    shaping model output into text safe to read aloud
internal/store     SQLite history and schema migrations
internal/config    environment loading and startup validation
internal/security  HTTP middleware and secret redaction
internal/prompts   the system instruction sent with every question
internal/web       local development dashboard
skill/             Alexa interaction model
```

Dependencies point inward and never cycle. `internal/alexa` takes an options
struct rather than importing `internal/config`, so the skill logic is testable
without a process environment.

## Requirements

- Go 1.26 or newer
- Docker and Docker Compose for containerized use
- An API key for the selected provider
- A public HTTPS endpoint for Alexa, using either Caddy with port-forwarded 443 or Cloudflare Tunnel

Alexa cannot call a LAN-only URL. It must reach a public HTTPS endpoint.

## Local Development

PowerShell:

```powershell
Copy-Item .env.example .env
notepad .env
go mod download
go run ./cmd/brainslug
```

Build, test, and lint:

```bash
go test ./...
```

```bash
go tool golangci-lint run ./...
```

The linter is pinned as a tool dependency in `go.mod`, so it needs no separate
install and CI runs the same version. See [CONTRIBUTING.md](CONTRIBUTING.md)
for the full loop and the standards it enforces.

Set at least:

```text
OPENAI_API_KEY=your_key_here
OPENAI_MODEL=gpt-5-mini
ALEXA_VERIFY_REQUESTS=false
```

You may set `OPENAI_API_KEY_FILE` instead of `OPENAI_API_KEY` if you prefer to mount or store the key in a local file. The file should contain exactly one non-comment secret value.

To use Anthropic instead:

```text
AI_PROVIDER=anthropic
ANTHROPIC_API_KEY=your_key_here
ANTHROPIC_MODEL=claude-sonnet-4-5
```

Use `ALEXA_VERIFY_REQUESTS=false` only for local testing. Check health:

```powershell
Invoke-RestMethod http://127.0.0.1:8080/health
```

Dashboard:

```text
http://127.0.0.1:8080/
```

History JSON:

```text
http://127.0.0.1:8080/history
```

## Docker Deployment

```powershell
Copy-Item .env.example .env
notepad .env
docker compose up --build -d
docker compose logs -f brainslug
```

The compose file binds `127.0.0.1:8080` and stores SQLite data under `./data`. Leave `SQLITE_PATH` unset in `.env` - the image already points it at the mounted volume, and overriding it sends the database somewhere the container user cannot write.

## Alexa Skill Setup Overview

1. Create a custom skill in the Alexa Developer Console.
2. Use invocation name `brain slug`.
3. Import `skill/interaction-model.json`.
4. Set the endpoint to `https://your-domain.example/alexa`.
5. Build the model.
6. Test: "Alexa, open Brain Slug", then ask a question in the follow-up turn.

The one-shot form currently reads badly - the shipped sample utterances produce "Alexa, ask Brain Slug ask what is SQLite". See [docs/invocation-design.md](docs/invocation-design.md) for why and what to replace them with.

See `docs/alexa-skill-setup.md` for more detail.

## HTTPS And Tunnels

Use one of these:

- Caddy terminating TLS on a public domain and reverse proxying to `127.0.0.1:8080`
- Cloudflare Tunnel routing a public hostname to `http://127.0.0.1:8080`

In either case scope the route to `/alexa` only. `Caddyfile.example` shows this; for Cloudflare Tunnel use a path-scoped ingress rule. Proxying the whole origin publishes the dashboard and the history feed.

NAT loopback or LAN access is not enough because Alexa requests originate from Amazon's cloud.

## Environment Variables

| Name | Purpose |
| --- | --- |
| `SERVER_ADDR` | Local bind address, default `:8080`. |
| `PUBLIC_BASE_URL` | Documentation/reference URL for the public endpoint. |
| `AI_PROVIDER` | Selected provider: `openai` or `anthropic`. |
| `OPENAI_API_KEY` | OpenAI API key. Never commit it. |
| `OPENAI_API_KEY_FILE` | Optional path to a local file containing the OpenAI API key. Overrides `OPENAI_API_KEY` when set. |
| `OPENAI_MODEL` | Required explicit model choice, for example `gpt-5-mini`. |
| `OPENAI_BASE_URL` | Optional OpenAI-compatible base URL. |
| `OPENAI_MAX_OUTPUT_TOKENS` | Output cap sent to the provider. |
| `OPENAI_TIMEOUT_SECONDS` | Provider HTTP timeout. |
| `ANTHROPIC_API_KEY` | Anthropic API key. Never commit it. |
| `ANTHROPIC_API_KEY_FILE` | Optional path to a local file containing the Anthropic API key. |
| `ANTHROPIC_MODEL` | Required explicit Anthropic model when `AI_PROVIDER=anthropic`. |
| `ANTHROPIC_BASE_URL` | Optional Anthropic-compatible base URL. |
| `ANTHROPIC_MAX_OUTPUT_TOKENS` | Anthropic output cap. |
| `ANTHROPIC_TIMEOUT_SECONDS` | Anthropic HTTP timeout. |
| `SQLITE_PATH` | SQLite database path. Leave unset unless you know whether you are configuring the local or the Docker case - see `.env.example`. |
| `ALEXA_VERIFY_REQUESTS` | Fail-closed verification toggle. Defaults to `true`. |
| `ALEXA_ALLOWED_SKILL_ID` | Optional expected Alexa skill application ID. |
| `LOG_LEVEL` | `debug`, `info`, `warn`, or `error`. |
| `SAVE_HISTORY` | Whether to save local interaction history. |

## Security Warnings

- Do not expose `/alexa` publicly without real Alexa request verification.
- Route only `/alexa` through the reverse proxy. `/` and `/history` serve every prompt and answer with no authentication; a bare `reverse_proxy` to the service publishes them.
- Do not expose the provider API key.
- Provider errors are redacted before local history storage, but prompts and answers are intentionally stored as local history.
- Put the app behind HTTPS.
- Add rate limiting before serious use. With no verification and no rate limit, an exposed endpoint is a metered API key someone else can spend.
- Treat stored prompts and responses as private data.

See `docs/security.md`.

## Roadmap

See [docs/project-plan.md](docs/project-plan.md) for detail and exit criteria.

1. Deferred answers, so a slow reasoning model does not mean a spoken error.
2. Token budget and reasoning-effort configuration.
3. Sample utterances and slot elicitation ([docs/invocation-design.md](docs/invocation-design.md)).
4. Full Alexa request signature verification.
5. Split the public `/alexa` listener from the loopback-only dashboard listener.
6. Rate limiting for `/alexa`, by IP and Alexa user ID.
7. Per-user history scoping and retention controls.

OpenCode was considered as a provider and dropped. It was only ever an implementation reference for calling an agent API; as a provider it would be a slower, more expensive path to the same chat completion.
