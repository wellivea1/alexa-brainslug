# Invocation Design

Options for how a user activates Alexa Brainslug and asks it a question. The
goal is more than one natural-sounding route, not a single canonical phrase.

Nothing here is committed. Every sample utterance has to survive a model build
in the Alexa Developer Console, which rejects some combinations that look fine
on paper.

## How Alexa Composes an Utterance

This is the part that causes most of the awkwardness, so it is worth stating
plainly. What the user says is two pieces glued together:

```text
[invocation phrase]        +  [sample utterance from interaction-model.json]
"ask brain slug"              "about the halting problem"
```

Alexa supplies the invocation phrase. Supported forms include:

- `ask {name} {utterance}`
- `ask {name} about {utterance}`
- `tell {name} to {utterance}`
- `search {name} for {utterance}`
- `open {name} and {utterance}`
- `{utterance} from {name}`

Connecting words that Alexa recognises between the name and the utterance
include `to`, `about`, `for`, `if`, `whether`, `using`, `with`, `from`, `by`,
and `in`. The invocation name can appear at the start, middle, or end.

The interaction model only controls the second half. So a sample utterance
should read as the **tail** of a sentence that already begins "ask brain
slug...", not as a standalone command.

## What Is Wrong Today

`skill/interaction-model.json` currently uses:

```text
ask {Query}
send {Query}
question {Query}
ask chat g p t {Query}
```

Glued to the invocation phrase, these produce:

- "Alexa, ask brain slug **ask** what is SQLite" - says "ask" twice
- "Alexa, ask brain slug **send** what is SQLite" - not English
- "Alexa, ask brain slug **question** what is SQLite" - not English

The samples were written as if they were the whole sentence. They are not.

Separately, `ask chat g p t {Query}` names a third-party product. That is a
trademark reference and should be removed before any certification attempt.

## Why a Carrier Phrase Is Required

`Query` is an `AMAZON.SearchQuery` slot. That slot type is greedy - it will
swallow anything - so Amazon requires every sample utterance to contain at
least one non-slot word anchoring it. A sample of just `{Query}` is rejected
with "Sample utterances for this intent should include a carrier phrase around
the AMAZON.SearchQuery slot."

The carrier phrase is therefore not optional. The design problem is choosing
carriers that read naturally *after* "ask brain slug".

## Option 1: Rewrite the Sample Utterances

Lowest effort, biggest improvement. Replace the current four with a set whose
carriers double as natural connecting words:

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

Which yields:

- "Alexa, ask brain slug **about** the halting problem"
- "Alexa, ask brain slug **what is** a merkle tree"
- "Alexa, ask brain slug **why** my sourdough is dense"
- "Alexa, ask brain slug **how** photosynthesis works"
- "Alexa, ask brain slug **whether** this is a real word"
- "Alexa, tell brain slug **to explain** quantum tunneling"
- "Alexa, open brain slug and **explain** the Monty Hall problem"
- "Alexa, search brain slug **for** the capital of Burkina Faso"

Trade-off: question-word carriers cover most natural phrasings but not bare
noun phrases. "Alexa, ask brain slug the halting problem" will not match and
falls through to `AMAZON.FallbackIntent`. `the answer to {Query}` and
`about {Query}` cover a lot of that gap.

## Option 2: Two-Turn Session With Slot Elicitation

The carrier-phrase rule applies to *intent* samples. During dialog slot
elicitation the user's entire next utterance fills the slot with no carrier
needed. That makes the two-turn flow strictly more permissive than the
one-shot flow:

```text
User:  "Alexa, open brain slug"
Alexa: "Brainslug is ready. What should I ask?"
User:  "the halting problem"          <- anything at all matches here
```

This requires adding dialog management to `AskGPTIntent` (mark `Query`
required, let Alexa elicit it) and handling `Dialog.Delegate` /
`Dialog.ElicitSlot` directives in `internal/alexa/response.go`, which does not
model directives yet.

