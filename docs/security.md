# Security

Alexa Brainslug is private infrastructure. Treat the endpoint, API key, prompts, and saved responses as sensitive.

## Alexa Request Verification

The initial implementation has a fail-closed placeholder for full Alexa request signature verification. With the default:

```text
ALEXA_VERIFY_REQUESTS=true
```

`POST /alexa` rejects requests. For local Alexa console testing only, you can set:

```text
ALEXA_VERIFY_REQUESTS=false
```

Do not expose `/alexa` publicly with verification disabled. A complete implementation should validate:

- Amazon signature certificate chain URL.
- Certificate trust chain and hostname constraints.
- Request body signature.
- Timestamp freshness.
- Expected Alexa skill application ID.

Set `ALEXA_ALLOWED_SKILL_ID` once you know your skill ID.

## HTTPS

Alexa requires HTTPS for custom skill endpoints. Use Caddy, Cloudflare Tunnel, or another reverse proxy/TLS terminator. Do not send Alexa traffic over plain HTTP across the internet.

## Dashboard Exposure

The dashboard at `/` and JSON history at `/history` do not implement authentication in the first version. Keep them bound to localhost or protected behind authentication. Stored prompts and responses may contain private information.

## Provider Secret Handling

Set `OPENAI_API_KEY` through `.env`, Docker secrets, or another local secret manager. Do not commit it. The health endpoint intentionally does not reveal secrets.

You can also set `OPENAI_API_KEY_FILE` to point at a local file containing exactly one non-comment secret value. This is useful for mounted Docker secrets or host-managed key files. When set, it overrides `OPENAI_API_KEY`.

Anthropic uses the same pattern with `ANTHROPIC_API_KEY` and `ANTHROPIC_API_KEY_FILE`.

Future OpenCode gateway credentials should use `OPENCODE_SERVER_PASSWORD` or `OPENCODE_SERVER_PASSWORD_FILE`. OpenCode should stay bound to loopback and should not be reachable directly from Alexa or the public internet.

Provider error text is redacted before it is saved in SQLite history. The dashboard still shows prompts and model answers because that is the point of the local history feature, so treat the database and dashboard as private.

Dynamic responses set no-store and browser hardening headers. If `PUBLIC_BASE_URL` starts with `https://`, the service also emits HSTS for reverse-proxy deployments.

## OpenCode Safety

OpenCode support is only stubbed. If added later, do not expose it directly to Alexa or the public internet. Put strict command boundaries, timeouts, working directory controls, and audit logging around it.

## Rate Limiting

Add rate limiting before serious use. At minimum, limit request rate by IP and Alexa user ID at the reverse proxy or application layer.

## Logging And Retention

Application logs should not include API keys. SQLite history stores prompts, answers, status, errors, model, provider, and latency. Add retention controls before storing sensitive or long-lived data.
