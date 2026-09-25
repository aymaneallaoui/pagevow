# pagevow specification

Status: draft 1, 2026-09-29. This file is the contract for everyone building pagevow. When code and this file disagree,
fix one of them in the same change.

## 1. What pagevow is

pagevow runs browser tests written as goals. A decision model drives a real Chromium step by step, an independent
verifier checks the final page, and every test leaves PNG screenshots. A Claude Code plugin runs the suite after a
coding task and sends failures back to Claude.

One binary, `pagevow`, does everything: it sets up the browser, selects and manages the decision model backend, runs
tests, and installs the plugin. The plugin holds no logic; it calls the binary.

Origin: a Go rewrite of the agent loop in `browser-use/jev-ultrafast` (MIT, Copyright (c) 2026 Browser Use), as
extended in the fork `aymaneallaoui/jev-ultrafast`. `NOTICE` carries the attribution. The reference implementation for
behaviour is the Python source in `~/jev-ultrafast` at branch `browser-tests`:

| Behaviour | Reference file |
|---|---|
| Request body, action space, questions, cascade, veto cache, text helper | `jev_ultrafast/model.py`, `jev_ultrafast/questions.py` |
| Agent loop, refusals, budgets, guards | `jev_ultrafast/agent.py` |
| Browser actions, freshness, settle wait | `jev_ultrafast/browser.py` |
| Page snapshot (runs inside the page) | `jev_ultrafast/snapshot.js` |
| Verifiers and failure reasons | `jev_ultrafast/verifiers.py` |
| Trace format | `jev_ultrafast/tracing.py` |
| Test runner, screenshots, report, exit codes | `scripts/browser_test.py`, `scripts/collect.py` |
| Stop hook, skill | `integrations/claude-code/` |

## 2. Principles

1. The model chooses among observed elements and supported operations. It never emits selectors or code.
2. A `DONE` from the model is not proof. A test passes only when the run ended `DONE` and the verifier returned true.
3. Never retry a browser mutation. A retry is a new run from the start URL.
4. Screenshots are evidence for people and for Claude. The decision model never receives them, and a failed screenshot
   never changes a verdict.
5. No site-specific logic and no hardcoded field values.
6. Secrets live in the OS keychain or the environment, never in config files, test files, traces, logs or the plugin.
7. The active backend is always visible, so a paid API is never used by surprise.
8. Every command works without a terminal (CI, hooks): no prompt unless stdin is a terminal, plain output when piped.

## 3. Decisions taken

| Topic | Decision |
|---|---|
| Name, module | `pagevow`, `github.com/aymaneallaoui/pagevow` |
| Language | Go (toolchain 1.27) |
| Architecture | layered CLI: `cmd/pagevow` + `internal/` packages by job |
| Dependency injection | `github.com/samber/do/v2` |
| Commands, config | `spf13/cobra`, `spf13/viper` |
| Browser control | `chromedp/chromedp` with `cdproto`, attached to a Chromium that pagevow starts |
| Terminal output | `charmbracelet/lipgloss`, `charmbracelet/huh` for prompts |
| Keys | `zalando/go-keyring` |
| Tests | stdlib `testing` + `stretchr/testify` |
| Release | `goreleaser`: Linux, macOS, Windows; amd64 and arm64 |
| Self update | `pagevow update` from GitHub releases, checksum verified |
| Logging | stdlib `log/slog` to stderr |
| Repository | private: `aymaneallaoui/pagevow` |
| Licence | MIT, Copyright (c) 2026 aymane aallaoui; `NOTICE` credits Browser Use for the ported loop |
| Commit dates | author and committer dates spread from 2026-09-22 to the current day, in increasing order, never in the future |

## 4. Commands

```
pagevow install [--browser] [--model NAME]   download the browser, and optionally a local model
pagevow use local|jev|custom|cascade [...]   choose the decision backend
pagevow start                                start what the active backend needs, and the browser
pagevow stop                                 stop everything pagevow started
pagevow status [--json]                      backend, ports, health, versions, GPU memory when known
pagevow doctor                               check the setup and say how to fix each problem
pagevow init [DIR]                           write a starter pagevow.yaml
pagevow run [--tests FILE] [--ids a,b] [--out DIR] [--screenshots final|failed|all]
            [--retries N] [--timeout SECONDS] [--full-page] [--json]
pagevow hook stop                            Claude Code Stop hook entry: reads hook JSON on stdin
pagevow plugin install|uninstall|path        manage the Claude Code plugin
pagevow keys set|unset|list NAME             keychain entries (values are never printed)
pagevow update                               replace the binary with the latest release
pagevow version
```

