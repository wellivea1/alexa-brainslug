# Security

Alexa Brainslug accepts internet-originated requests, can spend money through provider credentials, and stores private prompts and answers. Treat it as a paid authenticated application, not as a harmless home webhook.

## Current Security State

Implemented:

- ASK request signature verification.
- Amazon signing-certificate URL allowlisting and canonicalization.
- Bounded certificate retrieval, redirect validation, and cache.
- Certificate-chain and Alexa signing-name validation.
- Request timestamp freshness enforcement.
- Exact skill application ID enforcement.
- HTTP 400 rejection before application or provider execution.
- Separate public ASK and loopback administrative listeners.
- Request-body caps, HTTP server timeouts, and browser hardening headers.
- Provider error redaction.
- Derived and hashed provider end-user identifiers.
- Loopback Docker port mappings and a read-only container filesystem outside the data volume.

Still required before treating the service as continuously public:

- per-principal and network rate limits;
- bounded provider concurrency;
- a global request or spend cap;
- retention and deletion controls;
- operational monitoring and incident procedures.

`ALEXA_VERIFY_REQUESTS=true` is the default and must remain enabled for real Alexa traffic.

`ALEXA_VERIFY_REQUESTS=false` is only for local fixtures when no public route exists.

## Trust Boundaries

```text
Alexa device
  -> Amazon Alexa cloud
  -> public reverse proxy or tunnel
  -> ASK verification boundary
  -> ASK adapter
  -> application principal and policy boundary
  -> provider and persistence adapters

loopback operator
  -> administrative listener
  -> dashboard, health, and history

future MCP client
  -> separate MCP authentication boundary
  -> future MCP adapter
  -> the same application principal and policy boundary
```

ASK and future MCP authentication are separate systems and must not share a single verification switch or identity namespace.

## ASK Request Verification

The verifier processes the exact raw request body and performs these checks:

1. Read `SignatureCertChainUrl` and `Signature-256`.
2. Canonicalize the certificate URL by removing dot segments, duplicate slashes, and fragments.
3. Require HTTPS, host `s3.amazonaws.com`, port 443, and the Alexa `/echo.api/` path prefix.
4. Reject credentials, invalid ports, and any canonical path outside the allowed certificate location.
5. Fetch the certificate with bounded time and response size.
6. Parse the PEM chain.
7. Verify the chain against trusted roots.
8. Require the Alexa signing name `echo-api.amazon.com`.
9. Base64-decode the signature.
10. Verify the RSA SHA-256 signature over the exact raw body.
11. Parse and enforce request timestamp freshness.
12. Enforce `ALEXA_ALLOWED_SKILL_ID`.

Failure behavior:

- return HTTP 400;
- log a local stable error;
- do not call the application service;
- do not write history;
- do not reveal which public check failed.

Unit tests generate local certificate chains and signed bodies so the verifier can be tested without Amazon network access.

### Runtime validation

The verifier has accepted Amazon-signed Developer Console requests with certificate retrieval, chain validation, timestamp checking, and exact skill-ID enforcement enabled. A beta-enrolled physical Echo has also completed the launch and question flow. Generated-certificate unit tests continue to cover negative cases.

Do not disable verification to work around a real-request incompatibility. Capture a sanitized failure, identify the mismatch, and add a regression test.

## Skill ID

Set:

```text
ALEXA_ALLOWED_SKILL_ID=amzn1.ask.skill.example
```

This prevents another Alexa developer from configuring a different skill to call the same endpoint. It is required in addition to request-signature verification.

When request verification is enabled, a blank allowed skill ID is rejected at startup and by the verifier itself. Verification-disabled local fixtures may omit it because they are never exposed publicly.

## Public And Administrative Listeners

The process uses two listeners:

```text
SERVER_ADDR
  POST /alexa only

ADMIN_SERVER_ADDR
  GET /health
  GET /history
  GET /assets/dashboard.css
  GET /
  POST /settings/provider
  POST /auth/openai/browser
  POST /auth/openai/device/start
  POST /auth/openai/device/poll
  POST /auth/openai/disconnect
```

Default host addresses:

```text
SERVER_ADDR=127.0.0.1:8080
ADMIN_SERVER_ADDR=127.0.0.1:8081
```

Docker binds both back to host loopback. The reverse proxy or tunnel reaches port 8080 only.

The administrative listener contains private prompts and answers and has no remote-user authentication. Do not publish port 8081. An explicit non-loopback `ADMIN_SERVER_ADDR` is permitted for container networking and must be protected by the deployment topology, such as Docker's host-loopback port mapping.

Provider-setting changes use a per-process CSRF token and reject cross-origin
browser submissions. These are defense-in-depth controls for a loopback-only
surface, not remote-user authentication.

The public hostname should return 404 for:

```text
/
/history
/health
```

## Application Authorization

Every saved-data application operation uses a `Principal` with a namespace and subject.

Current ASK behavior:

- Alexa user ID becomes the subject in namespace `alexa`.
- A missing user ID is rejected rather than placed in a shared anonymous bucket.
- Latest-answer lookup is principal-scoped.
- The SQLite adapter preserves old Alexa storage keys for compatibility.
- The provider adapter derives a namespaced key before the provider client hashes it.

Future requirements:

- migrate stored Alexa keys to an explicit namespace;
- require principal scope for every job, history, search, and delete operation;
- never treat a caller-supplied user field as authorization;
- link identities explicitly rather than inferring that two platform accounts are the same person.

## Provider Credentials

Supported secret inputs:

```text
OPENAI_API_KEY
OPENAI_API_KEY_FILE
ANTHROPIC_API_KEY
ANTHROPIC_API_KEY_FILE
PROVIDER_AUTH_FILE
OPENAI_CHATGPT_AUTH_FILE
```

Requirements:

- never commit credentials;
- prefer mounted secret files or a host secret manager for unattended deployment;
- do not print configuration structs containing secret values;
- redact provider diagnostics before persistence or logs;
- rotate a key after suspected exposure and inspect provider usage.

`PROVIDER_AUTH_FILE` is the JSON credential store written by the loopback
dashboard's masked API-key controls. It defaults to
`brainslug.provider-auth.json` beside SQLite, is created with owner-only file
permissions where the platform supports them, and is ignored by Git. Saved
keys are never rendered back into HTML, URLs, health responses, or logs.
Removing a saved key does not edit environment variables or mounted secret
files.

`OPENAI_CHATGPT_AUTH_FILE` is different from the API-key file variables. It is
a JSON credential store written by the local account login and contains an
access token, refresh token, account identifier, and expiry. It defaults to
`brainslug.openai-auth.json` beside SQLite, is created with owner-only file
permissions where the platform supports them, and is ignored by Git. Treat a
copy as an active account credential. **Disconnect** removes the local file;
account-side revocation remains the recovery step after suspected exposure.

Browser account login uses PKCE, a cryptographically random state value, a
five-minute pending-flow lifetime, and a callback bound to `localhost:1455`.
The callback accepts only the matching pending state and never belongs on a
public proxy. The code login uses the OpenAI device page and retains only
short-lived pending identifiers until the token exchange completes. All admin
login and disconnect mutations require the same per-process CSRF token and
same-origin check as provider-setting changes.

The ChatGPT account connector is an OpenCode-compatible integration, not the
documented OpenAI API authentication contract. It uses the account-scoped
Codex Responses transport and its model availability may change with account
tier or service behavior. API-key mode remains the supported OpenAI API path.

The administrative health response reports provider and model names but no credentials.

The dashboard persists provider, authentication mode, model, base URL, token
limit, timeout, reasoning effort, and text verbosity in the non-secret runtime settings file.
That schema deliberately has no credential field. The dashboard exposes only
a boolean credential status and a generic source category such as
`Environment`, `Secret file`, `ChatGPT account`, or `Not configured`.

## Provider And Model Policy

Brainslug does not silently switch provider or model.

Saved metadata records:

- requested provider and model;
- actual provider and model when the backend reports them;
- latency;
- stable status;
- redacted error diagnostic.

OpenAI response failures and incomplete statuses are mapped to stable categories. Output-token exhaustion is distinguished from generic provider failure.

