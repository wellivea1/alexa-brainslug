# Security

Alexa Brainslug is private infrastructure. Treat the endpoint, API key, prompts, and saved responses as sensitive.

There is currently no configuration in which the service both works and is safe to expose. `ALEXA_VERIFY_REQUESTS=true` rejects Alexa along with everything else; `ALEXA_VERIFY_REQUESTS=false` works but leaves an unauthenticated proxy to a paid API key. Read the rest of this document as a description of the target state, not the current one.

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

The dashboard at `/` and the JSON history at `/history` have no authentication. They serve every prompt and every answer the gateway has processed.

They are served by the same `http.ServeMux` as `/alexa`, so a reverse proxy pointed at the service publishes all three. `Caddyfile.example` previously did exactly that; it now routes `/alexa` only and 404s everything else. If you use Cloudflare Tunnel instead, scope the ingress rule the same way - a hostname mapped to `http://127.0.0.1:8080` exposes the history feed.

The structural fix is to serve the dashboard from a second listener bound to `127.0.0.1`, so that no proxy configuration can reach it. That is planned, not done. Until then this is a documented warning, which is the weaker kind of protection.

Verify after any proxy change:

```powershell
Invoke-WebRequest https://your-domain.example/history
```

Expect a 404.

## Provider Secret Handling

Set `OPENAI_API_KEY` through `.env`, Docker secrets, or another local secret manager. Do not commit it. The health endpoint intentionally does not reveal secrets.

You can also set `OPENAI_API_KEY_FILE` to point at a local file containing exactly one non-comment secret value. This is useful for mounted Docker secrets or host-managed key files. When set, it overrides `OPENAI_API_KEY`.

Anthropic uses the same pattern with `ANTHROPIC_API_KEY` and `ANTHROPIC_API_KEY_FILE`.

Provider error text is redacted before it is saved in SQLite history. The dashboard still shows prompts and model answers because that is the point of the local history feature, so treat the database and dashboard as private.

Dynamic responses set no-store and browser hardening headers. If `PUBLIC_BASE_URL` starts with `https://`, the service also emits HSTS for reverse-proxy deployments.

## Rate Limiting

Add rate limiting before serious use. At minimum, limit request rate by IP and Alexa user ID at the reverse proxy or application layer, and add a global daily cap.

Signature verification and rate limiting solve different problems. Verification stops forged requests. It does nothing about a large volume of genuine ones, and every request spends money on an API key that is not the caller's.

## Logging And Retention

Application logs should not include API keys. SQLite history stores prompts, answers, status, errors, model, provider, and latency. Add retention controls before storing sensitive or long-lived data.

## If This Is Ever Published

Publishing to the Alexa Skill Store changes the threat model rather than extending it:

- Every user's questions bill one API key. Without per-user quotas and a global cap, that is an open tap.
- Strangers' prompts land in SQLite on a home machine. History would need per-user scoping and enforced retention, and `SAVE_HISTORY` would need to default off for anyone who is not the operator.
- A privacy policy URL becomes a certification requirement, because the skill collects personal information by voice.
- Home internet uptime becomes the availability commitment.

None of that is built. Treat store publication as a separate decision gated on it.
