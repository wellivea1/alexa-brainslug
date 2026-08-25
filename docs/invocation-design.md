# Invocation Design

## Decision

Use a classic ASK custom skill with explicit invocation.

Recommended baseline:

```text
Alexa, open Brain Slug
Brainslug is ready. What should I ask?
<free-form question>
```

Recommended one-shot forms after the interaction-model rewrite:

```text
Alexa, ask Brain Slug about the halting problem
Alexa, ask Brain Slug why my sourdough is dense
Alexa, ask Brain Slug how quantum tunneling works
Alexa, tell Brain Slug to explain the Monty Hall problem
```

Do not depend on name-free interaction or Alexa+ Natural Discovery for generic questions. Alexa+ normally has its own answer path and is not required to route an unnamed question to this private custom skill.

## Adapter Boundary

Invocation handling belongs to the ASK adapter.

The adapter owns:

- Invocation name and interaction model.
- Intent and slot mapping.
- Dialog directives and reprompts.
- Alexa-specific speech for completed, pending, failed, help, and stop states.

The application service owns:

- Submitting the question.
- Deciding whether the result completed or remains pending.
- Provider execution and job lifecycle.
- Saving and retrieving the caller's answer.

The Alexa handler should not invoke a provider directly after the application-core extraction.

## How Alexa Composes One-Shot Utterances

Alexa joins an invocation phrase to a sample utterance:

```text
[invocation phrase]        + [sample utterance]
"ask brain slug"             "about the halting problem"
```

The sample must read as the tail of a sentence that already begins with `ask Brain Slug`, `tell Brain Slug`, `open Brain Slug and`, or another supported invocation form.

The current interaction model uses samples such as:

```text
ask {Query}
send {Query}
question {Query}
ask chat g p t {Query}
```

These produce poor phrases:

```text
Alexa, ask Brain Slug ask what is SQLite
Alexa, ask Brain Slug send what is SQLite
```

The third-party product name also should not appear in a certification-bound interaction model.

## AMAZON.SearchQuery Constraint

`Query` uses `AMAZON.SearchQuery`. It is the appropriate built-in slot for flexible question text, but Amazon requires a carrier phrase around the slot in intent samples.

A sample containing only:

```text
{Query}
```

is rejected by the model builder.

Use a small set of natural carrier phrases rather than a large brittle utterance list.

Recommended one-shot samples:

```text
about {Query}
what is {Query}
why {Query}
how {Query}
whether {Query}
explain {Query}
to explain {Query}
for {Query}
the answer to {Query}
```

These should be tested in the Alexa Developer Console before replacing the shipped model. Amazon may reject or resolve some combinations differently from what looks valid on paper.

## Two-Turn Flow

The two-turn flow is the primary flexible path because slot elicitation can capture the user's whole follow-up utterance without requiring the user to say a carrier phrase.

Target behavior:

```text
User:  Alexa, open Brain Slug
Alexa: Brainslug is ready. What should I ask?
User:  Explain why disappearing polymorphs are difficult to reproduce.
```

Implementation requirements:

- Mark `Query` as required in the dialog model.
- Delegate or explicitly elicit the slot when it is missing.
- Model ASK directives in the response types.
- Preserve stop, cancel, help, fallback, and session-ended behavior.
- Normalize whitespace but do not rewrite the user's question in the adapter.

The two-turn flow should be the documented fallback whenever a one-shot phrase is not recognized.

## Completed And Deferred Responses

A provider result may or may not fit inside the ASK request deadline.

### Completed in the same turn

```text
User:  Alexa, ask Brain Slug about SQLite write-ahead logging.
Alexa: <concise answer>
```

The ASK adapter receives a completed application result and speaks the voice-safe answer.

### Accepted for deferred completion

```text
User:  Alexa, ask Brain Slug to compare three explanations of the Fermi paradox.
Alexa: I am working on that. Ask Brain Slug for the last response in a moment.
```