Exit codes of `run`: 0 all passed; 1 at least one test failed or is unverified; 2 infrastructure problem (backend or
browser not reachable, invalid tests file). `hook stop` follows section 10.

## 5. Packages

```
cmd/pagevow/            main: build the container, run the root command
internal/cli/           cobra commands, flag parsing, output selection; no business logic
internal/config/        viper: file, env, flags; paths per OS; schema and defaults
internal/keys/          keychain access with an environment fallback
internal/backend/       /v1/systemone client, request building, response validation, cascade, veto cache
internal/texthelper/    OpenAI-compatible chat client that returns one field value, total time budget
internal/browser/       Chromium download, launch, stop; CDP session; snapshot; actions; screenshots
internal/agent/         the loop: observe, decide, act; refusals; budgets; loop guard; confidence gates
internal/verify/        verifiers and failure reasons
internal/trace/         JSONL step traces and meta files
internal/runner/        tests file, attempts, retries, screenshot policy, result.json, report.json, exit code
internal/server/        local model server lifecycle (start, stop, health, PID files)
internal/hook/          Stop hook logic: fingerprint, block counter, mapping to exit codes
internal/plugin/        embedded plugin files and their installation
internal/ui/            lipgloss styles, tables, plain fallback
internal/update/        self update
internal/version/       version, commit, build date set at link time
```

Rules:
- `internal/cli` depends on everything; nothing depends on `internal/cli`.
- `internal/agent` depends on interfaces declared in `internal/agent` (consumer side), implemented by `backend`,
  `texthelper`, `browser`.
- Interfaces are small and declared where they are used.
- Every exported function that can block takes a `context.Context` first.
- Services are registered in `samber/do` providers in `internal/cli/container.go`; packages themselves do not import
  the container.

## 6. Configuration

File: `pagevow.yaml` in the user config directory (`os.UserConfigDir()/pagevow/config.yaml`), overridden by environment
variables `PAGEVOW_*`, overridden by flags.

```yaml
backend: local            # local | jev | custom | cascade
backends:
  local:   {url: "http://127.0.0.1:8009", model: jev-4b, mode: nf4}
  jev:     {url: "https://api.typesafe.ai", key: keychain:typesafe}
  custom:  {url: "", key: ""}
  cascade: {primary: "http://127.0.0.1:8009", verifier: "http://127.0.0.1:8010", target_conf: 0.5, veto_cache: true}
text_helper: {url: "", model: "", key: keychain:text-helper, timeout_seconds: 20}
browser: {port: 9333, headless: true, viewport: {width: 1480, height: 780}, channel: chrome-for-testing}
run: {retries: 1, timeout_seconds: 120, screenshots: failed, max_steps: 60}
guards: {loop_guard: false, done_min_conf: 0, blocked_min_conf: 0}
```

`key` values are references (`keychain:<name>` or `env:<NAME>`), never literal secrets. A literal value is rejected
with an error that names the fix.

Compatibility: the environment variables of the Python agent (`TYPESAFE_BASE_URL`, `TYPESAFE_API_KEY`,
`JEV_VERIFIER_BASE_URL`, `TEXT_MODEL_BASE_URL`, `TEXT_MODEL`, `TEXT_MODEL_API_KEY`, `TEXT_TIMEOUT_S`) are read as a
fallback when the matching `PAGEVOW_*` variable and config entry are absent.

## 7. Tests file

`pagevow.yaml` in the project root (also accepted: `browser-tests.yaml`, `.claude/browser-tests.yaml`, for
compatibility with the Python runner). A YAML list:

```yaml
- id: contact-form
  url: http://localhost:3000/contact.html
  goal: "On the contact page, enter name Ada Lovelace and email ada@example.com. Stop when both values are in the form. Do not send."
  tags: [shelf]
  verify: page
  verify_args:
    url: /contact\.html
    fields: {Name: Ada Lovelace, Email: ada@example.com}
```

