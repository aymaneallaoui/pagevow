# pagevow

Go CLI that runs browser tests written as goals. The code and the reference sections of `README.md` (commands, tests
file, run output, exit codes, configuration, platforms) are the contract. Read them before editing. When code and
README disagree, fix one of them in the same change.

## Go development

Before any Go coding, review, debugging, troubleshooting, or setup task, load the `samber/cc-skills-golang@golang-how-to` skill first: it routes to whichever other Go skills the task needs.

## Principles

1. The model chooses among observed elements and supported operations. It never emits selectors or code.
2. A `DONE` from the model is not proof. A test passes only when the run ended `DONE` and the verifier returned true.
3. Never retry a browser mutation. A retry is a new run from the start URL.
4. Screenshots are evidence for people and for Claude. The decision model never receives them, and a failed screenshot
   never changes a verdict.
5. No site-specific logic and no hardcoded field values.
6. Secrets live in the OS keychain or the environment, never in config files, test files, traces, logs or the plugin.
7. The active backend is always visible, so a paid API is never used by surprise.
8. Every command works without a terminal (CI, hooks): no prompt unless stdin is a terminal, plain output when piped.

## Engineering rules

- Code comments only when necessary (a non-obvious invariant, a workaround, a concurrency reason), never longer than
  two sentences. Godoc on exported identifiers is one sentence.
- Errors are wrapped with context (`fmt.Errorf("...: %w", err)`); no panics outside `main` start-up.
- No global mutable state. No `init()` side effects except cobra command registration.
- Layers: `internal/cli` depends on everything and nothing depends on it. Services are registered with `samber/do` in
  `internal/cli/container.go`; other packages never import the container. Interfaces are small and declared where
  they are used. Exported functions that can block take a `context.Context` first.
- Tests are offline: no network, no real browser, no model, no paid API, no real keychain, no real config directory.
  Live checks run only through explicit commands and never as part of `go test ./...`.
- `gofmt -s`, `go vet`, `golangci-lint` and `go test -race ./...` must pass before a change is reported as done
  (`make check`).
- Commit subjects: one sentence, conventional commits, lowercase. No AI attribution anywhere in git history.
- Plain wording in documents. No em dashes.
- Do not commit, push or tag unless the user asks.
