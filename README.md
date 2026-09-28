# pagevow

pagevow runs browser tests written as goals. A decision model drives a real Chromium step by step, an independent
verifier checks the final page, and every test leaves PNG screenshots. A Claude Code plugin runs the suite after a
coding task and sends failures back to Claude.

Status: phase 2. `pagevow run` works: it drives its own headless Chromium with a decision backend you already have
running, verifies each final page and writes screenshots. Starting and stopping local model servers, the Stop hook and
the plugin are not implemented yet; those commands print `not implemented yet (phase N)` and exit with code 2. The
design is in [docs/SPEC.md](docs/SPEC.md).

## Install from source

Go 1.27 or newer is required.

```
go install github.com/aymaneallaoui/pagevow/cmd/pagevow@latest
```

Or build in a checkout:

```
make build      # writes bin/pagevow
make install    # installs into GOBIN
```

## Commands

| Command | State |
|---|---|
| `pagevow version` | works |
| `pagevow status [--json]` | works: active backend, whether `run` would use a paid API, URLs, browser settings, config file |
| `pagevow use local\|jev\|custom\|cascade` | works: writes the backend into the config file |
| `pagevow keys set\|unset\|list` | works: keychain entries and a names-only index, values are never printed |
| `pagevow init [DIR]` | works: writes a starter `pagevow.yaml`, refuses when a tests file already exists |
| `pagevow run [--tests FILE] [--ids a,b] [--out DIR] [--screenshots final\|failed\|all] [--retries N] [--timeout SECONDS] [--full-page] [--json]` | works: see below |
| `pagevow start`, `stop`, `doctor` | phase 3 |
| `pagevow install`, `update` | phase 5 |
| `pagevow hook stop`, `plugin install\|uninstall\|path` | phase 4 |

## Running tests

`pagevow run` reads `pagevow.yaml` in the current directory (also `browser-tests.yaml` and
`.claude/browser-tests.yaml`) or the file given with `--tests`. Before any test it checks that the decision backend
answers, that the text helper answers when it is a loopback URL, and that a Chromium or Chrome is installed; a problem
is printed with the way to fix it and the exit code is 2. A model server for the `local` and `cascade` backends must
already be running: `pagevow start` arrives in phase 3.

`run` starts its own headless browser with a temporary profile under the user cache directory, opens every attempt in
a fresh window, and stops the browser when it ends, also after an error or Ctrl+C. The first Ctrl+C stops the current
test, writes the report for what ran and exits with code 1; a second one kills the browser and exits at once.

Output goes to `<out>/<UTC timestamp>/`, with `<out>` defaulting to `.pagevow` next to the tests file:

```
report.json
<test id>/            (retries: <test id>.retry1/)
  result.json  final.png  step-0000.png ...  <run id>.jsonl  <run id>.meta.json
```

A test passes only when the run ended `DONE` and its verifier returned true. A test without a verifier is
`UNVERIFIED`, counts as a failure and is never retried. A failed test runs again from a fresh session up to
`--retries` times. `--screenshots final` keeps only `final.png`, `failed` (the default) keeps the step screenshots of
failing attempts, `all` keeps every one. Nothing else is ever deleted. Exit codes: 0 all passed, 1 a test failed or is
unverified, 2 the run could not start. `--json` prints only the report on stdout; notes and warnings go to stderr.

## Configuration

The config file is `pagevow/config.yaml` in the user config directory. Environment variables named `PAGEVOW_*`
override it, and command line flags override both. Key fields hold references, never secrets: `keychain:NAME` or
`env:NAME`.

```
pagevow keys set typesafe < key.txt    # value comes from stdin or a hidden prompt
pagevow use jev
pagevow status
```

`keys set` and `keys unset` keep an index of stored names (names only) in `keys.json` next to `config.yaml`.
`keys list` shows every indexed name and every name the config references, each as `stored` (in the keychain), `env`
(resolved from the environment) or `missing`.

The text helper is used for `TYPE_TEXT` steps and is built when `text_helper.url` is set. `text_helper.reasoning: none`
switches the helper's reasoning off (legacy variable `TEXT_MODEL_REASONING`).

`status` prints `paid_api`: true when the backend is `jev`, or when the text helper URL is set and is not a loopback
address. The variables of the Python agent are read as a fallback: `TYPESAFE_BASE_URL` applies to
`backends.custom.url` only (`jev` keeps `https://api.typesafe.ai`), and `TYPESAFE_API_KEY` applies to both keys as
`env:TYPESAFE_API_KEY`.

## Development

```
make check      # gofmt check, go vet, golangci-lint, go test -race
```

Install the linter with `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0`.

Portions derive from browser-use/jev-ultrafast; see [NOTICE](NOTICE).
