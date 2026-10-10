# Alexa Skill Setup

This document covers a private classic ASK custom skill for the owner's Alexa account.

It does not configure Alexa+ MCP. Alexa+ is a separate partner-gated integration in which Alexa+ calls a third-party MCP server. See `alexa-platform.md` and `mcp-roadmap.md`.

## Prerequisites

- Amazon developer account associated with the Alexa account used for testing.
- Brainslug running locally or in Docker.
- Public HTTPS hostname routing only `/alexa` to the ASK listener.
- Administrative listener kept on loopback.
- Selected provider and explicit model.
- Real skill ID configured in `ALEXA_ALLOWED_SKILL_ID`.
- `ALEXA_VERIFY_REQUESTS=true`.

The custom request verifier is implemented, unit-tested, and validated against Amazon-signed Developer Console requests. Physical Echo recognition remains an external gate.

## Create The Skill

1. Open the Alexa Developer Console.
2. Select **Create Skill**.
3. Enter a development name such as `Brainslug`.
4. Select the locale used by the test Echo.
5. Choose a custom skill model.
6. Choose the option that permits your own HTTPS endpoint.
7. Create the skill.

Console labels may change. The required result is a custom interaction model whose endpoint is the Brainslug HTTPS service.

## Configure Invocation

Set the invocation name to:

```text
brain slug
```

Explicit invocation is the supported baseline.

Primary two-turn flow:

```text
Alexa, open Brain Slug
Brainslug is ready. What should I ask?
<free-form question>
```

The launch response contains a `Dialog.Delegate` directive for `AskGPTIntent`. The interaction model marks the `Query` slot required and supplies the prompt Alexa uses to collect it.

## Import The Interaction Model

1. Open the JSON editor for the interaction model.
2. Paste `skill/interaction-model.json`.
3. Save the model.
4. Build the model.
5. Commit any required locale-specific correction rather than editing only the console copy.

The model currently includes:

- invocation name `brain slug`;
- one required `AMAZON.SearchQuery` slot named `Query`;
- neutral carrier phrases for one-shot questions;
- `LastResponseIntent`;
- help, stop, cancel, fallback, and repeat built-ins;
- a dialog prompt for the missing question.

Representative one-shot forms:

```text
Alexa, ask Brain Slug about the halting problem
Alexa, ask Brain Slug regarding SQLite write-ahead logging
Alexa, tell Brain Slug to answer this question: why is the sky blue
```

The model and HTTPS invocation path can be verified with Developer Console requests. Exact spoken recognition must still be verified in the conversational simulator and on the real Echo.

## Configure The HTTPS Endpoint

Set the default region endpoint to:

```text
https://your-domain.example/alexa
```

Do not use a LAN-only URL:

```text
http://192.168.x.x:8080/alexa
http://localhost:8080/alexa
```

Alexa requests originate in Amazon's cloud.

The public hostname must route only `/alexa` to host loopback port 8080. It must not route the administrative listener on port 8081.

Expected public behavior:

```text
POST /alexa   -> Brainslug ASK handler
GET  /        -> 404
GET  /history -> 404
GET  /health  -> 404
```

See `local-deployment.md`.

## Record The Skill ID

Copy the skill application ID from the developer console and set:

```text
ALEXA_ALLOWED_SKILL_ID=amzn1.ask.skill.example
```

The skill ID check prevents another developer's skill from using the endpoint. It supplements request-signature verification; it does not replace it.

## Configure Verification

For all real Alexa tests:

```text
ALEXA_VERIFY_REQUESTS=true
ALEXA_ALLOWED_SKILL_ID=<your actual skill ID>
```

The verifier uses Alexa's:

```text
SignatureCertChainUrl
Signature-256
```

and checks the signing certificate, raw-body signature, request timestamp, and application ID before the application service can run.

The Brainslug host must be able to resolve and reach Amazon's signing-certificate URL over HTTPS.

### Verification-disabled mode

This mode exists only for local request fixtures:

```text
ALEXA_VERIFY_REQUESTS=false
```

Do not start a public proxy or tunnel in that mode. Disabling verification is not the troubleshooting procedure for a real Alexa request.

## Start Brainslug

Local process:

```powershell
go run ./cmd/brainslug
```

Docker:

```powershell
docker compose up --build -d
docker compose logs -f brainslug
```

Minimal administrative liveness:

```powershell
Invoke-RestMethod http://127.0.0.1:8081/healthz
```

Sign in to the dashboard to inspect detailed `/health`, then confirm:

- status is `ok`;
- provider and model are correct;
- Alexa verification is enabled;
- no credential is returned.

The public port does not serve `/health`.