Verifier `page` arguments, same meaning as the Python `page` verifier: `url` (regex or list), `text` (list of strings
visible on the page), `fields` (label to expected value, booleans for checkboxes), `values` (list), `checked` (label to
count). Goals may use `{date+N}` and `{date+N:FORMAT}` as in `scripts/collect.py`.

Project-level config name clash: the user config file is `config.yaml` in the config directory; `pagevow.yaml` in a
project is always a tests file.

## 8. Run output

```
<out>/<UTC YYYYmmddTHHMMSS>/
  report.json
  <test id>/            (retries: <test id>.retry1/)
    result.json
    final.png
    step-0000.png ...
    <run id>.jsonl      trace
    <run id>.meta.json
```

`<out>` defaults to `.pagevow` in the project. Nothing is ever deleted by `run`. `result.json` and `report.json` keep
the field names of `scripts/browser_test.py` (including `outcome`, `failed_checks`, `screenshot_errors`,
`final_from_step`, `missing_screenshots`), so tools written for the Python runner keep working. Traces keep the format
of `jev_ultrafast/tracing.py`, so `scripts/traces_to_kev.py` can turn pagevow runs into training data.

## 9. Backends

All backends speak `POST <url>/v1/systemone` with a bearer key.

| Backend | What `start` does | Needs |
|---|---|---|
| `jev` | nothing | key in keychain |
| `custom` | nothing | reachable URL |
| `local` | starts the model server as a background process, waits for `/v1/models` | Linux + NVIDIA in phase 2, macOS in phase 3 |
| `cascade` | starts primary and verifier, largest first | as `local` |

Local model serving stays in Python (`kev.serve`). `internal/server` manages it as a child process: it never links to
Python. Process records live in the state directory (`os.UserCacheDir()/pagevow/run/*.json`: pid, port, command, start
time); `stop` only ever stops processes recorded there and verifies the command line before sending a signal.

GPU safety: before starting a local model, `start` reads free GPU memory (`nvidia-smi`, when present) and refuses when
the model's known peak plus a 1.5 GiB margin does not fit, with the numbers in the message. Known peaks: `nf4` 5.6 GiB,
`int8` 7.2 GiB, `bf16` 10.6 GiB, `08b` 6.4 GiB.

## 10. Stop hook

`pagevow hook stop` reads the Claude Code hook JSON on stdin and behaves as
`integrations/claude-code/hooks/browser-tests-stop.sh` does:

| Situation | Exit | Output |
|---|---|---|
| `PAGEVOW_HOOK=0`, or no tests file | 0 | none |
| Project fingerprint equals the last passing one | 0 | none |
| Suite passes | 0 | none; fingerprint stored, block counter removed |
| Suite fails, blocks so far < cap (default 2) | 2 | stderr: failure report, `final.png` paths, `block N of M` |
| Suite fails, cap reached | 1 | stderr: tests still fail, Claude may stop, counter reset |
| Backend or browser not reachable | 1 | stderr: what is missing and the command to start it |

The fingerprint covers HEAD, tracked changes, staged changes and untracked files by content (symlinks by target), and
excludes `.pagevow/` and the plugin's own files. The hook never starts a local model by itself.

## 11. Claude Code plugin

Embedded in the binary, written by `pagevow plugin install`:

```
.claude-plugin/plugin.json
skills/pagevow/SKILL.md
hooks/hooks.json                  Stop -> "pagevow hook stop"
commands/pagevow-run.md
commands/pagevow-init.md
```

The skill text follows `integrations/claude-code/skills/browser-test/SKILL.md`, with commands changed to `pagevow`.
It keeps these rules: Claude does not start a local model on its own initiative; no secrets in goals; never weaken a
verifier to make a test pass; report remaining failures plainly.

## 12. Browser

- `pagevow install --browser` downloads a pinned Chrome for Testing build for the current OS and architecture into the
  data directory, verifies its checksum, and records the version.
- `start` launches it headless with its own profile directory and a fixed debugging port bound to 127.0.0.1.
- Each test gets a new target (tab) and closes it at the end.
- Headless is the default because a hidden window under Wayland receives no frames and screenshots hang. With
  `browser.headless: false` on Linux, pagevow adds `--ozone-platform=x11`.
