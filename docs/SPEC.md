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
pagevow install [--browser] [--force] [--json] [--model NAME]
                                             download the browser, and optionally a local model
pagevow use local|jev|custom|cascade [...]   choose the decision backend
pagevow start [--no-browser] [--json]        start what the active backend needs, and the browser
pagevow stop [--json]                        stop everything pagevow started
pagevow status [--json]                      backend, processes, health, versions, GPU memory when known
pagevow doctor [--json]                      check the setup and say how to fix each problem
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

Exit codes of `install`: 0 installed or already installed; 2 nothing was installed, including a missing flag.
Exit codes of `start` and `stop`: 0 everything requested runs or is stopped; 2 otherwise. `status` exits 0 also when
something is down. `doctor` exits 1 when a check fails; warnings do not change its exit code.

`pagevow supervise --spec FILE` is a hidden command that `start` runs in the background; see section 9.

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
  cascade:
    primary: "http://127.0.0.1:8009"
    verifier: "http://127.0.0.1:8010"
    primary_key: ""              # key reference; empty for a local server
    verifier_key: ""
    primary_model: jev-08b-d1a
    primary_mode: default
    verifier_model: jev-4b
    verifier_mode: nf4
    target_conf: 0.5
    veto_cache: true
server: {kev_dir: "~/kev", start_timeout_seconds: 600, gpu_watch: true, gpu_max_temp_c: 87, gpu_min_free_mib: 1500}
text_helper:
  url: ""
  model: ""
  key: keychain:text-helper
  timeout_seconds: 20
  local: {enabled: false, repo: "unsloth/Qwen3-1.7B-GGUF", file: "Qwen3-1.7B-Q4_K_M.gguf", alias: "qwen3-1.7b", gpu_layers: 99, start_timeout_seconds: 900}
