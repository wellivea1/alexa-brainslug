<p align="center">
  <img src="skill/assets/brainslug-icon-512.png" width="160" alt="Brainslug icon">
</p>

<h1 align="center">Brainslug</h1>

<p align="center">
  <strong>A friendly intelligence-enhancing parasite for Alexa.</strong><br>
  Ask out loud. Choose the brain. Keep the memory at home.
</p>

<p align="center">
  <a href="go.mod"><img alt="Go 1.26" src="https://img.shields.io/badge/Go-1.26-00ADD8?style=flat-square&logo=go&logoColor=white"></a>
  <a href="LICENSE"><img alt="Proprietary license" src="https://img.shields.io/badge/license-proprietary-555?style=flat-square"></a>
  <img alt="Self-hosted" src="https://img.shields.io/badge/runtime-self--hosted-2F855A?style=flat-square">
</p>

> **You:** "Alexa, ask Brain Slug why the sky is blue."
>
> **Brainslug:** asks the provider you selected, speaks a concise answer, and
> can save the complete exchange locally.

Brainslug is a self-hosted, single-owner Alexa custom skill gateway. Alexa is
the current entry point, but the application core is platform-neutral. It is
not a public chatbot, an Alexa control API, or an MCP relay.

## What it does

- Verifies Amazon ASK certificates, signatures, timestamps, and the exact skill
  ID before accepting a request.
- Supports two-turn questions, natural one-shot questions, and `last response`.
- Connects to OpenAI, OpenAI-compatible servers, or Anthropic.
- Optionally connects a ChatGPT Plus/Pro account with browser or code login.
- Speaks a short answer while retaining the complete result in local SQLite.
- Provides a private dashboard for activity, provider, model, and credential
  management.
- Keeps the public ASK listener separate from the private admin listener.

The interaction model builds in the Alexa Developer Console, signed requests
pass verification, and a beta-enrolled Echo has completed the launch and
follow-up question flow.

## How it fits together

```text
Echo or Alexa app
  -> Alexa cloud
  -> public HTTPS POST /alexa
  -> ASK request verification
  -> application service
  -> selected AI provider
  -> concise spoken response

application service
  -> local SQLite history

owner
  -> private dashboard on 127.0.0.1:8081
```

| Listener | Purpose | Exposure |
| --- | --- | --- |
| `127.0.0.1:8080` | `POST /alexa` only | Path-scoped HTTPS proxy |
| `127.0.0.1:8081` | Owner-protected dashboard, history, detailed health | Host loopback only |

> [!IMPORTANT]
> Never expose port `8081`. It contains private prompts, answers, and provider
> settings. The owner login is defense in depth for a local/private management
> path, not permission to publish it to the internet.

See [Architecture](docs/architecture.md) for the adapter boundaries and
listener design.

## Quick start

Requirements:

- Go 1.26 or newer for manual setup. The Windows installer can provision it.
- An Alexa custom skill and a public HTTPS endpoint.
- A credential for the selected provider, or an optional ChatGPT account.

### Windows setup

On a bare Windows machine, sign in to this private repository, choose
**Code -> Download ZIP**, extract it, then run:

```powershell
Set-Location "path\to\alexa-brainslug-main"
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\install.ps1
```

Or, from a cloned checkout, run:

```powershell
.\install.ps1
```

An extracted ZIP does not require Git, `winget`, administrator rights, or a
preinstalled Go toolchain. A copied standalone `install.ps1` can instead
bootstrap to `%LOCALAPPDATA%\Brainslug`; private-repository access is read from
the temporary `BRAINSLUG_GITHUB_TOKEN` environment variable and is never saved.
When Go is missing, the installer offers a user-local copy verified against the
SHA-256 published by go.dev. It runs the tests, builds `bin\brainslug.exe`,
creates `.env` when needed, and asks whether Brainslug should start
automatically when you sign in. The recommended startup option uses a per-user
Scheduled Task with no stored password.

For unattended setup:

```powershell
.\install.ps1 -NonInteractive -Startup Enable -InstallGo
```

Update an installed checkout with:

```powershell
.\update.ps1
```

The updater uses a clean fast-forward for Git checkouts or a hashed source
manifest for standalone installs. Downloads are retried into staging files, so
an interrupted transfer cannot replace the installed source or binary. It
builds and tests before stopping the current process, keeps the previous binary
for rollback, and preserves `.env`, SQLite, settings, and credentials. See
[Windows install and updates](docs/windows-install.md) for startup rationale,
options, logs, and recovery.

### Manual setup

Prepare the environment:

```powershell
Copy-Item .env.example .env
notepad .env
```

Set the real public URL, skill ID, provider, model, and credential. A minimal
OpenAI example looks like this:

```dotenv
PUBLIC_BASE_URL=https://brain-slug.example.com
ALEXA_VERIFY_REQUESTS=true
ALEXA_ALLOWED_SKILL_ID=amzn1.ask.skill.your-real-id

AI_PROVIDER=openai
OPENAI_AUTH_MODE=api_key
OPENAI_MODEL=gpt-5.6-terra
OPENAI_API_KEY=your-key-here
```

Start Brainslug:

