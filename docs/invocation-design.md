# Invocation Design

## Current Decision

Use a classic ASK custom skill with explicit invocation.

Primary flow:

```text
Alexa, open Brain Slug
Brainslug is ready. What should I ask?
<free-form question>
```

The interaction model and response types implement this two-turn design. The model builds in the Developer Console, signed launch and intent requests pass, and a beta-enrolled physical Echo completes the launch and question flow. Broader one-shot recognition coverage remains a device-test item.

## Adapter Boundary

Invocation handling belongs to `internal/alexa`.

The ASK adapter owns:

- invocation name and interaction model;
- intents and slots;
- dialog directives;
- ASK reprompts and spoken messages;
- mapping Alexa identity and session data to application types.

The application service owns:

- provider execution;
- model and output-token policy;
- persistence;
- latest-answer lookup;
- stable failure categories;
- future job lifecycle.

The Alexa handler no longer invokes provider clients or constructs store records directly.

## Current Interaction Model

Invocation name:

```text
brain slug
```

Question intent:

```text
AskGPTIntent
  Query: AMAZON.SearchQuery, required
```

Current one-shot samples:

```text
about {Query}
regarding {Query}
to answer {Query}
the question {Query}
this question {Query}
```

Representative complete utterances:

```text
Alexa, ask Brain Slug about the halting problem
Alexa, ask Brain Slug regarding SQLite write-ahead logging
Alexa, tell Brain Slug to answer why the sky is blue
Alexa, ask Brain Slug the question whether a TPU laptop would be useful
```

These carriers are deliberately neutral. Alexa sends the slot value rather than the full carrier phrase, so the words outside `{Query}` should not be required to preserve the question's meaning.

The previous samples `ask {Query}`, `send {Query}`, `question {Query}`, and `ask chat g p t {Query}` were removed because they produced awkward or certification-unfriendly phrases.

## Two-Turn Flow

`LaunchRequest` currently returns:

- spoken prompt `Brainslug is ready. What should I ask?`;
- `Dialog.Delegate` directive;
- an updated `AskGPTIntent` with an unfilled `Query` slot.

The interaction model marks `Query` required and provides the elicitation prompt:

```text
What should I ask?
```

Target exchange:

```text
User:  Alexa, open Brain Slug
Alexa: Brainslug is ready. What should I ask?
User:  Explain why disappearing polymorphs are difficult to reproduce.
```

Developer Console HTTPS invocation confirms that launch-to-dialog delegation and a filled `Query` intent are accepted for the selected locale. A beta-enrolled physical Echo also confirms that the spoken launch and follow-up question flow reaches the skill.

If Amazon requires a different launch-chaining shape, change the checked-in response and interaction model together and add a fixture test.

## AMAZON.SearchQuery Constraints

`AMAZON.SearchQuery` is the appropriate built-in slot for broad question text, but normal sample utterances need a carrier phrase. A sample containing only:

```text
{Query}
```

is rejected by Alexa's model builder.

Slot elicitation is the more flexible path because the user's next utterance can fill the required slot without a one-shot carrier.

Design rules:

- only one search-query slot in the intent;
- do not combine it with another slot in the same utterance;
- keep carrier phrases small and semantically neutral;
- use real activity-history evidence before adding samples;
- do not attempt to enumerate every question form.

## Current Response Behavior

### Completed answer

```text
User:  Alexa, ask Brain Slug about SQLite write-ahead logging.
Alexa: <concise answer>
```

The application returns a completed transport-neutral answer. The ASK adapter trims and speaks the short form. The full form is stored when history is enabled.

### Provider timeout or unavailable provider

```text
Alexa: The AI provider is busy right now. Try again in a moment.
```

This is currently a failure, not an accepted deferred job.

### Output-token exhaustion

```text
Alexa: The AI provider ran out of room before it answered. The response limit may be too low.
```

OpenAI incomplete responses caused by `max_output_tokens` map to a stable output-limit category and then to this spoken response.

### Last response

Current `LastResponseIntent` returns the latest successful nonempty saved answer owned by the Alexa principal.

Current states:

- no successful history -> no previous response;
- completed saved answer -> speak it;
- repository failure -> local history error;
- empty saved success -> empty-response message.

