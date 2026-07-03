# Alexa Brainslug

Alexa Brainslug is a private, self-hosted Alexa custom skill gateway. Alexa sends a captured question to a local Go service, the service calls an explicitly configured AI provider, and Alexa receives a short spoken response. Full responses are stored locally in SQLite for the dashboard and the "last response" intent.

This is not a public Alexa Skill Store product, a monetized wrapper, an AWS Lambda sample, or a general chatbot framework.

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

This is a first working skeleton. Alexa routing, SQLite history, Docker setup, the local dashboard, OpenAI, and Anthropic provider calls are implemented. OpenCode is selectable in the provider layer but intentionally still fails closed until the Go API integration is built.

Important: full Alexa request signature verification is not implemented yet. The `/alexa` endpoint fails closed by default with `ALEXA_VERIFY_REQUESTS=true`. For local Alexa console testing only, set `ALEXA_VERIFY_REQUESTS=false` and keep the endpoint private or otherwise tightly controlled.

## Requirements

- Go 1.26 or newer
- Docker and Docker Compose for containerized use
- An API key for the selected hosted provider, or a local OpenCode service once that provider is implemented
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

The compose file binds `127.0.0.1:8080` and stores SQLite data under `./data`.

## Alexa Skill Setup Overview

1. Create a custom skill in the Alexa Developer Console.
2. Use invocation name `brain slug`.
3. Import `skill/interaction-model.json`.
4. Set the endpoint to `https://your-domain.example/alexa`.
5. Build the model.
6. Test: "Alexa, open Brain Slug" and "Alexa, ask Brain Slug ask what is SQLite?"

See `docs/alexa-skill-setup.md` for more detail.

## HTTPS And Tunnels

Use one of these:

- Caddy terminating TLS on a public domain and reverse proxying to `127.0.0.1:8080`
- Cloudflare Tunnel routing a public hostname to `http://127.0.0.1:8080`

NAT loopback or LAN access is not enough because Alexa requests originate from Amazon's cloud.

## Environment Variables

| Name | Purpose |
| --- | --- |
| `SERVER_ADDR` | Local bind address, default `:8080`. |
| `PUBLIC_BASE_URL` | Documentation/reference URL for the public endpoint. |
| `AI_PROVIDER` | Selected provider: `openai`, `anthropic`, or `opencode`. |
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
| `OPENCODE_BASE_URL` | Future local OpenCode Go API endpoint. Keep it loopback-only. |
| `OPENCODE_SERVER_PASSWORD` | Future OpenCode gateway password, if required. |
| `OPENCODE_SERVER_PASSWORD_FILE` | Optional path to a local file containing the OpenCode password. |
| `OPENCODE_MODEL` | Required explicit OpenCode model when `AI_PROVIDER=opencode`. |
| `SQLITE_PATH` | SQLite database path. |
| `ALEXA_VERIFY_REQUESTS` | Fail-closed verification toggle. Defaults to `true`. |
| `ALEXA_ALLOWED_SKILL_ID` | Optional expected Alexa skill application ID. |
| `LOG_LEVEL` | `debug`, `info`, `warn`, or `error`. |
| `SAVE_HISTORY` | Whether to save local interaction history. |

## Security Warnings

- Do not expose `/alexa` publicly without real Alexa request verification.
- Do not expose the dashboard publicly without authentication.
- Do not expose the OpenAI API key.
- Provider errors are redacted before local history storage, but prompts and answers are intentionally stored as local history.
- Do not expose OpenCode directly if it is added later.
- Put the app behind HTTPS.
- Add rate limiting before serious use.
- Treat stored prompts and responses as private data.

See `docs/security.md`.

## Roadmap

- Implement full Alexa request signature verification.
- Implement OpenCode Go API provider behind the gateway.
- Add authenticated dashboard access.
- Add rate limiting for `/alexa`.
- Add retention controls for local history.