```powershell
go mod download
go run ./cmd/brainslug
```

Check the private listener:

```powershell
Invoke-RestMethod http://127.0.0.1:8081/healthz
Start-Process http://127.0.0.1:8081/
```

The first dashboard visit opens initial setup. Create the single owner account
there; the dashboard, detailed `/health`, history, and provider controls require
that login afterward. Sessions expire after 30 minutes idle and are cleared on
restart.

The full configuration surface is documented in
[`.env.example`](.env.example). Provider and model selection are always
explicit; Brainslug does not silently fall back or substitute a model.

## Providers

| Provider | Credential | Notes |
| --- | --- | --- |
| OpenAI | API key or secret file | Official Responses API |
| OpenAI account | ChatGPT login | Optional account-scoped connector |
| OAI Compat. | API key or local token | Explicit compatible base URL |
| Anthropic | Console API key or secret file | Direct Claude Messages API |

To connect a ChatGPT account, open the dashboard and choose
**Inference -> OpenAI -> Connect**. Browser login uses a temporary callback on
`localhost:1455`; do not forward that port. Use **Use code** when the browser
cannot reach the Brainslug host, including many Docker or remote-shell setups.

> [!NOTE]
> The ChatGPT connector follows the Codex account flow. It is
> separate from the documented OpenAI API-key contract, and available models
> depend on the account.

OpenAI-compatible settings are independent from official OpenAI settings.
Anthropic uses an Anthropic Console API key; a Claude Pro or Max subscription
login is not an API credential.

## Alexa setup

1. Create a **Custom** skill in the
   [Alexa Developer Console](https://developer.amazon.com/alexa/console/ask).
2. Name it **Brainslug** and use the invocation name `brain slug`.
3. Import [`skill/interaction-model.json`](skill/interaction-model.json).
4. Set the HTTPS endpoint to `https://your-domain.example/alexa`.
5. Copy the skill ID into `ALEXA_ALLOWED_SKILL_ID`.
6. Keep `ALEXA_VERIFY_REQUESTS=true`.
7. Build the model and enroll the intended Alexa account in the beta.

```text
You:       Alexa, open Brain Slug
Brainslug: Brainslug is ready. What should I ask?
You:       Why do databases use write-ahead logs?
```

One-shot forms include:

```text
Alexa, ask Brain Slug about the halting problem
Alexa, ask Brain Slug regarding SQLite write-ahead logging
Alexa, tell Brain Slug to answer this question: why is the sky blue
```

See [Alexa skill setup](docs/alexa-skill-setup.md) for endpoint, beta, and
device troubleshooting.

## Public routing

Alexa requests originate in Amazon's cloud, so a LAN-only URL is not enough.
Expose only this route:

```text
POST https://brain-slug.example.com/alexa
  -> 127.0.0.1:8080/alexa
```

The included [`Caddyfile.example`](Caddyfile.example) provides a path-scoped
example. Public requests to `/`, `/health`, and `/history` should return `404`.

For Docker:

```powershell
docker compose up --build -d
docker compose logs -f brainslug
```

The provided Compose file publishes both service ports back to host loopback,
stores data under `./data`, uses a read-only filesystem outside that volume,
and drops Linux capabilities. See [Local deployment](docs/local-deployment.md)
for native, Docker, Caddy, and tunnel layouts.

## Local data

| File | Contents |
| --- | --- |
| `brainslug.db` | Prompts, complete answers, status, and timing |
| `brainslug.settings.json` | Non-secret dashboard settings |
| `brainslug.provider-auth.json` | Saved provider API keys |
| `brainslug.openai-auth.json` | Refreshable ChatGPT account credentials |
| `brainslug.admin-auth.json` | Management owner name and salted password hash |

Treat the database, auth files, and backups as private. Paths can be changed
with `SQLITE_PATH`, `RUNTIME_SETTINGS_PATH`, `PROVIDER_AUTH_FILE`,
`OPENAI_CHATGPT_AUTH_FILE`, and `ADMIN_AUTH_FILE`.

The management security review and residual operating requirements are in
[`docs/security-audit.md`](docs/security-audit.md).

## Development

```bash
go build ./...
go vet ./...
go test ./...
go tool golangci-lint run ./...
```

The linter is pinned in `go.mod`; no global installation is required. See
[`CONTRIBUTING.md`](CONTRIBUTING.md) for architecture boundaries, testing
conventions, and the full local CI loop.

## Current limits

Brainslug does not yet provide durable background jobs, restart recovery,
request idempotency, rate or concurrency limits, a global spend cap, or history
retention controls. The synchronous provider timeout defaults to six seconds.

Add operational limits before continuous unattended exposure. Longer ASK
timeouts are not a substitute for a persisted deferred-job path.

## Documentation

- [Architecture](docs/architecture.md)
- [Alexa skill setup](docs/alexa-skill-setup.md)
- [Invocation design](docs/invocation-design.md)
- [Local deployment](docs/local-deployment.md)
- [Windows install and updates](docs/windows-install.md)
- [Security](docs/security.md)
- [Project plan](docs/project-plan.md)
- [Progress checks](docs/progress-checks.md)
- [MCP roadmap](docs/mcp-roadmap.md)