Retry and fallback policy belongs in the application layer. It must not be hidden inside a provider client.

## Rate Limiting And Spend Control

Signature verification proves that a request came through Alexa. It does not limit how many genuine requests can spend the provider key.

Before continuous exposure, implement:

- per-source network limit;
- per-principal limit;
- maximum prompt length;
- bounded concurrent provider calls;
- bounded future job queue;
- global daily request, token, or cost cap;
- fail-closed behavior when the cap is reached.

A real private user can accidentally create expensive load even when every request is authentic.

## Current Synchronous Request Risk

The provider call is currently synchronous under the ASK request context. Provider timeouts default to six seconds.

Consequences:

- slow reasoning can time out;
- the request is recorded as a failure;
- there is no accepted background state;
- a retry can spend again because adapter request idempotency does not exist.

Do not raise the timeout beyond Alexa's practical response budget. Implement durable jobs and idempotency instead.

## Deferred Jobs

The future background layer must satisfy:

- accepted state persisted before provider spending;
- idempotency scoped by adapter, principal, and request ID;
- bounded queue and worker concurrency;
- separate job deadline;
- explicit completed, failed, canceled, and interrupted states;
- restart recovery;
- ownership checks on status and result reads;
- retention for prompts, answers, errors, and abandoned jobs.

Do not start an unowned goroutine from the HTTP handler.

## Prompt And Response Data

SQLite may contain:

- user questions;
- short spoken answers;
- full model answers;
- provider and model metadata;
- session correlation;
- latency;
- redacted failures.

Treat the database and backups as private data.

Required future controls:

- retention by age and count;
- per-principal deletion;
- explicit default for full-answer storage;
- protected backups;
- dashboard filtering or owner authentication if the admin surface becomes remote.

`SAVE_HISTORY=false` remains supported. Features that depend on history must fail clearly when it is disabled.

## Logging

Logs may include:

- request method and route;
- provider and model names;
- stable error category;
- latency;
- derived correlation identifiers where needed.

Logs must not include:

- API keys or OAuth tokens;
- Alexa signatures;
- certificate bodies;
- raw Alexa user IDs;
- raw future MCP subjects;
- full prompts or answers by default;
- unredacted provider responses.

Debug logging does not waive these rules.

## Future MCP Security

A future MCP endpoint requires its own:

- service authentication;
- user OAuth authorization;
- issuer, audience, expiry, scope, and PKCE validation where required;
- authenticated subject to principal mapping;
- tool-level authorization;
- rate limits and spend controls;
- listener or hostname.

Do not reuse ASK verification or expose the provider interface directly as an MCP tool.

See `mcp-roadmap.md`.

## Distribution Threat Model

Public Skill Store or Alexa+ distribution changes the system from private infrastructure to a consumer service.

Before that decision:

- use production hosting;
- publish privacy and data-use disclosures;
- implement quotas and global spend controls;
- provide deletion procedures;
- isolate every user;
- add abuse controls and monitoring;
- define content and refusal policy;
- decide whether prompts and answers are stored.

Do not publish the current private configuration as though it were merely a final deployment checkbox.

## Incident Response

If the public route or provider key may have been abused:

1. disable the public route;
2. revoke or rotate the provider key;
3. inspect usage and billing;
4. preserve relevant redacted logs;
5. identify the missing verification, limit, or proxy control;
6. add a regression test before restoring exposure.

If prompts or answers may have been exposed, treat it as a private-data incident as well as a billing incident.

## Continuous-Use Exit Criteria

- [x] Real Amazon-signed request passes verification.
- [ ] Tampered, stale, foreign-skill, and invalid-certificate requests fail before application execution.
- [ ] Public and administrative listeners remain isolated.
- [ ] Port 8081 is not publicly routed.
- [ ] Per-principal and global limits exist.
- [ ] Provider concurrency is bounded.
- [ ] Credentials and raw platform IDs are absent from logs.
- [ ] History is principal-scoped.
- [ ] Retention matches the intended use.
- [ ] Physical Echo test succeeds repeatedly with verification enabled.
