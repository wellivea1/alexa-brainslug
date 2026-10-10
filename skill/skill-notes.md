# Alexa Skill Notes

Invocation name: `brain slug`

The custom interaction model is intentionally small. The skill supports:

- `LaunchRequest`: "Alexa, open Brain Slug", then the question in the next turn.
- `AskGPTIntent`: "Alexa, ask Brain Slug about why the sky is blue".
- `LastResponseIntent`: "Alexa, ask Brain Slug what was the last answer".

`AMAZON.SearchQuery` is used for the free-form `Query` slot. That slot type
requires a carrier phrase in every sample utterance, which is why the one-shot
samples are `about {Query}`, `regarding {Query}`, `to answer {Query}`,
`the question {Query}`, and `this question {Query}`. Alexa supplies the
invocation phrase and appends the sample, so each sample has to read as the
tail of a sentence that already begins "ask Brain Slug". Alexa sends only the
slot value, so a carrier must not carry meaning the question needs: `why
{Query}` would drop the word "why".

A bare question has no carrier, so it only fills `Query` while dialog
management is eliciting the slot. Launch, help, fallback, and unrecognized
intents therefore all answer with `Dialog.Delegate` into `AskGPTIntent`, and
the next utterance becomes the question. A retrieval phrase such as "last
response" captured as the `Query` is answered from history rather than sent to
the provider.

See `../docs/invocation-design.md` for the full dialog design and for the other
activation routes, including user-created routines.

The skill endpoint should be:

```text
https://your-domain.example/alexa
```

Do not expose this endpoint publicly with `ALEXA_VERIFY_REQUESTS=false`.
