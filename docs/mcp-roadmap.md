# MCP Roadmap

## Status

MCP is not implemented in Brainslug.

It is not required for the current private ASK skill, and it must not be added as speculative infrastructure. The repository should first complete the application-core extraction, safe ASK ingress, deferred jobs, identity scoping, retention, and quotas.

Amazon's documented Alexa+ MCP model makes Alexa+ the MCP client and the third party the MCP server. It does not expose Alexa as a public MCP server for an external assistant to control Echo devices, routines, or the Alexa account.

See `alexa-platform.md` for the current platform assumptions.

## Why The Core Still Becomes Adapter-Neutral

Separating ASK from the application core is useful even if MCP never ships:

- ASK protocol code becomes smaller and testable.
- Deferred jobs and history semantics stop depending on Alexa envelopes.
- The local dashboard can use the same application queries.
- A browser, private API, or another approved client can be added without duplicating provider and persistence logic.

MCP is one possible future adapter over those use cases, not the organizing principle of the core.

## Entry Gates

Do not create `internal/mcp`, add an MCP dependency, or expose an MCP listener until every applicable gate below is satisfied.

### Platform gate

At least one concrete host is selected:

- Amazon has granted Alexa+ builder access for the project, or
- another named MCP client is the actual deployment target.

A general belief that MCP will be useful later is not enough.

### Product gate

Brainslug has typed capabilities with value beyond relaying arbitrary user text to another LLM.

For an Alexa+ production profile, the core tool set cannot be only:

```text
chat(prompt)
ask_model(prompt)
complete_text(prompt)
```

Amazon currently states that a generic third-party LLM relay without unique domain functionality is not acceptable as the add-on's core purpose.

### Architecture gate

The application layer already owns:

- Principal-scoped operations.
- Deferred job state.
- Stable typed errors.
- Idempotency.
- Retention and deletion.
- Provider policy.

The MCP adapter must not become the place where those are first invented.

### Security gate

The target host's service and user authentication requirements are known and implemented.

For Alexa+ this currently means separate service-level and user-level OAuth flows, including PKCE for customer authorization where required.

### Performance gate

Every proposed synchronous tool has a credible path to the target host's latency budget.

Amazon currently documents a less-than-500-millisecond round-trip target for Alexa+ MCP queries. A remote frontier-model response is not a credible synchronous dependency under that budget.

### Policy gate

The exact proposed tool set has been reviewed against:

- Consumer-use requirements.
- Data collection and disclosure rules.
- Voice-only behavior.
- Confirmation and failure recovery.
- Prohibition on a generic LLM relay as the core function.
- Any category-specific requirements.

## Adapter Profile

A future MCP adapter should be a thin inbound transport:

```text
MCP request
  -> protocol and authentication middleware
  -> principal mapping
  -> explicit application operation
  -> structured MCP result
```

It owns:

- Streamable HTTP handling.
- MCP initialization and capability negotiation.
- Tool, resource, and prompt descriptions.
- Service and user authentication integration.
- Mapping the authenticated subject to an internal principal.
- Mapping application results and stable errors to MCP results.
- Optional UI metadata for the target host.

It does not own:

- Provider selection.
- Model fallback.
- Prompt policy.
- Job storage.
- Retention.
- Cross-user authorization.
- Alexa ASK request verification.

## Tool Design Rules

### One tool, one user-visible operation

Each tool must represent a distinct operation the user can understand. Do not expose internal helper functions or provider wire formats.

Good shape:

```text
get_saved_answer(answer_id)
get_analysis_status(job_id)
search_saved_answers(query, limit)
delete_saved_answer(answer_id)
```

Poor shape:

```text
invoke(method, args)
provider_request(raw_json)
chat(prompt)
```

The examples above are candidate shapes, not a commitment to publish them in Alexa+.

### Typed input and output

Schemas must:

- Use stable field names.
- Mark required fields accurately.
- Constrain enums and numeric ranges.
- Distinguish pending, completed, failed, and not-found states.
- Return identifiers that are opaque outside the caller's principal scope.

Do not rely on tool descriptions to compensate for an ambiguous schema.