browser: {port: 9333, headless: true, viewport: {width: 1480, height: 780}, channel: chrome-for-testing}
run: {retries: 1, timeout_seconds: 120, screenshots: failed, max_steps: 60}
guards: {loop_guard: false, done_min_conf: 0, blocked_min_conf: 0}
```

`browser.channel` is informational: `status` shows it and nothing reads it to choose an executable. The browser that is used
follows the lookup order of section 12.

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
| `local` | starts the model server as a background process, waits for `/v1/models` | Linux + NVIDIA in phase 3, macOS in phase 6 |
| `cascade` | starts each leg whose URL is loopback, largest first, one after the other | as `local` |

Local model serving stays in Python (`kev.serve`). `internal/server` manages it as a child process: it never links to
Python. Process records live in the state directory (`os.UserCacheDir()/pagevow/run/*.json`: pid, port, command, start
time); `stop` only ever stops processes recorded there and verifies the command line before sending a signal.

Supervisor: a model server and a local text helper run as the child of `pagevow supervise`, which `start` launches
in its own session. The supervisor starts the program in the program's own process group, with a parent-death signal
on Linux, writes the record, watches the GPU, and removes the record when the program ends. The browser has no
supervisor; `start` launches it detached with the profile `<cache>/pagevow/profiles/managed`.

Records: one file per process (`model-<port>`, `text-helper-<port>`, `browser-<port>`), written atomically with mode
0600. A record is alive when the pid exists, its start time equals the recorded one, and its command line fits the
kind. Any other record is stale and is removed by `start`, `stop`, `status` and `doctor`.

Stop sequence: SIGTERM to the supervisor, which sends SIGTERM to the program's process group, waits 10 seconds, sends
SIGKILL to the group, removes the record and exits. When the supervisor still runs after 15 seconds, `stop` kills
the group and the supervisor itself. A group is signalled only when it can be tied to the record. Order: browser,
text helper, models.

Modes: `nf4` and `int8` load the model quantised; `nf4`, `int8` and `bf16` turn CUDA graphs off, set the batch size
to 1 and use expandable allocator segments. pagevow sets these variables itself; values in the environment of
`pagevow start` do not change them, so the GPU check and the launched process agree. `default` passes nothing and
keeps CUDA graphs on; it is refused unless the base model named in the run directory is 1B or smaller, read as a
number from the model name. `model` is a directory under `<kev_dir>/runs/` or an
absolute path. pagevow never sets `KEV_API_KEY`: a local server is open and bound to 127.0.0.1.

Logs: `<os.UserCacheDir()>/pagevow/logs/<name>.log`, appended, mode 0600.

GPU safety: before starting a local model, `start` reads free GPU memory (`nvidia-smi`, when present) and refuses when
the model's known peak plus a 1.5 GiB margin does not fit, with the numbers in the message. Known peaks: `nf4` 5.6 GiB,
`int8` 7.2 GiB, `bf16` 10.6 GiB, `default` (0.8B models) 6.4 GiB. The peaks of every model that `start` is about to
launch are summed, the local text helper counts 2.0 GiB when it uses GPU layers, and the margin is added once; when
the sum does not fit, nothing is launched.

GPU guard: while a model runs, its supervisor samples the GPU once per second and stops the model when the
temperature reaches `server.gpu_max_temp_c` or free memory falls to `server.gpu_min_free_mib`. It exits with code 99
and leaves the reason in the log and in `<state dir>/<name>.tripped`, which `status` and `doctor` show. Five failed
samples in a row end the watch and leave the model running.

## 10. Stop hook

`pagevow hook stop` reads the Claude Code hook JSON on stdin and behaves as
`integrations/claude-code/hooks/browser-tests-stop.sh` does. It never writes to stdout and never prompts; on a terminal
it ignores stdin. The hook JSON supplies `session_id` and `cwd`; invalid or empty input is treated as empty.

| Situation | Exit | Output |
|---|---|---|
| `PAGEVOW_HOOK=0` (exactly `0`), or no tests file | 0 | none |
| Project fingerprint equals the last passing one | 0 | none |
| Suite passes | 0 | none; fingerprint stored, block counter removed |
| Suite fails, blocks so far < cap (default 2) | 2 | stderr: failure report, `final.png` paths, `Browser test block N of M.` |
| Suite fails, cap reached | 1 | stderr: tests still fail after N blocked attempts, Claude may stop, counter reset |
| Backend or browser not reachable | 1 | stderr: what is missing and the command to start it |

Environment:

| Variable | Meaning |
|---|---|
| `PAGEVOW_HOOK` | `0` turns the hook off; any other value leaves it on |
| `PAGEVOW_HOOK_MAX_BLOCKS` | how many times in a row one session is blocked, default 2, base 10; `0` never blocks; a non-number or a negative value gives 2 |
| `CLAUDE_PROJECT_DIR` | project directory; else `cwd` of the hook JSON; else the working directory |

State files live in `<project>/.pagevow/` (directory mode 0750, files 0600): `.last-pass` holds the fingerprint of the
last passing state and a newline, `.blocks-<session>` holds the block counter of one session. The session is
`session_id` with every character outside `A-Z a-z 0-9 _ -` removed, `unknown` when nothing is left, so the name cannot
leave the directory. pagevow never edits the project's `.gitignore`.

The hook runs the suite as a subprocess of the same binary, `pagevow run --tests FILE --out <project>/.pagevow --json`
in the project directory (plus `--config PATH` when the flag was given), and reads the report from its stdout. Exit 0
and 1 of that run are a pass and a fail; any other code skips the tests. The hook never starts a local model by itself.

The fingerprint is a SHA-256 over HEAD (`no-head` before the first commit), `git status --porcelain`, the diff and the
staged diff, every untracked file that git does not ignore by content (symlinks by target, unreadable files skipped) and
the tests file, all with `.pagevow/` excluded. Git-ignored files such as build output and local config do not change
the fingerprint, so a change only in them does not trigger the hook. Without git, or outside a work tree, it is the newest modification time of the tests file
and of every file under the project, skipping `.pagevow`, `.git`, `node_modules`, `.venv` and `__pycache__`.

## 11. Claude Code plugin

Embedded in the binary. `pagevow plugin install` renders it into a local marketplace under
`<UserConfigDir>/pagevow/claude-plugin/`:

```
.claude-plugin/marketplace.json      marketplace "pagevow", plugin source ./plugin
plugin/.claude-plugin/plugin.json    name "pagevow", version of the binary
plugin/skills/pagevow/SKILL.md
plugin/hooks/hooks.json              Stop -> '<absolute path of pagevow>' hook stop, timeout 900
plugin/commands/pagevow-run.md
plugin/commands/pagevow-init.md
```

The plugin id is `pagevow@pagevow`. The hook command carries the absolute path of the binary that ran `plugin install`,
quoted for the shell, so the hook works when pagevow is not on the `PATH` of Claude Code. Run `plugin install` again
after you upgrade or move the binary.

| Command | Behaviour |
|---|---|
| `plugin install` | writes the tree (a new tree is swapped in whole, an identical one is left alone), then with `claude` on `PATH` runs `claude plugin marketplace add <root>` and `claude plugin install pagevow@pagevow --scope user`; it updates instead of installing when the plugin is already installed |
| `plugin install --no-register` | writes the files and runs nothing |
| `plugin install` without `claude` | writes the files, prints the two `claude` commands and `claude --plugin-dir <root>/plugin`, exits 0 |
| `plugin uninstall` | with `claude` on `PATH` runs `claude plugin uninstall pagevow@pagevow` and `claude plugin marketplace remove pagevow` (a failure is a warning), then deletes the root; without `claude` it prints those commands |
| `plugin path [--json]` | prints `<root>/plugin`; exit 1 with a note on stderr when the plugin is not installed; `--json` prints `installed`, `path`, `root` and `id` |

`install` refuses a root directory that exists and does not hold the pagevow manifest, and never follows a symbolic
link at the root. `uninstall` deletes the root only when `plugin/.claude-plugin/plugin.json` has the name `pagevow`.

The skill text follows `integrations/claude-code/skills/browser-test/SKILL.md`, with commands changed to `pagevow`.
It keeps these rules: Claude does not start a local model on its own initiative; no secrets in goals; never weaken a
verifier to make a test pass; report remaining failures plainly.

## 12. Browser

- `pagevow install --browser` downloads a pinned Chrome for Testing build for the current OS and architecture into the
  data directory, verifies its checksum, and records the version.
  - The data directory is `<os.UserCacheDir()>/pagevow`. Installs live in `<data>/browser/<version>/` and the record is
    `<data>/browser/installed.json`: `{"version", "platform", "executable", "installed_at"}`, mode 0600, `executable`
    absolute and inside the browser directory.
  - The pinned version is `154.0.8037.92`. Google publishes no checksums, so the pin table in `internal/browser/cft.go`
    holds the size and SHA-256 of each archive, computed by pagevow on 2026-09-30. A different version needs new pins.
    The archives come from `https://storage.googleapis.com/chrome-for-testing-public/<version>/<platform>/chrome-<platform>.zip`.
  - Download goes to `<version>.zip.part` while it is hashed. A wrong size or hash deletes the file and fails with the
    expected and the actual value. The archive is unpacked into `<version>.tmp` and renamed to `<version>` only when it
    is complete and holds the executable; a leftover `.tmp` or `.part` from an earlier try is removed first.
  - Unpacking refuses entries whose path leaves the target, absolute paths, names with a backslash, anything that is not
    a file, a directory or a relative symbolic link that stays inside the tree, entries below a symbolic link, archives
    of more than 50000 entries or 2 GiB, and entries that hold more than they declare. Files keep their execute bit
    (0755, else 0644), directories are 0755.
  - Run again it does nothing while the pinned build is installed; `--force` installs it again. The install refuses
    while a browser record of `pagevow start` is alive: stop it first.

  | Platform | Go | Executable inside the archive |
  |---|---|---|
  | `linux64` | linux/amd64 | `chrome-linux64/chrome` |
  | `linux-arm64` | linux/arm64 | `chrome-linux-arm64/chrome` |
  | `mac-arm64` | darwin/arm64 | `chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing` |
  | `mac-x64` | darwin/amd64 | `chrome-mac-x64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing` |
  | `win64` | windows/amd64 | `chrome-win64/chrome.exe` |
  | `win32` | windows/386 | `chrome-win32/chrome.exe` |

  Any other system, such as windows/arm64, has no build; the command fails and names the platform.
- Lookup order of the browser executable: the installed build of the record when its file exists, then the program
  names on `PATH`, then the standard system locations. A record whose file is gone or points outside the browser
  directory is ignored.
- `start` launches it headless with its own profile directory and a fixed debugging port bound to 127.0.0.1.
- Each test gets a new target in its own window and closes it at the end. A tab inside an existing window gets no
  compositor frames while hidden, so screenshots and wheel scrolling stall for up to 5 seconds, in headless mode too.
- Each target disables the HTTP cache through CDP before its first navigation, so a run always sees the current files
  even though the profile persists.
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
| 3 | `server` (Linux + NVIDIA), `start`, `stop`, `status`, `doctor`, GPU guard | `pagevow use local && pagevow start && pagevow run` works from a clean state |
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
| Screenshot timeouts | cause found: hidden tabs receive no compositor frames. Fixed by one window per session; the 5 second timeout and the step fallback stay as a safety net |
| Browser errors | `ErrTargetRefused` and `ErrStalePage` are separate values; the agent tests for each |
| Navigation failure | opening the start URL fails the test with `ErrNavigation` instead of observing the browser error page |
| Browser port | `Launch` reads the port from `DevToolsActivePort` in its own profile, so it never attaches to another browser |
| Runner and browser lifetime | `run` starts its own headless browser and stops it at the end, unless a browser managed by `pagevow start` is running |
| Request key order | the model prompt is built from the request in JSON order, so every object of the `/v1/systemone` body and of the text helper request keeps the key order of the Python reference; parity tests compare token streams, never sorted maps |
| Trace redaction | secrets are replaced by `***` in trace files only, never in the request sent to a model; values shorter than 12 characters (the placeholder key `local`) are never redacted |
| Agent timeout | the runner always gives the agent a deadline; a page that stays stale spends no budget and would loop otherwise |

### Decisions from the review of pull request 1

| Topic | Decision |
|---|---|
| Secrets | one rule in `internal/secret`: a value is a secret only when it has 12 or more characters; placeholders such as `local` never are. Secrets are removed from the full text before it is cut, in plain, JSON-escaped, HTML-escaped and ASCII-escaped form, longest first. Requests sent to a model are never redacted |
| Reply fields | replies are read by exact key; a duplicate key takes its last value, as in Python |
| Non-JSON reply with status 200 | the decision model client treats it as transient, so the agent asks once more. Python does not retry. Deliberate difference |
| Missing `usage` | the trace records `null`, as Python does |
| Error texts | errors written into traces use the Python sentences; the transport cause stays reachable through `Unwrap` and the CLI prints it |
| URL patterns | translated from Python `re` semantics when the tests file loads: `\d`, `\w` and `\s` cover Unicode as Python's do, `$` also matches before a final newline, `\Z` is the end of the text. `\b` and `\B` are rejected with a message because RE2 cannot evaluate them on Unicode text. Not reproduced: Python's equivalence of dotless and dotted i under case-insensitive matching, and a pattern that consumes the final newline after `$` |
| Empty verifier | a verifier that would run no checks is an error when the tests file loads and names the test id. Stricter than the Python runner |
| YAML tags | explicit core tags (`!!str`, `!!int`, `!!float`, `!!bool`, `!!null`) in `verify_args` are honoured as PyYAML's safe loader honours them; other tags are an error |
| Run directories | names are planned before the run, one per attempt, compared without case and without trailing dots or spaces; clashes get a `-N` suffix; names are limited to 120 bytes with a hash suffix; Windows device names get a `_` prefix; a directory that cannot be created fails only that test |
| Timeout | `--timeout` bounds opening the page as well as the run |
| Headless | `run` uses `browser.headless` from the config; `--headed` overrides it |
| Paid services | decided by destination, not by backend name: a service is paid when its URL is not a literal loopback address or the host `localhost` and its key reference resolves to a value other than the placeholder |
| Loopback | only literal loopback addresses and the exact host `localhost`; `*.localhost` names do not count |
| Cascade `target_conf` | values above 1 are rejected; negative values disable escalation |
| Dialogs | `alert`, `confirm`, `prompt` and `beforeunload` are accepted automatically and recorded; a dialog never counts as a second execution of an action |
| Crashed or destroyed page | every call fails at once with `ErrTargetCrashed` and the run ends with status `error` |
| Call timeout | every browser call has its own timeout (10 s). A timeout after input was dispatched is `ErrOutcomeUnknown`: the action is written to the history as possibly executed and the run ends; the agent never chooses again after it |
| Downloads | denied by default; nothing is written to the user's download directory |
| Tabs opened by the page | recorded and closed with the session; the agent stays on its own tab |
| Iframes | content inside iframes is not observed (same as the Python reference). Visible frames are listed in `State.Frames`, outside the fingerprint and never sent to a model, and the runner warns about them |
| Warnings | dialogs, denied downloads, new tabs and unseen iframes appear as `warnings` in `result.json` and in the report; they never change a verdict or an exit code |
| Orphaned browser | on Linux the browser gets a parent-death signal, so it exits when pagevow is killed |
| Release archives | ship `LICENSE`, `NOTICE` and `README.md`; build date taken from the commit for reproducible builds |
| Result fields | `result.json` gains `warnings` (list of strings, empty when none) and `error_cause` (present only when the underlying error is not part of `error`); `report.json` totals gain `tests_with_warnings` |
| History | an action that ends in `ErrOutcomeUnknown` or `ErrSelectInterrupted` stays in the history with `outcome_unknown: true` |
| Blocked reason | after three refusals: `Target refused 3 times: <label>: ` followed by the browser's own text with no prefix |

### Decisions of phase 3

| Topic | Decision |
|---|---|
| Watchdog | the GPU guard is part of pagevow (the supervisor); no outside watchdog script is needed |
| Mode `default` | allowed for 0.8B models only; with a larger model CUDA graphs need more GPU memory than is safe |
| Preflight | every refusal happens before anything is launched: port in use, GPU memory, missing run directory, mode not allowed, missing program |
| Port in use without a record | `start` refuses and says that pagevow did not start that process |
| Failed start | what the call started for that process is stopped; the last 20 log lines and the log path are printed |
| `run` and the managed browser | `run` attaches to the browser of `pagevow start` when its record is alive and its endpoint answers, and leaves it running |
| Cascade keys | `primary_key` and `verifier_key` are key references; a remote leg counts for the paid notice and is never started |
| Local text helper | `llama-server` under the supervisor, off by default, not part of the GPU memory sum |
| `doctor` before the first `start` | a loopback destination that does not answer is a warning with the fix `pagevow start`; a remote one is a failure |
| Other systems | macOS and Windows refuse a local model with a message that names `jev` and `custom`; the browser still starts |
| Spec files | a spec with an environment entry whose name ends in `_KEY`, `_TOKEN` or `_SECRET` is rejected |
| Agent skills | `.claude/skills/` and `CLAUDE.md` guide coding agents and reviews; they are not part of the binary or of release archives |

### Decisions of phase 4

| Topic | Decision |
|---|---|
| Hook exit codes | 0 Claude may stop, 2 Claude is blocked, 1 the tests were skipped or the cap was reached; the hook prints its own stderr text and the process adds no `pagevow:` line (`ExitError.Silent`) |
| Block cap | `PAGEVOW_HOOK_MAX_BLOCKS`, default 2; `0` never blocks; a value that is not a plain non-negative number gives 2 |
| Disable switch | `PAGEVOW_HOOK=0`, compared as the exact string |
| State files | `.pagevow/.last-pass` and `.pagevow/.blocks-<session>` in the project, modes 0750 and 0600, session names reduced to `A-Z a-z 0-9 _ -` |
| Suite run | a subprocess of the same binary with `run --json`, so the hook and `pagevow run` cannot drift; the hook decodes the report from stdout |
| Fingerprint | git based, without `.pagevow/`; untracked files that git does not ignore are hashed by content, ignored files do not count; modification times when git is missing or the directory is not a work tree |
| `.gitignore` | pagevow never touches the project's `.gitignore` |
| Plugin root | `<UserConfigDir>/pagevow/claude-plugin/` with a marketplace file and the plugin under `plugin/` |
| Hook command | the absolute path of the running binary, shell quoted, followed by `hook stop`; install again after an upgrade |
| Registration | through the `claude` CLI only; with no `claude` or with `--no-register` the commands are printed and the exit code is 0 |
| Uninstall | unregisters first, failures are warnings, then removes the root only when it holds the pagevow manifest |
| Stdin | read with a 1 MiB limit, ignored on a terminal, never prompts |

### Decisions of phase 5, part 1

| Topic | Decision |
|---|---|
| Data directory | `<os.UserCacheDir()>/pagevow`; the browser installs and their record live under `browser/` |
| Pin | version `154.0.8037.92`, one size and SHA-256 per platform in the code; the version is never taken from the network |
| Install record | `installed.json` next to the versions, written atomically with mode 0600 |
| Already installed | `install --browser` reports it and downloads nothing; `--force` replaces the tree |
| Running browser | the install refuses, before any download, while a browser record of `pagevow start` is alive; the hint is `pagevow stop` |
| Old versions | a new pinned version installs next to the old one; nothing deletes old trees |
| Output | `[info] downloading ...` line, a progress line every 10 percent only on a terminal, then `[ok] installed ... at <path>`; `--json` prints `version`, `platform`, `executable`, `already_installed` and nothing else on stdout |
| `--model` | the flag exists and fails with `not implemented yet` and exit code 2 |
| `doctor` | `browser:installed` reports the recorded build, warns when it differs from the pin or cannot be used, and warns when there is neither a record nor a system browser; the missing browser fix names `pagevow install --browser` |
| `status` | the Browser section shows `installed` with the version and path, or `no (pagevow install --browser)` |
| HTTP client | 15 minute overall timeout, at most 3 redirects; the address of the archives can be replaced by the container for tests only |

## 17. Open questions

1. TypeSafe terms on training models from API output decide whether the local checkpoints may be distributed.
   Until checked, pagevow ships no model and `install --model` takes a path or a private Hugging Face repository.
2. Windows local model serving is not planned; Windows uses `jev` or `custom`.
3. Cascade on local models has not run through `pagevow start` on a real GPU; only backend `local` with `nf4` has.
4. Windows and macOS code paths compile but have not run on those systems.
