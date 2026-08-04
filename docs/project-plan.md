# Project Plan

## Reviewed Inputs

- Original project request in this repo's attachment.
- The ChatGPT conversation the project began with, by way of the Codex session
  summary. The conversation itself is no longer accessible; nothing in it is
  known to survive outside this repo.
- Current Alexa Brainslug code and tests.
- Prior-art reviews of `jackmcguire1/alexa-chatgpt` and NoobBoard.

## Goal

Route a hard question to a model chosen explicitly, and hear the answer back
through an Echo. Alexa answers the easy things itself; this exists for the
questions worth spending reasoning tokens on, where the value is deterministic
control over which model responds.

Alexa calls a public HTTPS endpoint, the local Go service verifies and routes
the request, the selected provider answers, Alexa speaks a concise version, and
the full answer is stored locally.

Supported providers are the OpenAI API and the Anthropic API.

Voice is the only intended interface. The dashboard exists for development and
testing and is not a product surface.

### Distribution

Store publication is an intended direction. It is not scheduled, and it changes
the threat model rather than extending it: every user's questions would bill one
API key, strangers' prompts would land in SQLite on a home machine, and a home
internet connection would become an availability commitment.

Nothing below depends on that decision. Build as though it is public -
verification, rate limiting, per-user scoping, retention - and treat submission
as a separate call made later, once metering and hosting exist.

## Current State

Implemented:

- Go HTTP server with `/health`, `/alexa`, `/history`, and `/`.
- Alexa request/response routing for launch, ask, last response, help, fallback,
  stop, cancel, repeat, and session end.
- SQLite interaction storage.
- Minimal local dashboard.
- Docker Compose and Caddy example.
- OpenAI provider using the Responses API.
- Anthropic provider using the Messages API.
- Provider selection with `AI_PROVIDER=openai|anthropic`.
- Secret-file support for provider credentials.
- Browser hardening headers, request body cap, and provider error redaction.
- Basic unit tests and SQLite integration tests.

Added in the architecture pass:

- Versioned, transactional schema migrations.
- Graceful shutdown, full server timeouts, and a startup path that does not
  skip its own cleanup.
- Shared provider HTTP plumbing, so a new provider is its wire format and
  nothing else.
- Typed provider errors, so the spoken message can distinguish a
  misconfiguration from a busy provider from an exhausted token budget.
- A speech package that shapes model output for a voice, with the Markdown
  stripping that used to live in the OpenAI provider.
- Linting, formatting, and CI, including a Docker build that has to start.

Known gaps, in the order they matter:

- No configuration both works and is safe. Verification on rejects Alexa;
  verification off leaves an open proxy to a paid API key.
- Provider timeouts (12s) exceed Alexa's response budget (8s).
- `MAX_OUTPUT_TOKENS` is shared between reasoning/thinking tokens and visible
  text. At 600 a reasoning model can return no text at all. The failure is now
  reported as its own error rather than a generic one, but the default is
  still wrong.
- Sample utterances read as whole sentences rather than as the tail of one.
- `/` and `/history` are unauthenticated and trivially exposed by a
  whole-origin reverse proxy.
- `/alexa` has no rate limiting.
- History has no per-user scoping outside `last response`, and no retention
  controls.
- `ANTHROPIC_MODEL` default is legacy.

## Architecture Principles

- Keep the service local-first and private.
- Keep the Alexa endpoint narrow: it should only accept Alexa-compatible JSON
  and return Alexa-compatible JSON.
- Do not assume LAN-only access works for Alexa.
- Do not hide the selected provider or model.
- Store full answers locally, but speak concise answers.
- Fail closed for security-critical incomplete work.
- Prefer structural guarantees over documented warnings. A dashboard bound to
  loopback cannot be exposed by a proxy misconfiguration; a documented warning
  can be, and was.
- Prefer boring Go interfaces and explicit env configuration over framework
  abstractions.
- Dependencies point inward and never cycle. Packages take narrow option
  structs rather than importing the configuration package, so each one is
  testable on its own.