The exact wording may change after device testing, but it must:

- Confirm that the request was accepted.
- Avoid promising a completion time.
- Tell the user how to retrieve the result.
- End before Alexa's request deadline.

The provider job continues under an application-owned deadline independent of the Alexa HTTP request.

### Pending retrieval

```text
User:  Alexa, ask Brain Slug for the last response.
Alexa: I am still working on it.
```

### Completed retrieval

```text
User:  Alexa, ask Brain Slug for the last response.
Alexa: <concise saved answer>
```

### Failed retrieval

```text
User:  Alexa, ask Brain Slug for the last response.
Alexa: I could not finish that request. Try asking again.
```

The adapter may distinguish operator misconfiguration, provider congestion, and an exhausted output budget when that distinction gives the listener a useful next step. It must not speak provider diagnostics or credentials.

## Last Response Semantics

`LastResponseIntent` should mean the latest job owned by the authenticated principal, not the latest row in the database globally.

Recommended rules:

1. If the latest job is running, report pending.
2. If it completed, speak the short answer.
3. If it failed, speak a stable user-safe failure message.
4. If there is no job, say no previous response exists.
5. Repeat may replay the last spoken answer, but it should not accidentally select a different user's record.

After deferred jobs exist, consider separate phrases for status and completed history only if real-device testing shows the single `last response` concept is confusing.

## Follow-Up Questions

Do not add general follow-up conversation in the same change as slot elicitation and deferred jobs.

A follow-up feature needs explicit answers to:

- Which prior turns are sent to the provider.
- How long a conversation remains active.
- Whether a deferred answer belongs to the same conversation.
- How history is scoped and deleted.
- How token growth is bounded.

The existing provider request type has room for prior context, but that is not a complete conversation design. Keep the first usable version one question per job.

## Optional User Routine

A user-created Alexa Routine may provide a shorter personal launch phrase, for example:

```text
When I say: think hard
Custom action: open Brain Slug
```

This only launches the skill. The user still supplies the question in the second turn.

Do not build product logic around provisioning routines. Alexa Routines Kit was retired, so this remains an optional manual convenience for the owner.

## Invocation Name

Keep `brain slug` unless real-device recognition is poor.

Reasons:

- Distinctive.
- Two words.
- Easy to hear and say.
- Already represented in the repository.
- Reads naturally with `about`, `why`, `how`, and `to explain` carriers.

Do not switch names merely to avoid a two-turn flow. The interaction-model constraints remain.

## Name-Free Interaction

`CanFulfillIntentRequest` and ASK name-free interaction are not the same as Alexa+ Natural Discovery. Neither should be a dependency for Brainslug's core use case.

Reasons to skip it initially:

- Generic questions compete with Alexa's own answer path.
- Routing is not guaranteed.
- Availability and eligibility vary.
- Explicit invocation is easier to test and explain.

The application architecture should not know whether an adapter was reached by explicit invocation or another platform routing mechanism.

## Interaction Model Acceptance Tests

Before calling the invocation rewrite complete:

- The model builds without warnings or unsupported utterances.
- `Alexa, open Brain Slug` elicits a missing query.
- The full follow-up utterance fills `Query`.
- At least the recommended `about`, `why`, `how`, and `to explain` one-shot forms work on a real Echo.
- Stop and cancel end the session.
- Help explains both one-shot and two-turn use briefly.
- Fallback returns to question elicitation rather than ending unnecessarily.
- A pending application result produces the deferred acknowledgement.
- `last response` distinguishes no history, pending, completed, and failed states.
- No sample utterance contains a third-party model or product trademark.

## Open Decisions For Device Testing

- Exact pending acknowledgement wording.
- Whether a completed answer should keep the session open for one follow-up.
- Whether `last response` and `status` need separate intents.
- Whether `repeat` should replay only the current session response or the latest stored answer.

These decisions should be made from real Echo behavior after the basic two-turn flow works, not from interaction-model speculation alone.
