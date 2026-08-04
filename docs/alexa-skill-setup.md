# Alexa Skill Setup

This covers setting up a private custom skill for your own account. Skill Store publication is a possible future direction with prerequisites that do not exist yet - see `security.md`.

## Steps

1. Open the Alexa Developer Console.
2. Create a new custom skill.
3. Choose a custom model.
4. Set the invocation name to `brain slug`.
5. Open the JSON editor for the interaction model.
6. Paste the contents of `skill/interaction-model.json`.
7. Save and build the model.
8. Configure the endpoint as HTTPS.
9. Set the default region endpoint to:

```text
https://your-domain.example/alexa
```

10. Test the skill.

Example phrases, as the model stands today:

```text
Alexa, open Brain Slug
Alexa, ask Brain Slug ask what is SQLite
Alexa, ask Brain Slug last response
```

The second one says "ask" twice. That is a defect in the sample utterances, not in the console setup - see `invocation-design.md` for the replacements.

## Notes About AMAZON.SearchQuery

`AMAZON.SearchQuery` captures free-form speech better than a custom slot for arbitrary questions, but it is greedy, so Amazon requires a carrier phrase in every sample utterance. A sample of just `{Query}` is rejected at build time.

The carrier phrase has to read as a continuation of the invocation phrase Alexa supplies, because the two are concatenated. If Alexa misses the query, add a small number of natural samples rather than building a large conversation model.

Slot elicitation is the exception: during a dialog the user's whole utterance fills the slot with no carrier phrase needed. That makes the two-turn flow more permissive than the one-shot flow, and it is worth adding.

## Testing With Verification Disabled

The initial code intentionally fails closed when `ALEXA_VERIFY_REQUESTS=true` because full request signature verification is not implemented yet.

For local skill testing only:

```text
ALEXA_VERIFY_REQUESTS=false
```

Do not leave this disabled on a publicly reachable endpoint.