## Developer Console Test

1. Start the HTTPS route.
2. Verify public `/`, `/history`, and `/health` return 404.
3. Open the skill test panel.
4. Enable testing for the development stage.
5. Invoke `open Brain Slug`.
6. Answer the prompt with a low-cost question.
7. Inspect the local logs and saved history.

Expected flow:

```text
LaunchRequest
  -> verification succeeds
  -> Dialog.Delegate for AskGPTIntent

IntentRequest with Query
  -> verification succeeds
  -> synchronous provider request
  -> concise answer
```

The current implementation is synchronous. Use a low-latency model or question for the first validation. Slow reasoning may exceed the six-second provider timeout.

## Physical Echo Test

After Developer Console success:

1. Use an Echo signed into the same Alexa account.
2. Say `Alexa, open Brain Slug`.
3. Provide a question in the follow-up turn.
4. Test representative one-shot forms.
5. Ask for `last response`.
6. Test help, stop, cancel, fallback, and repeat.
7. Review Alexa activity history when recognition differs from expectation.

Record the phrases that actually work. Do not infer real-device recognition from JSON validity alone.

## Verification Test Matrix

Code-level tests already cover generated signed requests. Real environment validation should include:

- valid Amazon-signed request;
- expected skill ID;
- wrong skill ID;
- stale captured request;
- modified body with original signature;
- absent signature headers;
- certificate fetch failure;
- public administrative-route probes.

Do not commit raw user identity, signatures, provider credentials, or private prompt content in fixtures.

## Voice Test Matrix

### Two-turn capture

```text
Alexa, open Brain Slug
```

Expected:

```text
Brainslug is ready. What should I ask?
```

The follow-up utterance should fill the entire `Query` slot.

### One-shot capture

Test:

```text
Alexa, ask Brain Slug about the halting problem
Alexa, ask Brain Slug regarding the New Madrid seismic zone
Alexa, tell Brain Slug to answer this question: why is the sky blue
```

### Immediate success

A provider response that finishes under the configured deadline should be spoken and stored.

### Provider timeout

A slow response should produce the provider-busy message and a redacted error record. It is not deferred; deferred jobs remain future work.

### Last response

Current behavior:

- no successful history -> no previous response;
- successful history -> latest nonempty successful answer;
- history failure -> local history error.

There is no pending-job state yet.

### Built-ins

Test:

- help;
- stop;
- cancel;
- repeat;
- fallback;
- session end.

Help and fallback should end with "What should I ask?", and a bare question spoken in reply should be answered rather than sent back to fallback. Answering that prompt with "last response" should read the previous answer.

## Troubleshooting

### The interaction model does not build

Use the exact model in `skill/interaction-model.json`. If Amazon rejects a sample or dialog rule, record the console error and commit the corrected model.

### Verification fails immediately

Check local logs for the stable failure category. Common causes include:

- blank or wrong skill ID;
- missing signature headers;
- host clock outside the allowed freshness window;
- outbound DNS or HTTPS failure when retrieving the signing certificate;
- certificate-chain validation failure;
- proxy modification of the request body.

Do not return detailed verification diagnostics to the public caller.

### Alexa reports a skill problem after verification

Check:

- provider key and explicit model;
- six-second provider deadline;
- OpenAI incomplete or failed response state;
- output-token limit exhaustion;
- SQLite write errors.

### Alexa times out

The model did not return inside the synchronous voice budget. Lower latency for initial validation. The long-term fix is the deferred-job phase.

### One-shot recognition is poor

Use the two-turn flow. Then adjust only the smallest set of carrier phrases supported by real activity-history evidence.

### The beta is active but absent from Your Skills

Confirm that the beta invitation was accepted in the iOS or Android Alexa app by the same Amazon consumer account and marketplace registered to the Echo. An identical email address can belong to separate Amazon marketplace accounts; for example, a beta link can authenticate against an Amazon.co.jp account even after the developer used Amazon.com. A Developer Console session therefore does not prove that the mobile Alexa account received the beta entitlement.

Add the intended consumer account as a beta tester, open the invitation on the mobile device that has the Alexa app, verify the Amazon marketplace before accepting, and then confirm that Brainslug appears in Your Skills.

## Development Versus Distribution

A development-stage private skill can be tested on the owner's Alexa account without public Skill Store publication.

Do not begin publication work until Brainslug has:

- real request verification validation;
- rate limits and global spend control;
- durable request idempotency;
- retention and deletion;
- production hosting;
- privacy policy and AI-processing disclosure;
- multi-user isolation;
- certification-safe content and invocation behavior.

Publication is a separate product decision.
