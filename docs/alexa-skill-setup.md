# Alexa Skill Setup

This project is intended for a private custom skill, not Skill Store publication.

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

Example phrases:

```text
Alexa, open Brain Slug
Alexa, ask Brain Slug ask what is SQLite
Alexa, ask Brain Slug last response
```

## Notes About AMAZON.SearchQuery

`AMAZON.SearchQuery` captures free-form speech better than a custom slot for arbitrary questions. It can still be sensitive to sample utterances and invocation phrasing. If Alexa misses the query, add a small number of natural samples rather than building a large conversation model.

Do not overbuild multi-turn conversation in the first version. The local dashboard stores full answers for review.

## Testing With Verification Disabled

The initial code intentionally fails closed when `ALEXA_VERIFY_REQUESTS=true` because full request signature verification is not implemented yet.

For local skill testing only:

```text
ALEXA_VERIFY_REQUESTS=false
```

Do not leave this disabled on a publicly reachable endpoint.
