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
| `pagevow run [--tests FILE] [--ids a,b] [--out DIR] [--screenshots final\|failed\|all] [--retries N] [--timeout SECONDS] [--full-page] [--headed] [--json]` | works: see below |
| `pagevow start`, `stop`, `doctor` | phase 3 |
| `pagevow install`, `update` | phase 5 |
| `pagevow hook stop`, `plugin install\|uninstall\|path` | phase 4 |

## Running tests

`pagevow run` reads `pagevow.yaml` in the current directory (also `browser-tests.yaml` and
`.claude/browser-tests.yaml`) or the file given with `--tests`. Before any test it checks that the decision backend
answers, that the text helper answers when it is a loopback URL, and that a Chromium or Chrome is installed; a problem
is printed with the way to fix it and the exit code is 2. A model server for the `local` and `cascade` backends must
already be running: `pagevow start` arrives in phase 3.

`run` starts its own browser with a temporary profile under the user cache directory, opens every attempt in
a fresh window, and stops the browser when it ends, also after an error or Ctrl+C. The browser is headless unless
`browser.headless` is `false` in the config (or `PAGEVOW_BROWSER_HEADLESS=false`) or you pass `--headed`; `status` shows
the same setting. `--timeout` covers opening the page as well as the steps: a page that never loads ends the attempt
with status `timeout`. The first Ctrl+C stops the current
test, writes the report for what ran and exits with code 1; a second one kills the browser and exits at once.

Output goes to `<out>/<UTC timestamp>/`, with `<out>` defaulting to `.pagevow` next to the tests file:

```
report.json
<test id>/            (retries: <test id>.retry1/)
  result.json  final.png  step-0000.png ...  <run id>.jsonl  <run id>.meta.json
```

Directory names are planned before the first test runs. Characters other than letters, digits, dot, underscore and
hyphen become `_`, names are cut to 120 bytes with a short hash, Windows device names such as `CON` or `nul.txt` get a
`_` prefix, and two ids that would share a directory (same name apart from case, trailing dots, or a name another
test's retry directory would use) get a `-2`, `-3` suffix. Every attempt has its own directory, and `report.json` lists
it as `directory`. If one directory cannot be created, that test fails and the rest of the suite runs.

A test passes only when the run ended `DONE` and its verifier returned true. A test without a verifier is
`UNVERIFIED`, counts as a failure and is never retried. A failed test runs again from a fresh session up to
`--retries` times. `--screenshots final` keeps only `final.png`, `failed` (the default) keeps the step screenshots of
failing attempts, `all` keeps every one. Nothing else is ever deleted. Exit codes: 0 all passed, 1 a test failed or is
unverified, 2 the run could not start. `--json` prints only the report on stdout; notes and warnings go to stderr.

### Warnings

While a test runs, pagevow notes what the page did that the agent did not choose or cannot see: a JavaScript dialog that
was accepted automatically, a download that was denied (pagevow never saves downloads), a new tab the page opened (the
agent stays on the original tab), and an iframe from another origin or one that holds controls. Each note is a line in
`warnings` in `result.json`, an empty list when there is none, and is printed under the test line, also for passing tests.
`report.json` counts the tests with a warning in `totals.tests_with_warnings`. Warnings never change a verdict or an
exit code.

When a model request fails, the failure block also prints `cause:` with the network error behind the fixed message, for
example `connection refused`; it never contains a key.

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

`status` prints `paid_api`, and `run` prints a notice, when a service would be called that is not on this machine and
has a real API key: the active backend (any kind), or the text helper, has a URL that is not loopback and a key
reference that resolves to a value other than the placeholder `local`. Only literal loopback addresses (`127.0.0.0/8`,
`::1`) and the exact host `localhost` count as this machine; `api.localhost` does not. A `custom` backend pointing at
`https://api.typesafe.ai` with a key therefore warns like `jev` does. The variables of the Python agent are read as a fallback: `TYPESAFE_BASE_URL` applies to
`backends.custom.url` only (`jev` keeps `https://api.typesafe.ai`), and `TYPESAFE_API_KEY` applies to both keys as
`env:TYPESAFE_API_KEY`.

`backends.cascade.target_conf` is at most 1. `0` means the default 0.5 and a negative value never asks the verifier
because of a low target confidence (`pagevow use cascade --target-conf -1`).

## Verifiers

A test file names a verifier with `verify` and its arguments with `verify_args` (see the spec, section 7). Three rules
differ from a plain Go or Python reading:

- URL patterns behave as in the Python runner, not as raw RE2. `\d`, `\w` and `\s` cover Unicode text, `$` also
  matches before a final newline, `\Z` is the end of the text, and `x{,3}` repeats up to 3 times. pagevow translates
  the pattern when the tests file loads. `\b` and `\B` are rejected with a message, because RE2 cannot evaluate
  them on Unicode text; write the boundary out, for example `(^|[^\p{L}\p{N}_])word($|[^\p{L}\p{N}_])`. Lookaround,
  backreferences and the `a`, `L` and `x` flags are not supported and are rejected the same way.
- A tag such as `!!str` in `verify_args` is honoured: `fields: {Subscribe: !!str yes}` expects the text `yes`, while a
  plain `yes` is the boolean true.
- A verifier that would run no checks (`verify_args: {}`, `url: []`, empty lists everywhere, or `verify: page` with no
  arguments) is rejected when the file loads, with the test id in the message. The Python runner accepts such a test
  and passes it on any page; pagevow is stricter on purpose.

## Development

```
make check      # gofmt check, go vet, golangci-lint, go test -race
```

Install the linter with `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0`.

Portions derive from browser-use/jev-ultrafast; see [NOTICE](NOTICE).