- Screenshot capture uses a 5 second timeout. After the first failure in a test no further step capture is tried;
  `final.png` is tried once, then recovered from the last step screenshot when the run ended `DONE` or `BLOCKED`.

## 13. Phases

| Phase | Content | Done when |
|---|---|---|
| 0 | Scaffold: module, commands as stubs, container, config, ui, version, Makefile, lint, CI, NOTICE | `make check` passes; every command prints help |
| 1 | `backend`, `texthelper`, `verify`, `trace`, `keys` with unit tests | request bodies and verifier results match the Python reference on recorded fixtures |
| 2 | `browser`, `agent`, `runner` | demo suite (`~/browser-test-demo`, 10 tests) gives the same verdicts as the Python runner with backend `custom` |
| 3 | `server` (Linux + NVIDIA), `start`, `stop`, `status`, `doctor` | `pagevow use local && pagevow start && pagevow run` works from a clean state |
| 4 | `hook`, `plugin` | a real Claude Code session is blocked by a failing test and released after the fix |
| 5 | `install --browser` on three systems, `update`, goreleaser, CI matrix | release archives for Linux, macOS, Windows |
| 6 | macOS local model through MLX | same suite passes on Apple Silicon |

Parity fixtures for phase 1 come from recorded traces in `~/jev-traces` (request bodies and answers) and from verifier
calls on recorded final pages; they are copied into `testdata/` with page text trimmed and no personal data.

## 14. Engineering rules

- Code comments only when necessary (a non-obvious invariant, a workaround, a concurrency reason), never longer than
  two sentences. Godoc on exported identifiers is one sentence.
- Errors are wrapped with context (`fmt.Errorf("...: %w", err)`); no panics outside `main` start-up.
- No global mutable state. No `init()` side effects except cobra command registration.
- Tests are offline: no network, no real browser, no model, no paid API. Live checks run only through explicit
  commands and are never part of `go test ./...`.
- `gofmt -s`, `go vet`, `golangci-lint`, `go test -race ./...` must pass before a change is reported as done.
- Commit subjects: one sentence, conventional commits, lowercase. No AI attribution anywhere in git history.
- Plain wording in documents. No em dashes.

## 15. Shared types

`internal/page` defines `State` and `Action`, the observed page. Every package that reads a page uses these types;
JSON field names match `snapshot.js`. `Marker`, `PageKey` and `Guards` are opaque and only compared.

## 16. Resolved questions

| Question | Decision |
|---|---|
| Legacy `TYPESAFE_BASE_URL` | applies to `backends.custom` only; `backends.jev` keeps the hosted URL. Legacy `TYPESAFE_API_KEY` applies to both as `env:TYPESAFE_API_KEY` |
| `keys unset` on a missing entry | success with a note (idempotent) |
| `init` when `browser-tests.yaml` or `.claude/browser-tests.yaml` exists | refuse and name the existing file |
| Listing stored keys | `keys set` and `keys unset` maintain an index of names (names only) in the config directory; `keys list` shows indexed names and referenced names with their state |
| Starter tests file | three real example tests with comments |
| `--config FILE` global flag | accepted |
| Cascade `target_conf` | 0 means the default 0.5; a negative value disables escalation on target confidence |
| Error causes | transport errors stay wrapped for `errors.Is`; message text never contains a key |
| History type | the agent owns its history type and converts it for the text helper |
| `run_id` | local time, as in the Python traces; run directories are UTC |
| Unicode text matching | `golang.org/x/text` (NFKD, case folding), verified against Python 3.13 on 18,945 code points |
| Regular expressions | Go RE2: no lookaround, no backreferences; an invalid pattern fails when the tests file loads |
| Tests file strictness | `verify_args` without `verify` is an error; unknown top-level keys produce a warning, not an error |
| Text helper reasoning switch | `text_helper.reasoning: none` in the config, wired in phase 2 |

## 17. Open questions

1. TypeSafe terms on training models from API output decide whether the local checkpoints may be distributed.
   Until checked, pagevow ships no model and `install --model` takes a path or a private Hugging Face repository.
2. Windows local model serving is not planned; Windows uses `jev` or `custom`.
3. Root cause of `Page.captureScreenshot` timing out on idle pages is unknown; the fallback covers it.
