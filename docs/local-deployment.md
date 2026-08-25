# Local Deployment

Brainslug runs locally, but the ASK endpoint must be reachable from Amazon's cloud over public HTTPS.

A LAN-only address such as:

```text
http://192.168.1.10:8080/alexa
```

will not work as an Alexa skill endpoint.

## Current Topology

The current process serves all routes on one listener:

```text
127.0.0.1:8080
  /alexa
  /health
  /history
  /
```

Only `/alexa` is intended for a public reverse proxy. The dashboard, history, and health routes are administrative and unauthenticated.

Target topology after the listener split:

```text
public ASK listener
  /alexa only

loopback administrative listener
  /health
  /history
  /

future optional MCP listener
  separate authentication and route
```

Until the split is implemented, public route isolation depends on the reverse proxy configuration.

## Docker Compose

PowerShell:

```powershell
Copy-Item .env.example .env
notepad .env
docker compose up --build -d
```

Check logs:

```powershell
docker compose logs -f brainslug
```

The provided compose file binds the service to `127.0.0.1:8080` on the host and persists SQLite data under `./data`.

Leave `SQLITE_PATH` unset in `.env` for the provided container deployment. The image already sets the database path to the mounted volume. Overriding it with a relative host-oriented path can point the container at an unwritable or unmounted location.

Verify local health:

```powershell
Invoke-RestMethod http://127.0.0.1:8080/health
```

Expected fields include the configured provider and model but no credentials.

## Caddy Reverse Proxy

Use `Caddyfile.example` as the baseline.

Required behavior:

```text
brain-slug.example.com {
    handle /alexa {
        reverse_proxy 127.0.0.1:8080
    }

    handle {
        respond "Not found" 404
    }
}
```

Do not replace this with:

```text
reverse_proxy 127.0.0.1:8080
```

A whole-origin proxy publishes the dashboard and every stored prompt and answer available through `/history`.

Requirements for direct Caddy exposure:

- Public DNS points to the server or router.
- TCP 443 reaches the Caddy host.
- Firewall rules permit only the required traffic.
- TLS certificate issuance succeeds.

Caddy handles normal public certificates automatically when DNS and inbound connectivity are correct.

## Cloudflare Tunnel

Cloudflare Tunnel avoids opening an inbound port. Scope the ingress rule to the ASK path:

```yaml
ingress:
  - hostname: brain-slug.example.com
    path: ^/alexa$
    service: http://127.0.0.1:8080
  - service: http_status:404
```

Do not map the hostname directly to the whole origin:

```yaml
service: http://127.0.0.1:8080
```

That publishes the dashboard and history feed.

Use the HTTPS hostname in the Alexa Developer Console:

```text
https://brain-slug.example.com/alexa
```

## Public Route Verification

After every proxy or tunnel change, test from outside the LAN where possible.

Expected:

```text
POST https://brain-slug.example.com/alexa   -> reaches ASK handler
GET  https://brain-slug.example.com/        -> 404
GET  https://brain-slug.example.com/history -> 404
GET  https://brain-slug.example.com/health  -> 404
```

PowerShell examples:

```powershell
Invoke-WebRequest https://brain-slug.example.com/ -SkipHttpErrorCheck
Invoke-WebRequest https://brain-slug.example.com/history -SkipHttpErrorCheck
Invoke-WebRequest https://brain-slug.example.com/health -SkipHttpErrorCheck
```

All three administrative paths should return 404 from the public hostname.

A 401 or login page from an unrelated edge product is also preferable to exposure, but the documented Brainslug proxy should return 404.

## Request Verification State

Current code has a fail-closed placeholder:

```text
ALEXA_VERIFY_REQUESTS=true
```

rejects all ASK requests.

For a short-lived controlled round-trip only:

```text
ALEXA_VERIFY_REQUESTS=false
ALEXA_ALLOWED_SKILL_ID=<actual skill ID>
```

This is not safe for continuous public operation. Anyone who reaches the route can potentially spend the configured provider key.

Required order:

1. Start the local service.
2. Start the path-scoped public route.
3. Run the real Echo test.
4. Stop or remove the public route immediately.
5. Re-enable fail-closed verification.

After real request verification is implemented and tested, continuous ASK exposure becomes reasonable only with rate limiting and a spend cap.

## NAT Loopback

NAT loopback helps devices inside the home network reach a public hostname. It does not make a LAN-only service reachable from Amazon's cloud.

The relevant path is:

```text
Amazon Alexa cloud
  -> public HTTPS hostname
  -> reverse proxy or tunnel
  -> 127.0.0.1:8080/alexa
```

## Target Listener Split

The public/private split should be structural.

Recommended configuration shape:

```text
ASK_LISTEN_ADDR=127.0.0.1:8080
ADMIN_LISTEN_ADDR=127.0.0.1:8081
```

The exact variable names may differ, but the properties are fixed:

- ASK listener registers only `/alexa`.
- Administrative listener registers `/`, `/history`, and `/health`.
- The administrative listener binds to loopback by default.
- The public proxy cannot reach the administrative listener through a broad path rule.
- Shutdown drains both listeners under a shared bounded lifecycle.

Do not add authentication to the current shared listener and call the structural problem solved. Authentication may still be useful later, but listener separation removes an entire proxy-misconfiguration class.

## Future MCP Deployment

No MCP route exists today and none should be added to the default proxy examples.

If an MCP adapter is later implemented, use separate ingress and authentication:

```text
brain-slug.example.com/alexa -> ASK signature verification
mcp.brain-slug.example.com/mcp -> MCP OAuth and service authentication
127.0.0.1:8081 -> local administration
```

Do not:

- Reuse ASK signature middleware for MCP.
- Put an unauthenticated `/mcp` route beside `/alexa`.
- Assume a hidden hostname is authentication.
- Route the local dashboard through an MCP host.

See `mcp-roadmap.md` and `security.md`.

## Operational Checks

Before a controlled ASK test:

- Provider and model are explicit in `/health`.
- Provider key is loaded and absent from logs.
- SQLite directory is writable and persistent.
- Public hostname routes only `/alexa`.
- `ALEXA_ALLOWED_SKILL_ID` is set.
- Verification state is understood.
- Rate limits exist before any continuous exposure.

After deployment changes:

- Restart the container.
- Confirm `/health` locally.
- Confirm history persistence.
- Confirm public administrative routes return 404.
- Inspect logs for provider or verification failures.
- Confirm the public route was removed after any verification-disabled test.
