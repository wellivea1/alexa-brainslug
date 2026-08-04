# Alexa Skill Notes

Invocation name: `brain slug`

The custom interaction model is intentionally small. The skill supports:

- `LaunchRequest`: "Alexa, open Brain Slug"
- `AskGPTIntent`: "Alexa, ask Brain Slug ask why the sky is blue"
- `LastResponseIntent`: "Alexa, ask Brain Slug last response"

`AMAZON.SearchQuery` is used for the free-form `Query` slot. That slot type
requires a carrier phrase in every sample utterance, which is what makes the
one-shot phrasing awkward.

The sample utterances currently in `interaction-model.json` were written as
whole sentences. Alexa supplies the invocation phrase and appends the sample, so
`ask {Query}` becomes "Alexa, ask Brain Slug ask what is SQLite". A sample has
to read as the tail of a sentence that already begins "ask brain slug".

`ask chat g p t {Query}` also references a third-party trademark and should be
removed before any certification attempt.

See `../docs/invocation-design.md` for replacements and for the other activation
routes, including slot elicitation and user-created routines.

The skill endpoint should be:

```text
https://your-domain.example/alexa
```

Do not expose this endpoint publicly with `ALEXA_VERIFY_REQUESTS=false`.
