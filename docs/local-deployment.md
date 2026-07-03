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

## Caddy Reverse Proxy

Use `Caddyfile.example` as a starting point:

```text
brain-slug.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

Requirements:

- A DNS record pointing to your home/server public IP.
- Port forwarding for TCP 443 to the Caddy host.
- A firewall rule allowing inbound 443.

Caddy handles TLS certificates automatically for normal public DNS names.

## Cloudflare Tunnel Alternative

Cloudflare Tunnel avoids opening inbound ports. A typical tunnel maps a hostname like `brain-slug.example.com` to:

```text
http://127.0.0.1:8080
```

Use the resulting HTTPS hostname as the Alexa endpoint:

```text
https://brain-slug.example.com/alexa
```

## Why NAT Loopback Is Not Enough

NAT loopback only helps devices inside your own network reach your public hostname. Alexa is not inside your network. The Alexa cloud must be able to reach a publicly routable HTTPS endpoint.

## Security Reminder

Do not expose `/alexa` publicly with `ALEXA_VERIFY_REQUESTS=false`. Do not expose `/` or `/history` publicly without authentication.
