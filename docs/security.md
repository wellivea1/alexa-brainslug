# Security

Alexa Brainslug accepts internet-originated requests, can spend money through provider credentials, and stores private prompts and answers. Treat the service as a paid authenticated application, not as a harmless home webhook.

## Current Blocking State

There is currently no configuration that is both fully functional and safe for continuous public exposure.

```text
ALEXA_VERIFY_REQUESTS=true
```

fails closed because complete Alexa request verification is not implemented.

```text
ALEXA_VERIFY_REQUESTS=false
```

allows testing but turns `/alexa` into an unauthenticated route to the configured provider account.

The allowed skill ID is useful defense in depth, but it is not request authentication by itself.

Use verification-disabled exposure only for a short controlled round-trip and remove the public route immediately afterward.

## Trust Boundaries

```text
Alexa device
  -> Amazon Alexa cloud
  -> public reverse proxy or tunnel
  -> ASK verification boundary
  -> ASK adapter
  -> application authorization and quota boundary
  -> provider and persistence adapters

Future MCP client
  -> separate MCP authentication boundary
  -> MCP adapter
  -> the same application authorization and quota boundary
```

The local dashboard is a separate administrative trust boundary and must remain loopback-only by default.

## ASK Request Verification

A complete ASK verifier must validate all of the following before parsing the request into an application action:

- Signature certificate URL scheme, host, port, and path constraints.
- Certificate retrieval with bounded timeouts and size limits.
- Certificate trust chain.
- Certificate validity and Alexa signing identity constraints.
- Signature over the exact raw request body.
- Request timestamp freshness.
- Expected skill application ID.

Failure behavior:

- Return HTTP 400.
- Log a local stable reason without returning it to the caller.
- Never invoke the provider or write user history.
- Never include the signature, certificate body, provider key, or raw user ID in logs.

Required verification fixtures:

- Valid signed request.
- Tampered body.
- Stale timestamp.
- Wrong skill ID.
- Disallowed certificate host or path.
- Invalid chain.
- Expired certificate.
- Oversized body.

Certificate caching, if added, must respect certificate validity and avoid turning retrieval into an unbounded SSRF or memory surface.

## Public And Administrative Listeners

Current code serves `/alexa`, `/health`, `/history`, and `/` on one listener. Proxy rules must therefore hide the administrative routes.

Target design:

```text
public ASK listener
  /alexa only

loopback administrative listener
  /health
  /history
  /
```

This split is required because structural isolation is stronger than a warning in a proxy example.

The public hostname must return 404 for:

```text
/
/history
/health
```

The dashboard and history currently have no authentication and contain private text. Do not publish them.

## Application Authorization

After the application-core extraction, every use case involving saved work must require a namespaced principal.

Requirements:

- A raw Alexa user ID is mapped to an internal principal at the adapter boundary.
- A future MCP OAuth subject uses a different namespace.
- History, job status, result retrieval, search, and deletion always apply principal scope.
- Object ownership is checked in the application layer, not only in adapter handlers.
- A caller-supplied user ID is never treated as authorization.
- Cross-platform identity merging is explicit and never inferred.

The application layer should make an unscoped history query impossible through its public interfaces.

## Provider Credentials

Supported secret inputs include environment variables and secret files:

```text
OPENAI_API_KEY
OPENAI_API_KEY_FILE
ANTHROPIC_API_KEY
ANTHROPIC_API_KEY_FILE
```

Requirements:

- Never commit credentials.
- Prefer file-mounted secrets or a host secret manager for unattended deployment.
- Do not print configuration structs containing secret fields.
- Redact provider error text before storage or logs.
- Do not return provider response bodies to Alexa or future MCP callers as diagnostics.
- Rotate a key after suspected exposure and inspect provider usage.

The local `.env` file should be readable only by the account running the service where the host supports permissions.

## Provider And Model Policy

Brainslug must not silently switch providers or models. Silent fallback makes billing, privacy, and answer provenance unpredictable.

The application records:

- Requested provider.
- Requested model.
- Actual provider and model reported by the backend.
- Stable failure category.
- Redacted local diagnostic.

A provider timeout, retry, or fallback policy belongs in the application layer and must be explicit.

## Rate Limiting And Spend Control

Signature verification proves that a request came through Alexa. It does not limit how many authenticated requests can spend the provider key.

Before continuous exposure, implement:

- Per-source network rate limit.
- Per-principal rate limit.
- Bounded concurrent provider jobs.
- Global daily request or cost cap.
- Maximum prompt length.
- Maximum accepted jobs per principal.
- Backpressure when the worker queue is full.