A retrieval phrase such as "last response" spoken in answer to "What should I ask?" may be captured as the `Query` slot instead of matching `LastResponseIntent`. The adapter recognizes the `LastResponseIntent` samples and close variants in `Query` and answers them from history instead of sending them to the provider.

There is no pending or canceled job state yet.

## Deferred Response Design

Deferred behavior remains planned rather than implemented.

Target accepted flow:

```text
User:  Alexa, ask Brain Slug to compare three explanations of the Fermi paradox.
Alexa: I am working on that. Ask Brain Slug for the last response later.
```

Target retrieval states:

```text
pending   -> I am still working on it.
completed -> <concise saved answer>
failed    -> I could not finish that request.
canceled  -> That request was canceled.
```

The exact wording should be chosen after the durable job state machine exists. Do not add pending speech before the application can persist and recover the job it claims to have accepted.

## Help And Fallback

Help, fallback, and unrecognized intents answer the same way launch does: short speech followed by `Dialog.Delegate` into `AskGPTIntent`, so the dialog model elicits `Query` and the user's next utterance becomes the question.

A plain open session would not work here. Every `AskGPTIntent` sample needs a carrier phrase, so outside slot elicitation a bare question matches no intent and lands in fallback again. Amazon permits chaining from any intent into a custom intent that has a dialog model.

Current help explains what Brainslug does and teaches `ask Brain Slug what was the last answer`, which is an exact `LastResponseIntent` sample. A test keeps that phrase tied to the interaction model.

Real-device testing should determine whether help needs to give an explicit one-shot example or emphasize the two-turn flow.

## Repeat

`AMAZON.RepeatIntent` currently shares the `last response` path. It retrieves the latest saved answer rather than tracking the last speech object in the active session.

This is simple and deterministic, but it is not a semantically perfect implementation of Alexa's usual repeat behavior. Revisit it only if physical-device testing shows confusion.

## Follow-Up Questions

General multi-turn provider context is not implemented.

A follow-up design must decide:

- which prior turns are included;
- how conversation ownership is stored;
- how long a conversation remains active;
- whether deferred answers belong to the same conversation;
- how token growth is bounded;
- how the conversation is deleted.

The Alexa session remaining open briefly does not create provider conversation state.

## Optional User Routine

A user-created Alexa Routine may provide a shorter private launch phrase:

```text
When I say: think hard
Custom action: open Brain Slug
```

This only launches the skill; the question is supplied in the follow-up turn.

Do not make the product depend on programmatic routine provisioning.

## Invocation Name

Keep `brain slug` unless real-device recognition is poor.

Reasons:

- distinctive;
- two words;
- easy to say;
- already represented in the repository;
- reads naturally with the current carrier phrases.

Changing the name does not remove the `AMAZON.SearchQuery` carrier and dialog constraints.

## Name-Free Interaction

Do not depend on ASK name-free interaction or Alexa+ Natural Discovery.

Reasons:

- generic questions compete with Alexa's own answer path;
- routing is not guaranteed;
- eligibility and availability vary;
- explicit invocation is easier to test and explain.

The application core remains independent of how an inbound adapter was selected.

## Validation Checklist

Code-level:

- [x] Interaction model contains no duplicated `ask` carrier.
- [x] Third-party model trademark removed from samples.
- [x] `Query` is required.
- [x] Dialog response directive is represented in Go.
- [x] Launch delegates to the question intent.
- [x] Help, fallback, and unrecognized intents delegate to the question intent.
- [x] Spoken retrieval guidance names an exact interaction-model sample.
- [x] ASK tests use a fake application service.
- [x] Output-limit and provider failures map to stable speech.

Alexa platform:

- [ ] Interaction model builds in the selected locale.
- [ ] `Alexa, open Brain Slug` elicits the full question.
- [ ] Follow-up utterance fills `Query` without truncation.
- [ ] Current one-shot phrases work on a real Echo.
- [ ] Help, fallback, stop, cancel, repeat, and last response behave naturally.
- [ ] Activity history confirms whether failures are ASR, model routing, or backend errors.

## Open Decisions After Device Testing

- Whether to add a small number of `why` or `how` one-shot carriers.
- Exact help wording.
- Whether repeat should track only the current session's last spoken output.
- Whether a completed answer should keep the session open for one explicit follow-up.
- Exact pending/status wording after deferred jobs exist.
