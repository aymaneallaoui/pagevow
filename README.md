<p align="center"><img src="assets/cover.gif" alt="pagevow running a suite in a terminal" width="100%"></p>

<h1 align="center">pagevow</h1>

<p align="center">
  <img src="https://img.shields.io/badge/go-1.27-00ADD8" alt="Go 1.27">
  <img src="https://img.shields.io/badge/licence-MIT-informational" alt="Licence MIT">
</p>

pagevow runs browser tests written as goals. You write what a person would do on the page and what the page should
show at the end. A decision model drives a real Chromium step by step, and it can only pick one of the elements pagevow
observed on the page and one of the operations pagevow supports: it never writes selectors or code. A test passes only
when the run ended `DONE` and an independent verifier, which reads the final page itself, returned true. Every test
leaves screenshots and a step trace as evidence. The active backend is always shown, so a paid API is never used by
surprise. One Go binary does all of it, and a Claude Code plugin runs the suite when Claude finishes a coding task.

[Quick start](#quick-start) · [Install](#install) · [How it works](#how-it-works) · [Tests file](#tests-file) ·
[Commands](#commands) · [Backends](#backends) · [Local servers](#local-servers) · [Configuration](#configuration) ·
[Claude Code](#claude-code-plugin-and-stop-hook) · [Platforms](#platforms) · [Development](#development)

## Quick start

```sh
pagevow install --browser                       # pinned Chrome for Testing, checked by size and SHA-256
pagevow use custom --url http://127.0.0.1:8080  # or: use jev, use local, use cascade (see Backends)
pagevow init                                    # writes a starter pagevow.yaml
pagevow run
```

A tests file is a YAML list. This is the shape of the starter that `pagevow init` writes:

```yaml
- id: home-loads
  url: http://localhost:3000/
  goal: "Open the home page. Stop when the page has finished loading."
  verify: page
  verify_args:
    url: /$
    text: ["Welcome"]

- id: contact-form
  url: http://localhost:3000/contact.html
  goal: "On the contact page, enter name Ada Lovelace and email ada@example.com. Stop when both values are in the form. Do not send."
  tags: [forms]
  verify: page
  verify_args:
    url: /contact\.html
    fields: {Name: Ada Lovelace, Email: ada@example.com}
```

`pagevow run` prints one line per test, the totals, and a block for each test that did not pass. When the output is
piped it looks like this (on a terminal each line also gets a mark):

```text
PASS       home-loads  steps=<n>  <seconds>s
FAIL       contact-form  steps=<n>  <seconds>s  attempt 2

1/2 passed. Report: .pagevow/<UTC timestamp>/report.json

FAIL contact-form
  goal: On the contact page, enter name Ada Lovelace and email ada@example.com. ...
  final url: http://localhost:3000/contact.html
  failed checks:
    - <check that failed>
  final.png: .pagevow/<UTC timestamp>/contact-form.retry1/final.png
  directory: .pagevow/<UTC timestamp>/contact-form.retry1
```

`pagevow status` shows the active backend and whether a paid service would be called. `pagevow doctor` checks the
setup and prints a fix for each problem.

## Install

### Release binaries

The latest release is `v0.2.0`. Each release has these assets, built by goreleaser:

| Asset | System |
|---|---|
| `pagevow_<version>_linux_amd64.tar.gz` | Linux, x86-64 |
| `pagevow_<version>_linux_arm64.tar.gz` | Linux, ARM64 |
| `pagevow_<version>_darwin_amd64.tar.gz` | macOS, Intel |
| `pagevow_<version>_darwin_arm64.tar.gz` | macOS, Apple Silicon |
| `pagevow_<version>_windows_amd64.zip` | Windows, x86-64 |
| `checksums.txt` | SHA-256 of every archive |

`<version>` is the tag without its `v`, for example `0.2.0`. Each archive holds the binary, `LICENSE`, `NOTICE` and
`README.md`. Check the archive against `checksums.txt`, unpack it and put `pagevow` on your `PATH`.

The repository `aymaneallaoui/pagevow` is private for now. Downloading a release, `go install` and `pagevow update`
all need an account or a token that can read it. With the GitHub CLI:

```sh
gh release download v0.2.0 --repo aymaneallaoui/pagevow --pattern 'pagevow_0.2.0_linux_amd64.tar.gz' --pattern checksums.txt
sha256sum --check --ignore-missing checksums.txt
tar -xzf pagevow_0.2.0_linux_amd64.tar.gz pagevow
```

### From source

Go 1.27 or newer is required. While the repository is private, `go install` needs `GOPRIVATE` and git credentials
that can read it.

```sh
GOPRIVATE=github.com/aymaneallaoui/pagevow go install github.com/aymaneallaoui/pagevow/cmd/pagevow@latest
```

Or in a checkout:

```sh
make build      # writes bin/pagevow
make install    # installs into GOBIN
```

### The browser

```sh
pagevow install --browser          # downloads Chrome for Testing 154.0.8037.92
pagevow install --browser --force  # installs it again
pagevow install --browser --json   # prints version, platform, executable and already_installed
```

The build goes to `<user cache directory>/pagevow/browser/<version>/`, and the install is recorded in
`<user cache directory>/pagevow/browser/installed.json`. Google publishes no checksums, so pagevow checks the size and
the SHA-256 of the archive against values pinned in the code. `run`, `start` and `doctor` use the installed build before
any Chromium or Chrome on `PATH`. The command refuses while the browser of `pagevow start` runs: run `pagevow stop`
first.

### A local model

pagevow ships no model. `install --model` puts your own run directory where the local backend finds it,
`<server.kev_dir>/runs/<name>`:

```sh
pagevow install --model ~/train/out/jev-4b                # copies a directory
pagevow install --model /path/to/run --name mine --link   # links it instead; the record stays in the runs directory
pagevow install --model owner/name@main                   # downloads a Hugging Face repository at that commit
pagevow use local --model jev-4b --mode nf4               # --mode bf16 on macOS
```

A run directory holds `adapter_config.json` (with `base_model_name_or_path`), `adapter_model.safetensors` and
`head.pt`. kev fetches the base model on first use. `head.pt` is a PyTorch file that can run code when it is loaded:
install models only from sources you trust.

<details>
<summary>Model install rules</summary>

- A Hugging Face download is checked file by file: large files against the SHA-256 the Hub lists, the others against
  their git blob id, every file against its size. It fails when no data arrives for 2 minutes; there is no overall
  time limit.
- A private repository needs `HF_TOKEN`, `HUGGING_FACE_HUB_TOKEN` or `pagevow keys set huggingface`. The token is only
  sent to the Hub and is never printed.
- A copy follows a symbolic link only to a file inside the source directory, so a Hugging Face cache snapshot needs
  `--link`, or a download with `--local-dir`.
- Keep the source outside `<kev_dir>/runs`: a source that is, or sits inside, the target is refused, and `--link`
  refuses any source inside the runs directory. On Windows `--link` needs Developer Mode or the symlink privilege.
- An existing name is replaced only with `--force`, only when pagevow installed it, and never while a model server
  runs it. Installing the same unchanged source again does nothing; a copy whose source changed needs `--force`.
- With `--browser` and `--model`, the model is installed first.
- Exit codes: 0 installed or already installed; 2 nothing was installed, or with both flags the model was installed and
  the browser was not.

</details>

### Update

```sh
pagevow update            # replaces the binary with the latest GitHub release
pagevow update --check    # prints both versions and downloads nothing
pagevow update --force    # installs the latest release again
pagevow update --json     # prints current, latest, update_available, updated and executable
```

Because the repository is private, `update` needs a token that can read it: `GITHUB_TOKEN`, else `GH_TOKEN`, else the
keychain entry `github` (`pagevow keys set github`). The token is sent to `api.github.com` only, is dropped on a
redirect to another host or port, and is never printed. Run `pagevow plugin install` again after an update: the Stop
hook stores the path of the binary.

<details>
<summary>How update replaces the binary</summary>

- It downloads `pagevow_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows) and `checksums.txt`, checks the SHA-256 of
  the archive, and renames the new binary over the running one.
- `checksums.txt` is not signed, so it catches a damaged download, not a tampered release.
- On Windows the running `pagevow.exe` is renamed to `pagevow.exe.old` first. That file is removed the next time
  `update` runs; when a process still runs from it, the next update uses a `pagevow.exe.old-<random>` name instead.
- When the directory of the binary is read-only, or the final rename fails, the verified binary is kept in
  `<user cache directory>/pagevow/update/` and the message says where.
- A build that is not a release, such as `dev` or a `git describe` build like `v1.2.3-5-gabc1234`, counts as older
  than every release.
- Exit codes: 0 updated or already current; 1 with `--check` when a newer release exists; 2 nothing was replaced.

</details>

## How it works

<p align="center"><img src="assets/architecture.png" alt="Diagram of one pagevow test run: the tests file feeds the runner, the agent loop decides through the backend and acts on Chrome for Testing over CDP, the verifier checks the final page, and the verdict and evidence go to the .pagevow run directory" width="100%"></p>

One run of one test:

1. **Tests file.** `pagevow run` reads `pagevow.yaml` and the user config. Each test has a start URL, a goal and a
   verifier.
2. **Runner.** For each test it opens a fresh window in Chrome for Testing, headless by default, and gives the agent a
   time limit and a step limit.
3. **Agent loop.** The agent observes the page and lists the elements it can act on. It sends the goal and those
   elements to the decision backend with `POST /v1/systemone`. The answer names one observed element and one
   operation: `CLICK`, `TYPE_TEXT`, `SELECT` or `PRESS_ENTER`, or ends the run with `DONE` or `BLOCKED`. The agent
   acts over CDP and observes again. A browser mutation is never retried; a retry of a test is a new run from the start
   URL.
4. **Verifier.** When the run ends, the verifier named by the test checks the final page: URL, visible text, field
   values, checked boxes. It does not ask the model.
5. **Verdict.** `PASS` only when the run ended `DONE` and the verifier returned true. A test without a verifier is
   `UNVERIFIED` and counts as a failure.
6. **Evidence.** Screenshots, `result.json`, the step trace and `report.json` are written under
   `.pagevow/<run>/`. Secrets are removed from traces. The decision model never receives a screenshot.

The diagram source is [`assets/architecture.excalidraw`](assets/architecture.excalidraw); open it in Excalidraw to
edit it.

## Tests file

`pagevow run` reads the first of `pagevow.yaml`, `browser-tests.yaml` and `.claude/browser-tests.yaml` in the current
directory, or the file given with `--tests`. The file is a YAML list of tests:

| Key | Required | Meaning |
|---|---|---|
| `id` | yes | unique name of the test, used for its output directory |
| `url` | yes | start URL |
| `goal` | yes | what to do and when to stop, in plain words |
| `tags` | no | list of text values, kept in the report |
| `verify` | no | verifier name: `page`, `echo`, `hn_story` or `flights` |
| `verify_args` | with `verify` | arguments of the verifier |
| `repeat` | no | a positive integer; accepted for compatibility with the Python runner and not used |

An unknown key is a warning, not an error. `verify_args` without `verify` is an error. Every problem in the file is
reported at once, and an invalid file stops the run before any test with exit code 2.

Goals and verifier values may use date placeholders: `{date+N}` becomes the day N days from today (for example
`October 9, 2026`), `{date+N:FORMAT}` formats it with `strftime` directives, and `{weekday+N}` gives the weekday name.
Any other `{...}` in a goal is an error.

Never put a password or another secret in a goal: goals are written to traces.

<details>
<summary>Verifiers and their arguments</summary>

| Verifier | Arguments | Passes when |
|---|---|---|
| `page` | `url`, `text`, `fields`, `values`, `checked` | every listed check holds on the final page |
| `echo` | `url`, `values` | the page is at `url` and shows every value (a form that echoes its submission) |
| `hn_story` | `rank`, `comments` | ported from the Python runner for Hacker News tasks |
| `flights` | `origin`, `destination`, `date`, `return_date`, `one_way`, `adults` | ported from the Python runner for Google Flights tasks |

Arguments of `page`:

- `url`: a pattern or a list of patterns, matched case-insensitively against the decoded final URL.
- `text`: a list of strings that must be visible on the page.
- `fields`: label to expected value. A boolean expects a checkbox or radio state.
- `values`: a list of values that some control on the page must hold.
- `checked`: label to the number of boxes with that label that must be checked.

Rules that differ from a plain Go or Python reading:

- URL patterns behave as in the Python runner, not as raw RE2. `\d`, `\w` and `\s` cover Unicode text, `$` also
  matches before a final newline, `\Z` is the end of the text, and `x{,3}` repeats up to 3 times. `\b` and `\B` are
  rejected with a message, because RE2 cannot evaluate them on Unicode text; write the boundary out, for example
  `(^|[^\p{L}\p{N}_])word($|[^\p{L}\p{N}_])`. Lookaround, backreferences and the `a`, `L` and `x` flags are rejected
  the same way. An invalid pattern fails when the file loads.
- Explicit core YAML tags (`!!str`, `!!int`, `!!float`, `!!bool`, `!!null`) in `verify_args` are honoured:
  `fields: {Subscribe: !!str yes}` expects the text `yes`, while a plain `yes` is the boolean true. Other tags are an
  error.
- A verifier that would run no checks (`verify_args: {}`, `url: []`, empty lists everywhere, or `verify: page` with no
  arguments) is rejected when the file loads, with the test id in the message. The Python runner would pass such a
  test on any page.

</details>

### Running tests

Before any test, `run` checks that the decision backend answers, that the text helper answers when it is a loopback
URL, and that a browser is available. A problem is printed with its fix and the exit code is 2. A model server for the
`local` and `cascade` backends must already run: start it with `pagevow start`.

When `pagevow start` keeps a browser running and it answers, `run` attaches to it and leaves it running. Otherwise
`run` starts its own browser with a temporary profile and stops it at the end, also after an error or Ctrl+C. The first
Ctrl+C stops the current test, writes the report for what ran and exits with code 1; a second one kills the browser and
exits at once.

A failed test runs again from a fresh session up to `--retries` times (default 1). An `UNVERIFIED` test is never
retried. `--timeout` covers opening the page as well as the steps. `--screenshots final` keeps only `final.png`,
`failed` (the default) also keeps the step screenshots of failing attempts, `all` keeps every one. Nothing is ever
deleted.

While a test runs, pagevow notes what the page did that the agent did not choose or cannot see: a JavaScript dialog it
accepted, a download it denied (pagevow never saves downloads), a new tab the page opened, and an iframe from another
origin or one that holds controls. Each note is a line in `warnings` and is printed under the test line. Warnings never
change a verdict or an exit code.

| Exit code of `run` | Meaning |
|---|---|
| 0 | every test passed |
| 1 | a test failed or is unverified, or the run was interrupted |
| 2 | the run could not start: wrong flag or argument, invalid tests file or config, backend or browser not reachable, or an interrupt before the first test |

`--json` prints only the report on stdout; notes and warnings go to stderr.

<details>
<summary>Run output layout</summary>

```text
<out>/<UTC YYYYmmddTHHMMSS>/
  report.json
  <test id>/               one directory per attempt; retries use <test id>.retry1/, .retry2/, ...
    result.json
    final.png
    step-0000.png ...
    <run id>.jsonl         step trace
    <run id>.meta.json
```

`<out>` is `--out`, else `.pagevow` next to the tests file. `<run id>` is the local start time plus a random suffix.

- `report.json` has `run_dir`, `tests_file`, `passed`, `totals` (`tests`, `passed`, `failed`, `unverified`,
  `missing_screenshots`, `tests_with_warnings`), `tests` (each with `id`, `outcome`, `passed` and `attempts`) and
  `interrupted` when the run was stopped early.
- `result.json` has `id`, `url`, `goal`, `status` (`DONE`, `BLOCKED`, `max_steps`, `timeout` or `error`), `verified`,
  `passed`, `outcome` (`PASS`, `FAIL` or `UNVERIFIED`), `steps`, `elapsed_ms`, `final_url`, `final_title`,
  `failed_checks`, `error`, `error_cause` (only when it adds to `error`), `reason`, `attempt`, `screenshots`, `trace`,
  `directory`, `screenshot_errors`, `screenshots_attempted`, `warnings` and `final_from_step`. The field names follow
  the Python runner, so its tools keep working.
- Directory names are planned before the first test runs. Characters other than letters, digits, dot, underscore and
  hyphen become `_`, names are cut to 120 bytes with a short hash, Windows device names such as `CON` get a `_`
  prefix, and two ids that would share a directory get a `-2`, `-3` suffix. A directory that cannot be created fails
  only that test.
- A screenshot has a 5 second timeout and a failed one never changes a verdict. When `final.png` cannot be captured
  after a run that ended `DONE` or `BLOCKED`, it is recovered from the last step screenshot.

</details>

## Commands

| Command | What it does |
|---|---|
| `pagevow install [--browser] [--model SOURCE] [--name N] [--link] [--force] [--json]` | download the pinned browser; copy, link or download a model run directory |
| `pagevow use local\|jev\|custom\|cascade [flags]` | choose the decision backend and write it into the config file |
| `pagevow start [--no-browser] [--json]` | start what the active backend needs, the local text helper when enabled, and a browser |
| `pagevow stop [--json]` | stop everything `start` started |
| `pagevow status [--json]` | active backend, paid service notice, URLs, browser, processes, health, GPU or memory, versions |
| `pagevow doctor [--json]` | check the setup and say how to fix each problem |
| `pagevow init [DIR]` | write a starter `pagevow.yaml`; refuses when a tests file already exists |
| `pagevow run [--tests FILE] [--ids a,b] [--out DIR] [--screenshots final\|failed\|all] [--retries N] [--timeout SECONDS] [--full-page] [--headed] [--json]` | run the tests |
| `pagevow hook stop` | the Claude Code Stop hook: reads the hook JSON on stdin |
| `pagevow plugin install [--no-register]`, `plugin uninstall`, `plugin path [--json]` | manage the Claude Code plugin |
| `pagevow keys set NAME`, `keys unset NAME`, `keys list` | keychain entries; values are read from stdin or a hidden prompt and never printed |
| `pagevow update [--check] [--force] [--json]` | replace the binary with the latest release |
| `pagevow version` | print the version |

Every command accepts `--config FILE`. Without a terminal no command prompts, and output is plain text.

`use` flags by backend:

| Backend | Flags |
|---|---|
| `local` | `--url`, `--model`, `--mode` |
| `jev` | `--url`, `--key` |
| `custom` | `--url`, `--key` |
| `cascade` | `--primary`, `--verifier`, `--primary-model`, `--primary-mode`, `--verifier-model`, `--verifier-mode`, `--primary-key`, `--verifier-key`, `--target-conf`, `--veto-cache` |

| Exit codes | 0 | 1 | 2 |
|---|---|---|---|
| `run` | all passed | a test failed or is unverified | the run could not start |
| `install` | installed or already installed | | nothing installed (see Install) |
| `update` | updated or current | `--check` found a newer release | nothing replaced |
| `start` | everything requested runs | | something did not start |
| `stop` | everything stopped, or nothing to stop | | a process could not be stopped |
| `status` | always, also when something is down | | |
| `doctor` | no check failed (warnings allowed) | a check failed | |
| `plugin path` | installed | not installed | |
| `hook stop` | Claude may stop | tests skipped, or block limit reached | Claude is blocked |

Every command also exits 2 on a wrong flag, a wrong flag value or an unexpected argument, so 1 is never a usage error.

## Backends

Every backend speaks `POST <url>/v1/systemone` with a bearer key. The body keeps the key order of the reference
implementation, because the decision model answers differently when the order changes.

| Backend | Where the model runs | What `start` does |
|---|---|---|
| `local` | a kev model server on this machine | starts it under a supervisor and waits for `/v1/models` |
| `jev` | the hosted API at `https://api.typesafe.ai`, paid | nothing |
| `custom` | any URL you give | nothing |
| `cascade` | a primary model checked by a verifier model | starts each leg whose URL is a loopback address, largest first |

The default backend is `local`. In `cascade`, the verifier model is asked when the primary model answers `DONE` or
`BLOCKED`, and when its confidence in the chosen target is below `target_conf` (0 means the default 0.5, a negative
value never asks for that reason). With `veto_cache` a verifier override of `DONE` or `BLOCKED` is reused on the same
page.

```sh
pagevow use local --model jev-4b --mode nf4
pagevow use jev                      # key reference keychain:typesafe by default
pagevow use custom --url http://127.0.0.1:8080
pagevow use cascade --primary-model jev-08b-d1a --primary-mode default --verifier-model jev-4b --verifier-mode nf4
pagevow use cascade --primary https://gpu.example.test --primary-key env:GPU_KEY
```

A remote cascade leg is never started. Its key reference (`primary_key`, `verifier_key`) is resolved like the key of
`jev` and `custom`.

**Text helper.** `TYPE_TEXT` steps need a value for the field. The optional text helper, any OpenAI-compatible chat
server set in `text_helper.url`, writes that value from the goal. Without it a `TYPE_TEXT` decision fails; pagevow never
guesses or hardcodes field values. `text_helper.reasoning: none` switches the helper's reasoning off. pagevow can start
a local helper with `llama-server` when `text_helper.local.enabled` is true and `text_helper.url` is a loopback URL.

**Paid service notice.** `status` prints `paid_api`, and `run` prints a notice, when a service would be called that is
not on this machine and has a real key: the active backend or the text helper has a URL that is not loopback and a key
reference that resolves to a value other than the placeholder `local`. Only literal loopback addresses (`127.0.0.0/8`,
`::1`) and the exact host `localhost` count as this machine; `api.localhost` does not.

## Local servers

`pagevow start` starts what the active backend needs, then a browser:

| Process | Program | Started when |
|---|---|---|
| `model-<port>` | `uv run --extra serve python -m kev.serve --run <run> --port <port>` in `server.kev_dir` | backend `local`, or a loopback leg of `cascade` |
| `text-helper-<port>` | `llama-server -hfr <repo> -hff <file> --alias <alias> --host 127.0.0.1 --port <port> ...` | `text_helper.local.enabled` is true and `text_helper.url` is loopback |
| `browser-<port>` | the installed Chrome for Testing, else your Chromium or Chrome | always, unless `--no-browser` |

The order is models (largest first, each one waited for), the text helper, the browser. Every refusal happens before
anything is launched. A process that already runs and answers is reported as `already running`; a port that answers
without a pagevow record is refused. A process that does not become ready is stopped again and the last 20 lines of
its log are printed. pagevow never sets `KEV_API_KEY`: a local server is open and bound to 127.0.0.1.

`pagevow stop` stops the browser, the text helper and the models, in that order. It only signals a process whose
recorded identity (pid, start time, command line) still matches, and a process group only when it can be tied to the
record. A model server gets SIGTERM and 10 seconds, then SIGKILL for its process group. The managed browser profile
(`<user cache directory>/pagevow/profiles/managed`) stays on disk.

<details>
<summary>Modes and the GPU memory check</summary>

`backends.local.mode` and the cascade mode fields take `nf4`, `int8`, `bf16` or `default`.

- `nf4` and `int8` load the model quantised. `nf4`, `int8` and `bf16` set `KEV_CUDA_GRAPHS=0`, `KEV_MAX_BATCH=1` and
  `PYTORCH_CUDA_ALLOC_CONF=expandable_segments:True`, and always set `KEV_LOAD_IN_4BIT` and `KEV_LOAD_IN_8BIT`, whatever
  the environment of `pagevow start` holds, so the memory check matches what runs.
- `default` adds nothing and keeps CUDA graphs on. It is refused for every model whose base model is larger than 1B,
  read from `adapter_config.json` in the run directory.
- `model` is a run name under `<kev_dir>/runs/` or an absolute path.

Known peaks on Linux: `nf4` 5.6 GiB, `int8` 7.2 GiB, `bf16` 10.6 GiB, `default` 6.4 GiB. The local text helper counts
2.0 GiB when `text_helper.local.gpu_layers` is above 0. Before it launches anything, `start` adds up the peaks of what it
is about to start, adds a margin of 1.5 GiB and compares the sum with the free GPU memory from `nvidia-smi`. When it
does not fit, `start` refuses and prints the numbers. Without `nvidia-smi` it warns and continues.

</details>

<details>
<summary>macOS with Apple Silicon</summary>

The model server runs through MLX. Install `uv`, clone kev, point `server.kev_dir` at the clone, then:

```sh
pagevow use local --model jev-4b --mode bf16
pagevow install --browser
pagevow start
```

pagevow sets `KEV_BACKEND=mlx`, and `/v1/models` reports `backend: mlx`. MLX serves bf16, so modes `nf4` and `int8`
are refused by `use`, `start` and `doctor`; `bf16` and `default` (1B or less) are accepted.

Free memory is the unified memory read through `sysctl`: the free, purgeable and file-backed pages. When `sysctl`
cannot be read, `start` refuses to start the model. The peaks are estimates until they are measured on a real Mac:
11.5 GiB for a model above 1B (or of unknown size) and 3.0 GiB for 1B or less, plus the text helper and the 1.5 GiB
margin. A model above 1B also needs 16 GiB of memory in total. The temperature is not read, and the
`server.gpu_min_free_mib` limit is not applied to unified memory until it is measured.

</details>

<details>
<summary>The GPU guard</summary>

Every model server and the local text helper runs under a small supervisor process (`pagevow supervise`, hidden from
help). When `server.gpu_watch` is true and `nvidia-smi` is present, the supervisor samples the GPU once per second. It
stops its process when the temperature reaches `server.gpu_max_temp_c` (default 87) or free memory falls to
`server.gpu_min_free_mib` (default 1500). It writes `guard: stopped <name>: <reason> (temp N C, free N MiB)` to the log
and to `<user cache directory>/pagevow/run/<name>.tripped`, and exits with code 99. `status` and `doctor` show that
message until the next `pagevow start` of the same process. Five failed samples in a row end the watch and leave the
process running.

</details>

<details>
<summary>Logs, records and orphaned processes</summary>

- Logs are appended to `<user cache directory>/pagevow/logs/<name>.log` (mode 0600).
- Records are one JSON file per process in `<user cache directory>/pagevow/run/<name>.json` (directory 0700, files
  0600, written atomically). A record holds names, pids, ports, the command, times and paths, never a key.
- A record is alive while its pid exists, its start time is the recorded one and its command line still fits.
- A model server or text helper record whose supervisor is gone while its program still runs is orphaned. This can
  happen on macOS, which has no parent-death signal, when the supervisor is killed. `status` and `doctor` show it with
  the fix `pagevow stop`, `start` refuses to reuse its port, and `stop` sends the signals to the program's process
  group itself and prints `<name> stopped (supervisor was gone)`.
- Any other record is stale, and `start`, `stop`, `status` and `doctor` remove it and say so.
- On Windows there is no supervised local server, and `stop` ends the browser's process tree.

</details>

## Configuration

The config file is `pagevow/config.yaml` in the user config directory, or the file given with `--config`. Environment
variables override it, and command line flags override both. Every key has a variable: `PAGEVOW_` plus the key in
capitals with dots as underscores, for example `PAGEVOW_BROWSER_HEADLESS=false`. A `pagevow.yaml` in a project is
always a tests file, never config.

Key fields hold references, never secrets: `keychain:NAME` or `env:NAME`. A literal value is rejected with the fix. A
backend or text helper URL with a user name, a password or a query string is rejected too, and a real key is never sent
over plain `http` to a host that is not loopback.

```sh
pagevow keys set typesafe < key.txt    # value from stdin, or a hidden prompt on a terminal
pagevow keys list                      # each name as stored, env or missing, cascade keys included
pagevow keys unset typesafe
```

`keys set` and `keys unset` keep an index of names (names only) in `keys.json` next to `config.yaml`.

<details>
<summary>Config schema and defaults</summary>

```yaml
backend: local                 # local | jev | custom | cascade
backends:
  local:   {url: "http://127.0.0.1:8009", model: jev-4b, mode: nf4}
  jev:     {url: "https://api.typesafe.ai", key: keychain:typesafe}
  custom:  {url: "", key: ""}
  cascade:
    primary: "http://127.0.0.1:8009"
    verifier: "http://127.0.0.1:8010"
    primary_key: ""            # key reference; empty for a local server
    verifier_key: ""
    primary_model: jev-08b-d1a
    primary_mode: default
    verifier_model: jev-4b
    verifier_mode: nf4
    target_conf: 0.5           # at most 1; 0 means 0.5, negative never asks on confidence
    veto_cache: true
server:
  kev_dir: "~/kev"
  start_timeout_seconds: 600   # 1 to 3600
  gpu_watch: true
  gpu_max_temp_c: 87           # 40 to 100
  gpu_min_free_mib: 1500       # 0 to 65536
text_helper:
  url: ""
  model: ""
  key: keychain:text-helper
  timeout_seconds: 20          # 1 or more
  reasoning: ""                # empty or none
  local:
    enabled: false
    repo: "unsloth/Qwen3-1.7B-GGUF"
    file: "Qwen3-1.7B-Q4_K_M.gguf"
    alias: "qwen3-1.7b"
    gpu_layers: 99             # 0 to 999
    start_timeout_seconds: 900 # 1 to 3600
browser:
  port: 9333
  headless: true
  viewport: {width: 1480, height: 780}
  channel: chrome-for-testing  # shown by status; does not choose the executable
run:
  retries: 1                   # 0 or more
  timeout_seconds: 120         # per attempt
  screenshots: failed          # final | failed | all
  max_steps: 60
guards:
  loop_guard: false
  done_min_conf: 0
  blocked_min_conf: 0
```

The environment variables of the Python agent are read as a fallback when neither the `PAGEVOW_*` variable nor the
config entry is set:

| Variable | Config key |
|---|---|
| `TYPESAFE_BASE_URL` | `backends.custom.url` (`jev` keeps `https://api.typesafe.ai`) |
| `TYPESAFE_API_KEY` | `backends.jev.key` and `backends.custom.key`, as `env:TYPESAFE_API_KEY` |
| `JEV_VERIFIER_BASE_URL` | `backends.cascade.verifier` |
| `TEXT_MODEL_BASE_URL` | `text_helper.url` |
| `TEXT_MODEL` | `text_helper.model` |
| `TEXT_MODEL_API_KEY` | `text_helper.key`, as an `env:` reference |
| `TEXT_MODEL_REASONING` | `text_helper.reasoning` |
| `TEXT_TIMEOUT_S` | `text_helper.timeout_seconds` |

</details>

## Claude Code plugin and Stop hook

```sh
pagevow plugin install      # writes the plugin and registers it with Claude Code
pagevow plugin path         # where it is; pass it to claude --plugin-dir
pagevow plugin uninstall
```

`plugin install` writes a local marketplace to `<user config directory>/pagevow/claude-plugin/` and runs
`claude plugin marketplace add` and `claude plugin install pagevow@pagevow --scope user`. Without `claude` on `PATH`, or
with `--no-register`, it writes the files and prints the commands to run yourself. The plugin holds a skill, the slash
commands `/pagevow-run` and `/pagevow-init`, and a Stop hook that runs `pagevow hook stop` by the absolute path of the
binary, with a 900 second timeout. Run `pagevow plugin install` again after you upgrade or move pagevow. On Windows it
refuses a binary path that contains `$`, a backtick or `%`, because a double quoted command would expand them.

When Claude tries to stop, the hook runs the project's tests file and blocks Claude while the tests fail, so Claude
reads the `final.png` files and fixes the app or the test. The hook never starts a local model and never writes to
stdout.

| Situation | Exit | Output on stderr |
|---|---|---|
| `PAGEVOW_HOOK=0`, or no tests file | 0 | none |
| project unchanged since the last pass | 0 | none |
| suite passes | 0 | none |
| suite fails, fewer blocks so far than the limit | 2 | failure report, `final.png` paths, `Browser test block N of M.` |
| suite fails, limit reached | 1 | the tests still fail and Claude may stop; the counter is reset |
| backend or browser not reachable, or the run could not start | 1 | what is missing and the command to fix it |

| Variable | Meaning |
|---|---|
| `PAGEVOW_HOOK` | `0` turns the hook off; any other value leaves it on |
| `PAGEVOW_HOOK_MAX_BLOCKS` | blocks in a row per session, default 2; `0` never blocks; an invalid value gives 2 |
| `CLAUDE_PROJECT_DIR` | project directory; else `cwd` of the hook JSON; else the working directory |

"Unchanged" is a fingerprint over the git state of the project (HEAD, status, diffs, untracked files that git does
not ignore, and the tests file), without `.pagevow/`. Files that git ignores do not count. Without git it is the newest
modification time under the project. State lives in `<project>/.pagevow/`: `.last-pass` and `.blocks-<session>`.
pagevow never edits `.gitignore`.

## Platforms

| | Linux amd64, arm64 | macOS arm64 | macOS amd64 | Windows amd64 |
|---|---|---|---|---|
| Release binary | yes | yes | yes | yes |
| Pinned browser | yes | yes | yes | yes |
| Local model (`local`, `cascade`) | NVIDIA GPU | MLX | no | no |
| Local text helper | yes | yes | yes | no |
| `jev` and `custom` | yes | yes | yes | yes |

The pinned browser is Chrome for Testing `154.0.8037.92`. It also has a Windows 386 build, which a binary built from
source can use; Windows on arm64 has no build, so there is no release for it. Systems without local model serving use
`jev` or `custom`; `start` says so and still starts the browser.

## Development

```sh
make check             # gofmt check, go vet, golangci-lint, go test -race
make release-snapshot  # builds every release archive into dist/ without publishing, needs goreleaser
```

Install the linter with `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0`. Tests are
offline: no network, no real browser, no model, no paid API and no real keychain.

| Workflow | Runs |
|---|---|
| `ci.yml` | on every change: `go mod tidy` check, gofmt, vet, golangci-lint, `go test -race` on Linux, cross-builds for macOS amd64 and Windows 386, `goreleaser check`; tests on macOS and Windows |
| `release.yml` | on a `v*` tag: `make check`, then goreleaser publishes the GitHub release with `checksums.txt` |
| `live.yml` | manual: installs the browser, runs `doctor` and checks for updates on Linux, macOS and Windows |
| `mlx-live.yml` | manual: starts a public 0.8B checkpoint through MLX on a macOS runner, checks memory, stop and orphan handling |

`AGENTS.md` and `CLAUDE.md` hold the rules for changes and reviews.

## Licence

MIT, see [LICENSE](LICENSE). Parts of pagevow are derived from
[browser-use/jev-ultrafast](https://github.com/browser-use/jev-ultrafast), used under the MIT License; see
[NOTICE](NOTICE).