Worth doing regardless of Option 1 - it is the only route that accepts
arbitrary phrasing, and it pairs naturally with keeping the session open for
follow-up questions.

## Option 3: User-Created Routines (Arbitrary Short Phrases)

The only way to get a completely custom trigger phrase. In the Alexa app the
user creates a routine: *When you say "**think hard**" -> Alexa says/does ->
Custom -> "open brain slug"*.

This escapes every invocation-name rule. Candidate phrases: "think hard",
"big brain time", "deep question", "second opinion", "phone a friend".

Trade-offs: per-user manual setup, cannot pass a question through in one shot
(it can only launch the skill, so it must be combined with Option 2), and
routine triggers are account-wide. Note that Alexa Routines **Kit** - the
developer API for provisioning routines - was retired on 2026-05-13, so this
cannot be shipped pre-configured; it is a documented setup step for the user.

## Option 4: Name-Free Interaction (`CanFulfillIntentRequest`)

Lets Alexa route an un-named request ("Alexa, what is a merkle tree") to the
skill when it declares it can fulfil the intent. Requires implementing the
`CanFulfillIntentRequest` interface.

Realistically low value here: Alexa+ answers general questions itself, so it
will not hand a generic question to a third-party skill. Listed for
completeness; not recommended.

## Alternative Invocation Names

Amazon's rules, verbatim where they matter:

- Two or more words, unless the name is a brand you can prove you own
- Lower-case alphabetic characters only
- No `alexa`, `amazon`, `echo`, `skill`, `app`
- No launch verbs: `ask`, `open`, `tell`, `start`, `play`, `run`, `use`,
  `launch`, `begin`, `enable`
- Two-word names may not use articles (`the`, `a`, `an`) or prepositions
  (`for`, `to`, `of`, `about`, `with`, `by`, `at`, `in`)
- No connecting words: `and`, `or`, `if`, `whether`, `that`, `using`
- Numbers spelled out; hyphens generally not allowed
- Must be distinctive - overly generic names get rejected at certification

Candidates that satisfy all of the above:

| Name | Reads as | Notes |
| --- | --- | --- |
| `brain slug` | "ask brain slug about..." | Current. Distinctive, two clear syllables, good ASR. |
| `deep thought` | "ask deep thought about..." | Reads the most naturally of any option. Possible IP concern (Hitchhiker's Guide) at certification. |
| `second opinion` | "ask second opinion about..." | Matches the actual purpose exactly. Slightly stilted without an article. |
| `brain trust` | "ask brain trust about..." | Natural, distinctive. |
| `big brain` | "ask big brain about..." | Casual; very easy to say. |
| `think tank` | "ask think tank about..." | Natural but generic - certification risk. |

`the oracle` and similar are disallowed (leading article in a two-word name).

Because a routine (Option 3) can front any of these with a phrase the user
prefers, the invocation name matters less than it looks. Recommendation: keep
`brain slug` - it is distinctive, ASR-friendly, and already in the repo.

## Recommended Combination

1. Rewrite the sample utterances (Option 1) - cheap, fixes the worst of it.
2. Add slot elicitation and keep the session open (Option 2) - makes the
   two-turn flow accept anything, and is required for the deferred-answer flow
   to feel conversational.
3. Document the routine trick (Option 3) in the README as an optional setup
   step for a short custom phrase.

Skip Option 4.

## Certification Notes

Relevant only if the skill is published to the store:

- Remove `ask chat g p t {Query}` - third-party trademark.
- A skill that forwards arbitrary user speech to a third-party AI API needs a
  privacy policy URL, and should disclose the AI use in the description.
- Invocation name must not be generic; `brain slug` is fine, `think tank` is
  borderline.

## Open Decisions

- Keep `brain slug`, or switch to `deep thought` / `second opinion`?
- Should the session stay open after an answer for follow-up questions, or end
  each turn (current behaviour is end-of-turn via `NewTell`)?
- Should `LastResponseIntent` keep its own utterances, or fold into a single
  "what did you find" phrasing once deferred answers exist?
