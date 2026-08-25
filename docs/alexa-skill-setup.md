# Alexa Skill Setup

This document covers a private classic ASK custom skill for the owner's Alexa account.

It does not configure Alexa+ MCP. Amazon's documented Alexa+ MCP path is a separate partner-gated integration in which Alexa+ calls a third-party MCP server. See `alexa-platform.md` and `mcp-roadmap.md`.

## Before Starting

You need:

- An Amazon developer account associated with the Alexa account used for testing.
- The Brainslug service running locally.
- A public HTTPS hostname or short-lived tunnel that can route only `POST /alexa` to the service.
- An explicit provider and model configured.
- A plan to remove the public route immediately after initial testing if request verification is still incomplete.

Current security limitation:

- `ALEXA_VERIFY_REQUESTS=true` fails closed because full Alexa signature verification is not implemented.
- `ALEXA_VERIFY_REQUESTS=false` permits testing but leaves the route unauthenticated.

Do not leave a public endpoint available with verification disabled.

## Create The Skill

1. Open the Alexa Developer Console.
2. Select **Create Skill**.
3. Enter a development name such as `Alexa Brainslug`.
4. Choose the primary locale used by the test Echo.
5. Select a custom skill model.
6. Use the Alexa-hosted or provision-your-own-resources option that allows an HTTPS endpoint. Brainslug uses its own HTTPS service, not Lambda.
7. Create the skill.

Console labels can change. The required result is a custom interaction model with an HTTPS endpoint owned by this service.

## Configure Invocation

Set the invocation name to:

```text
brain slug
```

Keep explicit invocation as the supported route.

Primary test flow:

```text
Alexa, open Brain Slug
Brainslug is ready. What should I ask?
<question>
```

The shipped interaction model still contains poor one-shot samples. The intended replacement and two-turn slot-elicitation design are in `invocation-design.md`.

## Import The Interaction Model

1. Open the JSON editor for the interaction model.
2. Paste the contents of `skill/interaction-model.json`.
3. Save the model.
4. Build the model.
5. Resolve any locale-specific validation errors before endpoint testing.

Until the model rewrite lands, use the two-turn launch flow for the least awkward test. Do not treat the current `ask ... ask ...` one-shot phrasing as intended behavior.

After the invocation work is implemented, confirm that the model includes:

- `AMAZON.SearchQuery` for the question slot.
- Natural one-shot carrier phrases.
- A required `Query` slot or equivalent dialog model for two-turn elicitation.
- Help, fallback, stop, cancel, repeat, and last-response intents.

## Configure The HTTPS Endpoint

Set the default region endpoint to:

```text
https://your-domain.example/alexa
```

Do not use:

```text
http://192.168.x.x:8080/alexa
http://localhost:8080/alexa
```

Alexa requests originate in Amazon's cloud and require a publicly reachable HTTPS endpoint.

The public hostname must not route the dashboard, history, or health endpoints.

Expected public behavior:

```text
POST /alexa   -> Brainslug
GET  /        -> 404
GET  /history -> 404
GET  /health  -> 404
```

See `local-deployment.md`.

## Record The Skill ID

Copy the skill application ID from the developer console and configure:

```text
ALEXA_ALLOWED_SKILL_ID=amzn1.ask.skill.example
```

This is not a substitute for signature verification. It is one required check inside verification.

Do not commit the real ID in a public example if the repository's privacy model later changes.

## Controlled First Round-Trip Test

Use this only while full verification is incomplete.

1. Start Brainslug locally.
2. Confirm the provider and model in `/health`.
3. Set:

```text
ALEXA_VERIFY_REQUESTS=false
ALEXA_ALLOWED_SKILL_ID=<your actual skill ID>
```

4. Start a path-scoped short-lived tunnel or proxy.
5. Confirm that the public hostname returns 404 for `/`, `/history`, and `/health`.
6. Invoke `Alexa, open Brain Slug` on the real Echo.
7. Ask a low-cost test question.
8. Confirm the request and answer locally.
9. Save a sanitized request fixture needed for verification tests.
10. Stop the tunnel or remove the public route.

Do not use a guessed, permanent, or broadly routed hostname as a substitute for verification.

## Verification-Enabled Test

After full request verification is implemented:

```text
ALEXA_VERIFY_REQUESTS=true
ALEXA_ALLOWED_SKILL_ID=<your actual skill ID>
```

Required test cases:

- Developer Console test succeeds.
- Real Echo test succeeds.
- Tampered request body fails with HTTP 400.
- Stale timestamp fails.
- Wrong skill ID fails.
- Invalid or disallowed certificate URL fails.
- Public administrative routes remain unreachable.

The endpoint may remain continuously reachable only after these checks pass and rate limits exist.

## Voice Test Matrix

### Launch and elicitation

```text
Alexa, open Brain Slug
```

Expected target behavior:

```text
Brainslug is ready. What should I ask?
```

The following full utterance should fill the query slot.

### One-shot question

After the interaction-model rewrite:

```text
Alexa, ask Brain Slug about the halting problem
Alexa, ask Brain Slug why the sky is blue
Alexa, tell Brain Slug to explain write-ahead logging
```

### Fast completion

A low-latency provider response should be spoken in the same turn.

### Deferred completion

A deliberately slow fixture or provider stub should return a pending acknowledgement before Alexa times out. The work should continue under its own deadline.

### Last response

Test all states:

- No previous job.
- Latest job pending.
- Latest job completed.
- Latest job failed.

### Built-in behavior

Test:

- Help.
- Stop.
- Cancel.
- Repeat.
- Fallback.
- Session end.

## Development Versus Distribution

Private development requires a working skill attached to the owner's account. It does not require Skill Store publication.

Do not begin publication work until the project has:

- Real request verification.
- Per-user history scoping.
- Retention and deletion controls.
- Quotas and a global spend cap.
- Production hosting.
- Privacy policy and disclosure of third-party AI processing.
- Certification-safe content and invocation behavior.

Publication is a separate product decision, not the final checkbox of local setup.

## Troubleshooting

### Alexa says the endpoint is invalid

Check:

- HTTPS is valid for the hostname.
- The endpoint path is exactly `/alexa`.
- The route is publicly reachable from outside the LAN.
- The proxy forwards `POST` and the body unchanged.

### Alexa reports a skill problem immediately

Check local logs for:

- Verification failure.
- JSON parsing failure.
- Provider configuration failure.
- Provider deadline exceeded.

Do not return verification diagnostics to the public caller.

### The one-shot phrase sounds wrong

That is a known interaction-model defect. Use the two-turn flow and implement the carrier-phrase rewrite in `invocation-design.md`.

### Alexa times out but history later contains an answer

The current synchronous path exceeded Alexa's deadline. The correct fix is the deferred-job architecture, not a longer ASK timeout.

### The provider returns no visible text

The output-token budget may have been consumed by reasoning or thinking tokens. Raise the configured budget or reduce reasoning effort; do not assume the provider returned an empty successful answer without diagnosis.
