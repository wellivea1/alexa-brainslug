# Management Security Audit

Audit date: 2026-08-26

Scope: the public Alexa listener, local management listener, first-run setup,
credentials, browser sessions, request forgery controls, storage, logging, and
native/Docker deployment boundaries.

## Findings

| Severity | Finding | Resolution |
| --- | --- | --- |
| Critical | The management dashboard, detailed health response, history, and provider credential mutations required no login. Any process or user able to reach port 8081 could read prompts or change paid-provider credentials. | Fixed. Every private management route now requires a valid owner session. |
| High | An unconfigured management service could be claimed by the first reachable client. | Fixed for supported deployments. Native setup requires loopback Host and source addresses. Compose explicitly trusts only its container proxy while publishing the host port on `127.0.0.1`. |
| High | There was no durable management identity or password storage contract. | Fixed. First-run setup creates one owner and stores only a uniquely salted PBKDF2-HMAC-SHA256 hash using 600,000 iterations. The credential file is owner-only where the platform supports modes and is ignored by Git. |
| High | Management sessions did not exist, so logout, expiry, fixation resistance, and cookie policy were absent. | Fixed. Sessions use 256-bit random tokens, store only token digests in memory, expire after 30 minutes idle or eight hours absolute, are capped, and use `HttpOnly` and `SameSite=Strict`. `Secure` is set for HTTPS requests. Restart invalidates all sessions. |
| Medium | Login guessing had no throttling because login did not exist. | Fixed. Failed logins are limited independently by normalized username and source address, with generic errors and a temporary lockout. |
| Medium | `/health` exposed provider/model configuration without authentication and was also required by installers and container health checks. | Fixed. Detailed `/health` is authenticated. Minimal unauthenticated `/healthz` reports only service liveness. |
| Medium | CSRF protection covered provider mutations but not account lifecycle operations. | Fixed. Setup, login, logout, and provider mutations require a cryptographic form token and validated same-origin request. |
| Low | OAuth, provider API key, and new owner credential filenames were not all covered by the original ignore rule. | Fixed. Each concrete credential suffix is ignored explicitly. |

## Controls Verified

- The public listener registers only `POST /alexa`; management, setup, login,
  history, health, and assets remain absent there.
- Alexa requests retain signing-certificate validation, signature validation,
  timestamp freshness, and exact skill-ID enforcement.
- Private management authorization is enforced by server middleware, not by
  navigation visibility or JavaScript.
- Setup ownership cannot be repeated after the credential file is created.
- Passwords are bounded before derivation and never written to responses,
  logs, runtime settings, or credential files.
- Login errors do not distinguish a missing username from a wrong password.
- Session cookies are host-only, path-scoped, `HttpOnly`, and
  `SameSite=Strict`; no session identifier enters HTML, URLs, local storage, or
  logs.
- Logout invalidates the server-side session before clearing the cookie.
- HTML templates continue to contextually escape stored prompts and answers;
  CSP limits scripts and resources to the management origin.
- State changes remain POST-only and retain origin and CSRF verification.
- Provider diagnostics and secrets retain existing redaction boundaries.
- Native and Docker health checks use the non-sensitive `/healthz` endpoint.

## Residual Risks And Operating Requirements

- Port 8081 must remain host-loopback or behind a private authenticated HTTPS
  management proxy. The built-in login is defense in depth, not permission to
  publish the dashboard to the internet.
- Plain HTTP is acceptable only on loopback. A remote management proxy must
  terminate HTTPS so session cookies receive the `Secure` attribute.
- `ADMIN_SETUP_TRUST_PROXY=true` is intended only for the provided Compose
  topology whose host port is bound to `127.0.0.1`. Do not enable it behind an
  untrusted or publicly reachable proxy.
- There is one local owner, no MFA, and no web password-reset path. The offline
  recovery procedure is to stop Brainslug, remove the configured
  `ADMIN_AUTH_FILE`, restart, and complete loopback setup again. File access is
  therefore equivalent to account recovery authority.
- Host filesystems and backups remain inside the trust boundary. They contain
  prompts, answers, provider keys, OAuth refresh tokens, and the password hash.
- Public ASK rate limits, provider concurrency limits, global spend caps, and
  history retention remain separate open operational risks documented in
  `security.md`; management login does not mitigate them.

Password and session parameters follow the OWASP
[Password Storage](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html),
[Authentication](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html),
[Session Management](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html),
and [CSRF Prevention](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html)
cheat sheets current at the audit date.
