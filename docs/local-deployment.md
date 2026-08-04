# Local Deployment

Alexa still needs a public HTTPS endpoint even though the gateway runs locally. A LAN-only address such as `http://192.168.1.10:8080/alexa` will not work because Alexa requests come from Amazon's cloud.

## Docker Compose

```powershell
Copy-Item .env.example .env
notepad .env
docker compose up --build -d
```

Check logs:

```powershell
docker compose logs -f brainslug
```

The service listens on `127.0.0.1:8080` on the host and stores SQLite data in `./data`.

Leave `SQLITE_PATH` unset in `.env`. The image points it at `/data/brainslug.db`, which is the mounted volume. Compose passes every variable in `.env` through to the container and those override the image's own values, so an uncommented `SQLITE_PATH=./brainslug.db` resolves to `/app/brainslug.db` - a directory the unprivileged container user cannot write. The service then fails to start, and the mounted volume goes unused.

## Caddy Reverse Proxy

Use `Caddyfile.example` as a starting point. It routes `/alexa` and 404s everything else:

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

Do not replace this with a bare `reverse_proxy 127.0.0.1:8080`. The same server also answers `/` and `/history`, both unauthenticated, both containing every prompt and answer that has gone through the gateway.

Requirements:

- A DNS record pointing to your home/server public IP.
- Port forwarding for TCP 443 to the Caddy host.
- A firewall rule allowing inbound 443.

Caddy handles TLS certificates automatically for normal public DNS names.

## Cloudflare Tunnel Alternative

Cloudflare Tunnel avoids opening inbound ports. Scope the ingress rule to the Alexa path rather than mapping the whole origin:

```yaml
ingress:
  - hostname: brain-slug.example.com
    path: ^/alexa$
    service: http://127.0.0.1:8080
  - service: http_status:404
```

A hostname mapped straight to `http://127.0.0.1:8080` publishes the dashboard and the history feed along with the skill endpoint.

Use the resulting HTTPS hostname as the Alexa endpoint:

```text
https://brain-slug.example.com/alexa
```

Verify the scoping after any change - `https://brain-slug.example.com/history` should return 404, not JSON.

## Why NAT Loopback Is Not Enough

NAT loopback only helps devices inside your own network reach your public hostname. Alexa is not inside your network. The Alexa cloud must be able to reach a publicly routable HTTPS endpoint.

## Security Reminder

Do not expose `/alexa` publicly with `ALEXA_VERIFY_REQUESTS=false`. Since signature verification is not implemented yet, that means not exposing it publicly at all - see `security.md`.

Never route `/` or `/history` publicly. They have no authentication and serve the full prompt and answer history.