### Principal scoping

Every user-specific tool call derives its principal from authenticated context. A caller-supplied user ID is not authorization.

Requirements:

- The tool cannot request another principal's data.
- Object IDs are checked against the authenticated principal.
- Search and list operations always apply principal scope inside the application layer.
- Service-level discovery calls do not receive user data.

### Idempotent mutations

Any tool that creates or changes state accepts or derives an idempotency key appropriate to the host. A repeated request must not duplicate provider spending or destructive work.

### Long-running work

A long-running operation must not block the MCP request until a frontier model finishes.

Preferred pattern where the host experience supports it:

```text
submit operation
  -> accepted + job ID

get status
  -> pending, completed, or failed

get result
  -> saved structured result
```

This pattern still requires validation with the actual MCP host. Do not assume Alexa+ automatically polls or proactively announces completion.

### Errors

Return stable structured categories such as:

- invalid_argument
- unauthenticated
- forbidden
- not_found
- conflict
- rate_limited
- pending
- provider_unavailable
- internal

Provider response bodies and local diagnostics remain private. The application layer decides the category; the adapter decides the MCP representation.

### Voice-only path

An Alexa+ tool must remain usable on a headless Echo.

Any optional MCP App UI supplements, rather than replaces:

- A concise spoken result.
- A spoken confirmation before consequential actions.
- A spoken failure path.
- A way to recover from missing information.

## Candidate Capability Profiles

These profiles illustrate how one application core may expose different allowed surfaces. They are not all required.

### Private development MCP profile

A private client controlled by the owner may expose experimental capabilities, including a model-backed analysis submission, if authentication and spending controls are adequate.

This profile is not evidence that the same tool is acceptable for Alexa+ publication.

### Alexa+ production profile

Expose only the subset that:

- Has consumer value independent of a generic chat relay.
- Meets latency and voice constraints.
- Has clear authorization and confirmation semantics.
- Has been reviewed against Amazon policy.

A likely first profile would favor retrieval and management of already-created Brainslug results rather than synchronous frontier-model generation. Whether that is sufficiently valuable for Alexa+ remains an open product question.

### Administrative profile

Local administrative operations should normally stay on the loopback dashboard or private API rather than MCP. Do not expose secret rotation, raw logs, database inspection, or cross-principal administration to an assistant host without a concrete need and stronger authorization.

## Authentication Separation

ASK and MCP are different trust boundaries.

```text
ASK
  - Amazon request signature
  - certificate validation
  - timestamp freshness
  - allowed skill ID

MCP
  - target-host service authentication
  - OAuth user authorization
  - scopes
  - token validation
  - authenticated subject
```

Do not share a boolean such as `VERIFY_REQUESTS` between them. Use separate configuration, middleware, logs, and tests.

## Deployment Separation

A future MCP endpoint should use a separate listener or hostname from ASK and the local dashboard.

Example target topology:

```text
ask.example.com/alexa  -> ASK listener
mcp.example.com/mcp    -> MCP listener with OAuth
127.0.0.1:admin        -> dashboard, history, health
```

No MCP route should exist in the default Docker or proxy examples before the adapter is implemented and authenticated.

## Testing

Required tests for any future adapter:

- MCP initialization and protocol negotiation.
- Tool discovery contains only the configured profile.
- Authentication failure.
- Missing or invalid user authorization.
- Principal isolation.
- Schema validation.
- Stable error mapping.
- Idempotent duplicate mutations.
- Latency budget for synchronous tools.
- Pending and result retrieval for long-running work.
- Voice-safe structured output for Alexa+ tools.
- No provider credential, raw platform subject, or private diagnostic in responses or logs.

Use the target client's official inspector or test tooling in addition to local unit tests.

## Decision Record

Current decision:

- Build ASK now.
- Extract the application core now.
- Do not build MCP now.
- Re-evaluate MCP only after the entry gates are met.

This avoids two bad outcomes:

1. Coupling all application behavior permanently to Alexa because MCP is uncertain.
2. Building a large speculative MCP framework that does not solve the immediate ASK security and usability problems.