Rate-limit responses should be short and stable. Internal counters or cost details do not need to be spoken.

A global spend cap must fail closed. When the cap is reached, no provider call starts.

## Deferred Jobs

Background work changes the threat model.

Requirements:

- Persist accepted state before provider execution.
- Use idempotency to prevent duplicate provider charges from retries.
- Use bounded worker concurrency.
- Give each job a separate maximum runtime.
- Mark interrupted jobs explicitly during recovery.
- Do not allow an unbounded queue to consume memory or future spend.
- Check the principal again when status or results are retrieved.
- Retention applies to prompts, results, failures, and abandoned jobs.

The public request context must not be reused as an unbounded background context.

## Prompt And Response Data

SQLite history may contain:

- User questions.
- Short spoken answers.
- Full model answers.
- Provider and model metadata.
- Session or conversation correlation.
- Latency.
- Redacted failure information.
- Deferred job state.

Treat the database and backups as private data.

Required controls before multi-user or public distribution:

- Retention by age and count.
- Per-principal deletion.
- Clear default for whether full answers are stored.
- No raw platform identifiers where a derived internal principal is sufficient.
- Backup and restore behavior that preserves the same privacy expectations.

`SAVE_HISTORY=false` should remain a supported privacy mode. Features that depend on history, such as `last response`, must fail clearly when history is disabled.

## Logging

Logs may contain:

- Stable request correlation ID.
- Adapter surface.
- Derived principal identifier where necessary and safe.
- Provider and model names.
- Stable failure category.
- Latency.
- Job state transitions.

Logs must not contain:

- API keys or access tokens.
- Alexa request signatures.
- Certificate bodies.
- Raw Alexa user IDs.
- Raw MCP OAuth subjects.
- Full prompts or answers by default.
- Unredacted provider errors.

Debug logging must not weaken these rules.

## Future MCP Security

A future MCP endpoint is a separate ingress surface.

For Alexa+ or another OAuth-based host, require:

- Service authentication where the host requires it.
- User authorization for user-specific tools.
- PKCE where required.
- Token issuer, audience, expiry, and scope validation.
- Mapping of the authenticated subject to a namespaced principal.
- Tool-level authorization for reads and mutations.
- Separate rate limits and spend controls.

Do not:

- Reuse `ALEXA_VERIFY_REQUESTS` for MCP.
- Treat an MCP client-supplied user field as identity.
- Expose the local provider interface as an MCP tool.
- Expose raw history or administrative operations without a narrow user-owned scope.
- Assume a private hostname is authentication.

The MCP route should use a separate listener or hostname from ASK and administration.

## Content And Third-Party AI

A private skill that forwards broad questions to a model can still produce unsafe, private, copyrighted, or otherwise restricted content.

Before public distribution:

- Review Alexa skill and Alexa+ content policies against the exact behavior.
- Disclose third-party AI processing.
- Define moderation and refusal behavior.
- Avoid presenting unverified generated material as authoritative.
- Avoid unrestricted user-generated-content or general-web-search behavior where platform policy does not permit it.

A generic third-party LLM relay is specifically a poor Alexa+ MCP certification target. The private ASK use case and a public Alexa+ add-on are not interchangeable.

## Distribution Threat Model

Skill Store or Alexa+ publication changes the system from private infrastructure to a hosted consumer service.

Required before that decision:

- Production hosting outside a home internet connection.
- Privacy policy and data disclosures.
- Per-user quotas and global spend caps.
- Account deletion and data-deletion procedures.
- Multi-user identity and history isolation.
- Monitoring and incident response.
- Abuse controls.
- Availability expectations.
- A decision about whether user content is stored at all.

Do not implement publication-specific complexity merely to make the private skill work. Do not publish without it either.

## Incident Response

If a provider key or public route may have been abused:

1. Disable the public route.
2. Revoke or rotate the provider credential.
3. Inspect provider usage and billing.
4. Preserve relevant redacted logs.
5. Identify the missing verification, limit, or proxy control.
6. Add a regression test before restoring exposure.

If prompts or answers may have been exposed, treat it as a private-data incident, not only a billing incident.

## Security Exit Criteria For Continuous ASK Use

Continuous public ASK exposure is acceptable only when:

- Full Alexa verification passes with real requests.
- Verification failures do not reach the provider.
- Public and administrative listeners are separated.
- Public administrative routes are unreachable.
- Per-principal and global limits exist.
- Provider concurrency is bounded.
- Provider keys and raw platform IDs are absent from logs.
- History is principal-scoped.
- Retention is configured for the intended use.
- A short-lived real-device test has been repeated with verification enabled.