- One runtime dependency. A SQLite driver earns its place; a vendor SDK for a
  single HTTP endpoint does not.

## Engineering Standards

The service is meant to run unattended, answer a stranger's question, spend
money doing it, and keep a record. That is the bar the code is held to, and it
is why the following are treated as requirements rather than polish.

- Every failure path is either handled or explicitly and visibly ignored, with
  a comment saying why. Errors wrap with `%w`; callers that need to
  distinguish a failure match a sentinel or a typed error.
- Configuration is validated at startup, and an unparseable value is a startup
  failure naming the variable rather than a silent default.
- The process starts and stops cleanly: no `os.Exit` past a defer, signal
  handling, and a bounded drain on shutdown.
- Schema changes are versioned, transactional, and append-only.
- Credentials never reach a log, an error, or the database. Alexa user IDs
  never reach a provider unhashed.
- Exported symbols and packages are documented. Comments record why, not what.
- Linting, formatting, the race detector, and a container that actually starts
  are enforced by CI, not by review attention.

`../CONTRIBUTING.md` is the working version of this list.

Known deliberate gaps, so they are not mistaken for oversights: there are no
metrics or tracing, and no structured request IDs. Both are worth adding when
there is more than one user to correlate across, and neither is worth the
weight before then.

## Phases

### Phase 0: Prove the Round Trip

Before more code. Create the skill in the developer console, point it at a
temporary tunnel with verification off and `ALEXA_ALLOWED_SKILL_ID` set, and
confirm an utterance from the actual Echo reaches the endpoint. Tear the tunnel
down afterwards.

Exit criteria:

- A real device invocation produces a request in the local log.
- The invocation phrasing that works is written down.

Everything downstream assumes this works. It is ten minutes to find out.

Status: not started.

### Phase 1: Make It Usable

The purpose of the project is slow, expensive answers. The current design treats
that as an error case.

Exit criteria:

- A voice deadline shorter than Alexa's 8 seconds returns an acknowledgement
  rather than a failure.
- The provider call continues in the background under its own deadline and
  writes its result to SQLite.
- `last response` retrieves that result.
- Token budget and reasoning/thinking effort are configurable per provider, and
  an empty-text response is reported as its own diagnosable error rather than a
  generic one.
- Sample utterances read naturally; slot elicitation accepts free-form questions
  in the two-turn flow. See `invocation-design.md`.
- `docker compose up --build` starts and persists to the mounted volume.

Status: not started.

### Phase 2: Make It Safe To Expose

Exit criteria:

- `/alexa` validates Alexa signatures, certificate URL, certificate chain,
  timestamp freshness, and allowed skill ID, and rejects with HTTP 400.
- `ALEXA_VERIFY_REQUESTS=true` works with the Alexa Developer Console.
- Request verification has unit tests using captured or fixture requests,
  including wrong host, expired certificate, stale timestamp, and tampered body.
- The public listener serves only `/alexa`. The dashboard, history, and health
  endpoints are on a separate loopback-bound listener.
- `/alexa` has rate limiting by IP and Alexa user ID, plus a global daily cap.

Status: not started.

### Phase 3: Make It Honest About Multiple Users

Exit criteria:

- History is scoped per Alexa user ID everywhere, not just in `last response`.
- Retention is configurable by age and count.
- Stored errors remain redacted.
- The OpenAI provider has tests covering `output_text`, the nested
  `output[].content[]` fallback, refusals, API errors, and empty output.
- Security documentation describes a multi-user threat model.

Status: not started.

### Deferred

Account linking, privacy policy, certification preparation, and per-user quotas.
All are gated on the distribution decision above.

## Dropped

**OpenCode provider.** OpenCode was an implementation reference for calling an
agent API, not a product requirement. As a provider it is a slower, more
expensive route to the same chat completion the hosted providers already give,
and an agent that runs for minutes cannot fit a voice turn. Removed from the
provider layer, the configuration surface, and the documentation.
