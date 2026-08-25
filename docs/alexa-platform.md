# Alexa Platform Constraints

Last verified: 2026-08-25.

This document records the Alexa platform assumptions that affect Brainslug architecture. Recheck the linked Amazon documentation before implementing or publishing a platform integration because Alexa+ access, MCP support, policy, and certification requirements can change.

## Supported Surfaces Relevant To Brainslug

| Surface | Third-party interaction model | Brainslug status |
| --- | --- | --- |
| ASK custom skill | Invocation name, intents, slots, request JSON, and Alexa response JSON | Current implementation and primary route |
| ASK pre-built domain interfaces | Amazon-defined directives for smart home, music, video, and other supported domains | Not relevant to general question answering |
| ASK tasks, triggers, reminders, and proactive events | Narrow permissioned schemas for composition or asynchronous behavior | Possible future supporting features, not a general speech channel |
| Alexa+ Category add-ons | Amazon-defined transactional service-provider interfaces or category MCP | Partner-gated and not a current fit |
| Alexa+ MCP Toolkit | Alexa+ acts as MCP client and invokes a third-party MCP server | Future-only, partner-gated, and policy constrained |
| Alexa as an MCP server for external agents | External model controls Echo devices, routines, or Alexa account through MCP | No documented public platform |

## Classic ASK Custom Skill

A custom skill remains the generally available route for an independent developer who wants Alexa to call a custom HTTPS backend.

Amazon documentation:

