# Local Deployment

Brainslug runs locally, but Alexa must reach the ASK listener over public HTTPS. The administrative listener remains on loopback.

## Current Topology

```text
Amazon Alexa cloud
  -> https://brain-slug.example.com/alexa
  -> reverse proxy or Cloudflare Tunnel
  -> host 127.0.0.1:8080
  -> container :8080
  -> POST /alexa only

operator browser or local tooling
  -> host 127.0.0.1:8081
  -> container :8081
  -> /health, /history, /
```

Default host-side configuration:

```text
SERVER_ADDR=127.0.0.1:8080
ADMIN_SERVER_ADDR=127.0.0.1:8081
```

The two listeners are structurally separate. The public listener has no dashboard, history, or health route.

A LAN-only address such as:

```text
http://192.168.1.10:8080/alexa
```

is not a valid Alexa endpoint because Alexa requests originate in Amazon's cloud.

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

The provided compose file overrides the in-container addresses:

```text
SERVER_ADDR=:8080
ADMIN_SERVER_ADDR=:8081
```

and maps both ports back to host loopback:

```text
127.0.0.1:8080:8080
127.0.0.1:8081:8081
```

SQLite data is stored under `./data`.

Leave `SQLITE_PATH` unset in `.env` for the provided container deployment. The image already points it at `/data/brainslug.db`. A host-oriented relative override can send the container to an unwritable or unmounted path.

Administrative health:

```powershell
Invoke-RestMethod http://127.0.0.1:8081/health
```

Dashboard and history:

```text
http://127.0.0.1:8081/
http://127.0.0.1:8081/history
```

The public port should return 404 for those paths:

```powershell
Invoke-WebRequest http://127.0.0.1:8080/ -SkipHttpErrorCheck
Invoke-WebRequest http://127.0.0.1:8080/history -SkipHttpErrorCheck
Invoke-WebRequest http://127.0.0.1:8080/health -SkipHttpErrorCheck
```

## Caddy Reverse Proxy

Use `Caddyfile.example` as the baseline:

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

A broad reverse proxy to port 8080 no longer reaches administrative routes, but the path-scoped rule still reduces the exposed surface and documents intent.

Do not route port 8081 publicly.

Requirements for direct Caddy exposure:

- public DNS points to the server or router;
- TCP 443 reaches the Caddy host;
- firewall rules permit the required traffic;
- certificate issuance succeeds.

## Cloudflare Tunnel

A path-scoped ingress rule avoids opening an inbound port:

```yaml
ingress:
  - hostname: brain-slug.example.com
    path: ^/alexa$
    service: http://127.0.0.1:8080
  - service: http_status:404
```

Use this endpoint in the Alexa Developer Console:

```text
https://brain-slug.example.com/alexa
```

Do not add an ingress rule for port 8081.

## ASK Verification

For real Alexa requests:

```text
ALEXA_VERIFY_REQUESTS=true
ALEXA_ALLOWED_SKILL_ID=<actual skill ID>
```

The verifier checks:

- signing-certificate URL constraints;
- certificate trust and Alexa signing name;
- SHA-256 signature over the exact body;
- timestamp freshness;
- skill application ID.

The verifier is unit-tested but has not yet been validated against a real Amazon-signed request in this environment. Use a controlled initial deployment and inspect failures locally.

`ALEXA_VERIFY_REQUESTS=false` is only for local fixture tests with no public route. Do not use it as an operational mode.

## Public Route Verification

After every proxy or tunnel change, test from outside the LAN where practical.

Expected:

```text
POST https://brain-slug.example.com/alexa   -> ASK handler
GET  https://brain-slug.example.com/        -> 404
GET  https://brain-slug.example.com/history -> 404
GET  https://brain-slug.example.com/health  -> 404
```

Also confirm that the administrative interface remains reachable only from the intended local host or private management path.

## NAT Loopback

NAT loopback helps devices inside the network reach a public hostname. It does not make a LAN-only service reachable from Amazon.

The relevant path is:

```text
Amazon Alexa cloud
  -> public HTTPS hostname
  -> reverse proxy or tunnel
  -> 127.0.0.1:8080/alexa
```

## Security State

The listener split and ASK verifier remove the two largest original exposure defects, but continuous operation still needs:

- per-principal rate limits;
- bounded provider concurrency;
- a global request or spend cap;
- real Amazon-signed request validation;
- retention appropriate to the stored prompts and answers.

Until those exist, keep the deployment private and monitored rather than treating it as a public service.

## Future MCP Deployment

No MCP route exists today.

A future authenticated MCP adapter must use separate ingress and authentication, for example:

```text
brain-slug.example.com/alexa -> ASK signature verification
mcp.brain-slug.example.com/mcp -> MCP OAuth and service authentication
127.0.0.1:8081 -> local administration
```

Do not place an unauthenticated `/mcp` route beside `/alexa`, and do not reuse ASK verification settings for MCP.

See `mcp-roadmap.md` and `security.md`.

## Operational Checklist

Before an Alexa test:

- [ ] Provider and model are explicit in administrative `/health`.
- [ ] Provider key is loaded and absent from logs.
- [ ] SQLite directory is writable and persistent.
- [ ] Public hostname routes only `/alexa`.
- [ ] Port 8081 is not public.
- [ ] `ALEXA_ALLOWED_SKILL_ID` is set.
- [ ] `ALEXA_VERIFY_REQUESTS=true`.
- [ ] The interaction model builds in the Developer Console.

Before continuous use:

- [ ] Real signed requests pass the verifier.
- [ ] Invalid and stale requests fail before provider use.
- [ ] Rate limits exist.
- [ ] Provider concurrency is bounded.
- [ ] A global cap prevents uncontrolled spend.
- [ ] Retention is configured.
