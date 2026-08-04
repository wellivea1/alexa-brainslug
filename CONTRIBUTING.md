# Contributing

## Prerequisites

Go 1.26 or newer. Nothing else needs installing: the linter is pinned as a
tool dependency in `go.mod`, so `go tool` fetches and runs the same version CI
does. There is no Makefile, because the project is developed on Windows as
often as not and `make` is not reliably present.

## The loop

```bash
go build ./...
go test ./...
go tool golangci-lint run ./...
go tool golangci-lint fmt ./...
```

Before opening a pull request, run what CI runs:

```bash
go mod tidy && git diff --exit-code -- go.mod go.sum
go vet ./...
go test -race -coverprofile=coverage.out -covermode=atomic ./...
go tool cover -func=coverage.out | tail -1
go tool golangci-lint run ./...
```

`-race` needs cgo. On a stock Windows machine it is unavailable, so the race
detector runs in CI on Linux rather than locally. Run the plain `go test ./...`
locally and let CI cover the rest.

## What CI enforces

Four things fail a build:

1. `go build`, `go vet`, and `go test -race` on Linux.
2. `golangci-lint run` with the configuration in `.golangci.yml`.
3. Formatting: `golangci-lint fmt` must produce no diff.
4. ASCII only, across Go, Markdown, JSON, and YAML.
5. `docker build` succeeds and the container answers `/health`.

`go.mod` and `go.sum` must already be tidy.

## Standards

### Comments explain why, not what

The code says what it does. A comment earns its place by recording something
the code cannot: a constraint from an external API, a trade-off that was
weighed, a bug that a line prevents. `// increment the counter` above `i++` is
noise; `// Alexa gives up at 8 seconds` above a timeout is the reason the
number is what it is.

Every exported symbol needs a doc comment. Every package needs a package
comment. The linter enforces both.

### Errors

Wrap with `%w` so callers can match. Define a sentinel or a typed error when a
caller has to distinguish a failure and do something different about it, not
by reflex: `providers.ErrEmptyResponse` exists because the handler says
something specific when it happens.

Never return a provider error to a caller without passing it through
`security.RedactString` first.

### Configuration

Configuration is explicit and validated at startup. A variable that is set but
unparseable is an error naming the variable, never a silent fall back to a
default. An operator should learn about a broken `.env` when the process
refuses to start, not from a failed voice request an hour later.

### Anything spoken aloud

Text that reaches Alexa has no screen behind it. Do not reference the
dashboard, a URL, a log file, or anything else the listener cannot hear. Keep
it short, lead with the answer, and put it in a named constant next to the
others in `internal/alexa` rather than inline in a branch.

### Secrets

Never log a credential, a request signature, or a raw Alexa user ID. Providers
receive a hash of the user ID, not the ID. Prompts and answers are private
data: they can go in the local database, and they must not go anywhere else.

### Database migrations

`internal/store/migrations.go` is append-only. Once a migration has been
released, its version number and its statements are frozen; a mistake is
corrected by a new migration, never by editing an old one. Version numbers are
never reused or reordered, because applied versions are recorded by number.

There are no down migrations.

### Tests

Table-driven where there are cases, named subtests, and assertion messages
that say what was expected rather than only what was got. Prefer
`httptest.NewServer` over mocking an HTTP client.

A test for a bug should fail against the old behaviour. If it would have
passed before the fix, it is documenting the code rather than protecting it.

### ASCII only

No smart quotes, em dashes, ellipses, or arrows, anywhere in the repository.
They render badly in terminals and diffs, and text that reaches Alexa is read
aloud by a voice that does not always know what to do with them. Use `-`,
`...`, and `->`.

This is easy to violate by pasting from a browser or a chat window. CI checks
it because review does not reliably catch it.

## Pull requests

Keep the change and its tests together. Explain why in the description; the
diff already covers what. If you found something out of scope, say so rather
than fixing it in the same branch.