- [Understanding custom skills](https://developer.amazon.com/en-US/docs/alexa/custom-skills/understanding-custom-skills.html)
- [Built-in slot type reference](https://developer.amazon.com/en-US/docs/alexa/custom-skills/slot-type-reference.html)
- [Progressive response API](https://developer.amazon.com/en-US/docs/alexa/custom-skills/progressive-response-api-reference.html)
- [Skill Management API overview](https://developer.amazon.com/en-US/docs/alexa/smapi/smapi-overview.html)

### What ASK provides

- Wake-word handling and microphone capture on Alexa devices.
- Speech recognition and routing to a selected skill.
- Intent and slot resolution according to the skill interaction model.
- Request, session, user, device, and context metadata in structured JSON.
- Speech synthesis and supported visual or directive responses.
- Developer-console testing, certification, and skill-management APIs.

### What the skill backend receives

The backend receives a structured request such as `LaunchRequest`, `IntentRequest`, or `SessionEndedRequest`. It does not receive a continuous microphone stream.

For open-ended questions, `AMAZON.SearchQuery` is the relevant slot type. It captures flexible language but still operates inside an Alexa interaction model. It requires a carrier phrase in intent samples and is not equivalent to raw transcription.

### Invocation

The normal route is invocation-name based:

```text
Alexa, open Brain Slug
Alexa, ask Brain Slug about the halting problem
```

ASK name-free interaction exists in limited forms, but Brainslug must not depend on Alexa routing generic questions away from Alexa+ and into this skill. The supported baseline is explicit invocation.

### Response deadline

A custom skill response must complete inside Alexa's short request budget, commonly documented and observed as approximately 8 seconds. A progressive response may acknowledge work but does not extend the final-response deadline.

Design consequence:

- Fast provider result: answer in the same voice turn.
- Slow provider result: accept a deferred job and return a short acknowledgement before the deadline.
- Later retrieval: `last response` or another explicit intent reads the completed result.

The current 12-second provider timeout is therefore structurally wrong for an immediate ASK request.

### Out-of-session interaction

ASK supports narrow asynchronous systems such as reminders, timers, proactive events, custom tasks, and routine triggers. These do not provide arbitrary unsolicited text-to-speech or a general ambient-agent channel.

Brainslug should not design around an undocumented ability to make an Echo speak any text at any time. Deferred results must remain pull-based unless a specific supported schema is later adopted.

## Alexa+ MCP Toolkit

Amazon documentation:

- [Alexa+ developer docs home](https://developer.amazon.com/docs/alexaplus/add-ons/home.html)
- [Alexa+ integration approach](https://developer.amazon.com/docs/alexaplus/add-ons/choose-the-proper-alexaplus-integration-approach.html)
- [MCP Toolkit overview](https://developer.amazon.com/docs/alexaplus/add-ons/mcp-toolkit-overview.html)
- [MCP Toolkit quickstart](https://developer.amazon.com/docs/alexaplus/add-ons/mcp-toolkit-quickstart.html)
- [MCP authentication](https://developer.amazon.com/docs/alexaplus/add-ons/mcp-toolkit-authentication.html)
- [Certification guidelines](https://developer.amazon.com/docs/alexaplus/add-ons/certification-guidelines.html)
- [Policy requirements](https://developer.amazon.com/docs/alexaplus/add-ons/policy-requirements.html)

### Direction of control

The documented runtime direction is:

```text
Alexa+ agent and MCP client
  -> third-party MCP server
  -> third-party service or data
```

It is not:

```text
third-party AI client
  -> public Alexa MCP server
  -> Echo devices or Alexa account
```

Brainslug may eventually operate an MCP server that Alexa+ calls. It cannot use this platform as a documented reverse control API for Alexa.

### Access status

Alexa+ builder and MCP production onboarding are currently select-partner or private-preview programs. Protocol compatibility alone does not make a server publicly discoverable in Alexa+.

Design consequence:

- Do not make MCP a prerequisite for the current project.
- Do not schedule production MCP work until Amazon access exists.
- Keep the application core reusable so an approved adapter can be added later without moving business logic again.

### Protocol and capability model

Amazon currently documents Streamable HTTP support for MCP specification version `2025-11-25`, including tools, resources, prompts, structured results, elicitation, and optional MCP App UI. One lifecycle example still shows an older protocol version, so a future implementation must negotiate and test compatibility rather than hard-code a version from one sample payload.

Alexa+ owns the top-level conversation and selects tools. The third-party developer supplies typed operations and structured results. That is different from a classic skill, where the developer defines intents and much of the spoken response.

Design consequence:

- The application core returns structured use-case results.
- The ASK adapter writes ASK speech.
- A future MCP adapter writes tool schemas and structured MCP results.
- Core logic does not emit protocol-specific response objects.

### Latency

Amazon currently specifies a less-than-500-millisecond round-trip query response target for MCP servers.

A fresh remote frontier-model inference is not a credible default inside that budget. For MCP, Brainslug must favor:

- Fast database or search operations.
- Cached or precomputed results.
- Immediate job acceptance with a separate status operation, where the host experience supports it.
- Deterministic service actions.

Do not assume Alexa+ will wait for a slow model merely because the protocol can carry the request.

### Authentication

Amazon documents service-level OAuth for registration and discovery operations and user-level authorization-code flow with PKCE for customer-specific actions.

Design consequence:

- ASK signature verification and MCP OAuth are separate trust systems.
- `/alexa` and any future MCP endpoint require separate middleware and configuration.
- An Alexa skill application ID is not an MCP user identity.
- A future MCP principal must be derived from an authenticated OAuth subject.

### Policy

Amazon's certification guidance rejects an add-on whose core function simply relays customer queries to a third-party AI or LLM without unique value or domain-specific functionality.

Therefore an Alexa+ production profile must not expose only:

```text
chat(prompt) -> text
ask_model(prompt) -> text
```

The current Brainslug feature may remain useful as a private ASK skill. That does not make it a certifiable Alexa+ MCP add-on.

A future MCP profile must expose narrow capabilities that exist independently of the conversational wrapper, such as retrieving an existing saved analysis, checking a job, searching the user's own stored answers, or performing another real domain operation. Candidate tools are not commitments and still require policy review.

## Design Decisions Derived From The Platform

1. Use ASK now.
2. Keep explicit invocation as the supported baseline.
3. Use `AMAZON.SearchQuery` plus a two-turn elicitation flow for flexible questions.
4. Return before the ASK deadline and defer slow work.
5. Keep deferred result retrieval pull-based.
6. Extract a transport-neutral application service before adding another adapter.
7. Do not add MCP code until access and a certifiable capability set exist.
8. Do not expose a generic LLM relay in an Alexa+ MCP production profile.
9. Keep ASK request verification and future MCP OAuth separate.
10. Do not claim or depend on external-agent control of Alexa.

## Revalidation Triggers

Recheck the platform documentation when any of these occurs:

- Amazon opens Alexa+ MCP to self-service developers.
- Amazon changes the supported MCP protocol version.
- Amazon changes the MCP latency or authentication requirements.
- Amazon introduces a public Alexa server interface for external agents.
- ASK response deadlines, `AMAZON.SearchQuery`, or skill certification policy changes.
- Brainslug moves from private use toward public distribution.
