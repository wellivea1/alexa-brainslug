# Alexa Skill Notes

Invocation name: `brain slug`

The custom interaction model is intentionally small. The skill supports:

- `LaunchRequest`: "Alexa, open Brain Slug"
- `AskGPTIntent`: "Alexa, ask Brain Slug ask why the sky is blue"
- `LastResponseIntent`: "Alexa, ask Brain Slug last response"

`AMAZON.SearchQuery` is used for the free-form `Query` slot. Alexa may require
testing and sample utterance tuning for your account and locale.

The skill endpoint should be:

```text
https://your-domain.example/alexa
```

Do not expose this endpoint publicly with `ALEXA_VERIFY_REQUESTS=false`.
