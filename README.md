# pagevow

pagevow runs browser tests written as goals. A decision model drives a real Chromium step by step, an independent
verifier checks the final page, and every test leaves PNG screenshots. A Claude Code plugin runs the suite after a
coding task and sends failures back to Claude.

Status: phase 6 is in progress. `v0.1.0` is released and the Linux path is verified live. A local model on macOS with
Apple Silicon now runs through MLX; the manual `mlx-live` workflow checks it with a public 0.8B checkpoint, and the
private 4B suite on a real Mac is still to run. `pagevow run` drives a headless Chromium with a decision backend, verifies each final page and writes
screenshots. `pagevow start`, `stop`, `status` and `doctor` manage the local model servers, the local text helper and a
browser. `pagevow hook stop` and `pagevow plugin` provide the Claude Code Stop hook and plugin. `pagevow install --browser`
downloads a pinned Chrome for Testing, and `pagevow update` replaces the binary with the latest GitHub release. goreleaser
builds the release archives on a version tag, and CI runs the tests on Linux, macOS and Windows. The design is
in [docs/SPEC.md](docs/SPEC.md).

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

## Install a model

```
pagevow install --model ~/train/out/jev-4b           # copies a run directory into <kev_dir>/runs/jev-4b
pagevow install --model /path/to/run --name mine --link   # links it instead of copying; the record stays in <kev_dir>/runs
pagevow install --model owner/name@main               # downloads a Hugging Face repository, every file checked by hash
```

A run directory holds the LoRA adapter, the pointer head and the tokenizer; the base model named in `adapter_config.json`
is fetched by kev on first use. Keep the source outside `<kev_dir>/runs`: a source that is, or sits inside, the target
directory is refused, and `--link` refuses any source inside the runs directory. A copy follows a symbolic link only to a file inside the source directory, so a Hugging
Face cache snapshot needs `--link`, or a download with `--local-dir`. Installing the same unchanged directory again does
nothing; once the directory changed, `--force` copies it again. A download has no overall time limit and fails only when
no data arrives for 2 minutes. With `--browser` and `--model`, the model is installed first. For a private repository set `HF_TOKEN` or `HUGGING_FACE_HUB_TOKEN`, or store the token
with `pagevow keys set huggingface`. The token is never printed. After the install, `pagevow use local --model <name>
--mode nf4` (or `--mode bf16` on macOS) selects it. pagevow ships no model of its own.

## Install the browser

```
pagevow install --browser          # downloads Chrome for Testing 154.0.8037.92, about 190 MB
pagevow install --browser --force  # installs it again
pagevow install --browser --json   # prints version, platform, executable and already_installed
```

The download goes to `<user cache directory>/pagevow/browser/<version>/`. pagevow checks the size and the SHA-256 of the
archive against values that are pinned in the code (Google publishes none) and records the install in
`<user cache directory>/pagevow/browser/installed.json`. Linux (amd64, arm64), macOS (arm64, amd64) and Windows
(amd64, 386) have a build; other systems, such as Windows on arm64, do not. `run`, `start` and `doctor` use the installed
build before any Chromium or Chrome on `PATH`. The command refuses while the browser of `pagevow start` runs: run
`pagevow stop` first. `status` and `doctor` show the installed version.

## Update

```
pagevow update            # replaces the binary with the latest GitHub release
pagevow update --check    # prints the two versions, downloads nothing, exits 1 when a newer release exists
pagevow update --force    # installs the latest release again
pagevow update --json     # prints current, latest, update_available, updated and executable
```

The repository is private, so `update` needs a token that can read it: `GITHUB_TOKEN`, else `GH_TOKEN`, else the
keychain entry `github` (`pagevow keys set github`). The token is sent to `api.github.com` only, is dropped on a
redirect to another host or port, and is never printed. `update` downloads `pagevow_<version>_<os>_<arch>.tar.gz`
(`.zip` on Windows) and `checksums.txt` from the release, checks the SHA-256 of the archive and replaces the running
binary by renaming the new one over it. On Windows the running `pagevow.exe` is renamed to `pagevow.exe.old` first and
that file is removed the next time you run `update`, even when nothing is newer; if a process still runs from it, the
next update uses a `pagevow.exe.old-<random>` name instead, and `pagevow stop` ends a model server or browser you
started. When the directory of the binary is read-only, or the final rename fails, `update` keeps the verified binary in
`<user cache directory>/pagevow/update/` and tells you where. `checksums.txt` is not signed, so it catches a damaged
download, not a tampered release. A build that is not a release, such as `dev` or a `git describe` build like
`v1.2.3-5-gabc1234`, counts as older than every release. Run `pagevow plugin install` again after an update: the Stop
hook stores the path of the binary.

## Commands

| Command | State |
|---|---|
| `pagevow version` | works |
| `pagevow status [--json]` | works: active backend, paid API notice, URLs, browser settings, running processes, health, GPU, versions, guard messages |
| `pagevow use local\|jev\|custom\|cascade` | works: writes the backend into the config file |
| `pagevow start [--no-browser] [--json]` | works: starts the local model servers, the local text helper and a browser that stays running |
| `pagevow stop [--json]` | works: stops what `start` started |
| `pagevow doctor [--json]` | works: checks the setup and says how to fix each problem |
| `pagevow keys set\|unset\|list` | works: keychain entries and a names-only index, values are never printed |
| `pagevow init [DIR]` | works: writes a starter `pagevow.yaml`, refuses when a tests file already exists |
| `pagevow run [--tests FILE] [--ids a,b] [--out DIR] [--screenshots final\|failed\|all] [--retries N] [--timeout SECONDS] [--full-page] [--headed] [--json]` | works: see below |
| `pagevow hook stop` | works: the Claude Code Stop hook, see below |
| `pagevow plugin install [--no-register]`, `plugin uninstall`, `plugin path [--json]` | works: manage the Claude Code plugin, see below |
| `pagevow install --browser`, `install --model PATH|OWNER/NAME[@REV] [--name N] [--link] [--force] [--json]` | works: downloads and verifies the pinned Chrome for Testing; copies, links or downloads a model run directory into `<kev_dir>/runs/` |
| `pagevow update [--check] [--force] [--json]` | works: replaces the binary with the latest GitHub release after checking its SHA-256, see below |

## Running tests

`pagevow run` reads `pagevow.yaml` in the current directory (also `browser-tests.yaml` and
`.claude/browser-tests.yaml`) or the file given with `--tests`. Before any test it checks that the decision backend
answers, that the text helper answers when it is a loopback URL, and that a Chromium or Chrome is available (`pagevow install --browser` or one on `PATH`); a problem
is printed with the way to fix it and the exit code is 2. A model server for the `local` and `cascade` backends must
already be running: start it with `pagevow start`.

When `pagevow start` keeps a browser running and its debugging endpoint answers, `run` attaches to it, opens every
attempt in a fresh window and leaves the browser running. Otherwise `run` starts its own browser with a temporary
profile under the user cache directory, opens every attempt in a fresh window, and stops that browser when it ends,
also after an error or Ctrl+C. The browser is headless unless
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

## Claude Code plugin and Stop hook

```
pagevow plugin install      # writes the plugin and registers it with Claude Code
pagevow plugin path         # where it is: pass it to claude --plugin-dir
pagevow plugin uninstall
```

`plugin install` writes a small local marketplace to `<user config directory>/pagevow/claude-plugin/` and runs
`claude plugin marketplace add` and `claude plugin install pagevow@pagevow --scope user`. Without the `claude` program on
`PATH`, or with `--no-register`, it writes the files and prints the commands to run yourself. The plugin holds a skill,
the slash commands `/pagevow-run` and `/pagevow-init`, and a Stop hook that runs `pagevow hook stop` by the absolute path of
the binary. Run `pagevow plugin install` again after you upgrade or move pagevow.

When Claude tries to stop, the hook runs the tests file of the project (`pagevow.yaml` and the other names) and blocks
Claude while the tests fail, so Claude reads the `final.png` files and fixes the app or the test. It blocks at most
`PAGEVOW_HOOK_MAX_BLOCKS` times in a row per session (default 2, `0` never blocks), then lets Claude stop with a note that
the tests still fail. It skips the run when the project has not changed since the last pass (files that git ignores do not count), and `PAGEVOW_HOOK=0` turns
it off. A backend or browser that is not reachable never blocks: the hook says what to start (`pagevow start`,
`pagevow doctor`) and lets Claude stop. State lives in `.pagevow/` in the project; pagevow never edits `.gitignore`.

## Local servers and the browser

`pagevow start` starts what the active backend needs, then a browser:

| Process | Program | Started when |
|---|---|---|
| `model-<port>` | `uv run --extra serve python -m kev.serve --run <run> --port <port>` in `server.kev_dir` | backend `local`, or a loopback leg of `cascade` |
| `text-helper-<port>` | `llama-server -hfr <repo> -hff <file> --alias <alias> --host 127.0.0.1 --port <port> ...` | `text_helper.local.enabled` is true and `text_helper.url` is a loopback URL |
| `browser-<port>` | the browser from `pagevow install --browser`, else your Chromium or Chrome; headless unless `browser.headless` is false | always, unless `--no-browser` |

The order is models (largest first, each one waited for), the text helper, the browser. `start` is idempotent: a
process that is already running and answers is reported as `already running`. A port that answers but has no record
of pagevow is refused with a message that names the port. With backend `jev` or `custom` only the browser (and the
text helper, when enabled) starts. `start` prints the log path of every process. Exit code 0 means everything
requested runs, 2 means something did not start; the last 20 lines of the log are printed for a process that did not
become ready, and that process is stopped again. `--json` prints one JSON document with `ok`, `processes`
(`name`, `kind`, `pid`, `port`, `action`, `ready`, `log`, `error`), `warnings`, `stale_removed` and `problems`.

`pagevow stop` stops the browser, the text helper and the models in that order, then removes stale records. It only
signals a process whose recorded identity still matches. A model server gets SIGTERM and 10 seconds, then SIGKILL for
its process group; the browser gets SIGTERM and 5 seconds. When the supervisor of a model server or text helper is gone
while the program it started still runs (an orphan, possible on macOS after the supervisor was killed), `stop` sends
those signals to the program's process group itself and prints `<name> stopped (supervisor was gone)`. The managed browser profile
(`<user cache directory>/pagevow/profiles/managed`) stays on disk. Nothing to stop is exit code 0; a process that could
not be stopped is exit code 2.

`pagevow status` also lists the recorded processes (pid, port, state `ready`, `starting`, `orphaned` or `gone`, uptime, log), asks each
destination of the active backend for `/v1/models` with a 2 second timeout, shows GPU memory and temperature when
`nvidia-smi` answers (on a Mac with Apple Silicon the unified memory, without a temperature), the pagevow version and the browser version, and the messages left by the GPU guard. It exits
with 0 also when something is down. `--json` adds the keys `processes` (each with `state`, `alive`, `ready` and, for a
model server or text helper, `child_pid`), `health`, `gpu` (omitted when unknown; `unified`
is true on a Mac, where `components` lists the free, speculative, purgeable and file-backed MiB; elsewhere `unified` is omitted),
`versions`, `tripped` and `stale_removed` to the existing ones.

`pagevow doctor` runs a list of checks, each one `ok`, `warn` or `fail` with a finding and a fix: the config file, the
key references of the active backend (the value is never printed), the backend destination, on Linux and on macOS with
Apple Silicon `uv`, the kev checkout, the quantisation switch in `kev/checkpoint.py` (Linux only), the run directory,
the mode for the model, `nvidia-smi` and free GPU memory (the unified memory on a Mac), then the Chromium executable, the browser port, the text helper, stale records, guard messages and
the state and log directories. The exit code is 1 when a check fails; warnings do not change it.

### Modes and GPU memory

`backends.local.mode` and the cascade mode fields take `nf4`, `int8`, `bf16` or `default`. The first three set
`KEV_CUDA_GRAPHS=0`, `KEV_MAX_BATCH=1` and `PYTORCH_CUDA_ALLOC_CONF=expandable_segments:True` and always set
`KEV_LOAD_IN_4BIT` and `KEV_LOAD_IN_8BIT` (`1` for the one the mode names, `0` for the other), whatever the environment of
`pagevow start` holds, so the memory check always matches what runs.
`default` adds nothing, keeps CUDA graphs on and is refused for every model whose base model is larger than 1B
(read from `adapter_config.json` in the run directory). `model` is a run name under `<kev_dir>/runs/` or an absolute
path. pagevow never sets `KEV_API_KEY`: a local server is open and bound to 127.0.0.1.

Known peaks are `nf4` 5.6 GiB, `int8` 7.2 GiB, `bf16` 10.6 GiB and `default` 6.4 GiB. The local text helper counts 2.0 GiB when `text_helper.local.gpu_layers` is above 0 and nothing otherwise. Before it launches anything,
`start` adds up the peaks of the models and the text helper it is about to start (processes that already run are not counted), adds a margin of
1.5 GiB and compares the sum with the free GPU memory. When it does not fit, `start` refuses and prints all numbers.
Without `nvidia-smi` it warns and continues.

```
pagevow use local --model jev-4b --mode nf4
pagevow use cascade --primary-model jev-08b-d1a --primary-mode default --verifier-model jev-4b --verifier-mode nf4
pagevow use cascade --primary https://gpu.example.test --primary-key env:GPU_KEY
```

A cascade leg is started only when its URL is a loopback address. A remote leg is never started; its key reference
(`primary_key`, `verifier_key`) is resolved like the key of `jev` and `custom`, and the leg counts for the paid service
notice. Local model serving needs Linux with an NVIDIA GPU or macOS with Apple Silicon; on other systems `start` says
so and still starts the browser.

### macOS with Apple Silicon

On a Mac with Apple Silicon the model server runs through MLX. Install `uv`, clone kev and point `server.kev_dir` at
the clone, then choose mode `bf16` (or `default` for a model of 1B or less) and install the browser:

```
pagevow use local --model jev-4b --mode bf16
pagevow install --browser
pagevow start
```

pagevow sets `KEV_BACKEND=mlx`, and `/v1/models` reports `backend: mlx`. Modes `nf4` and `int8` are refused on macOS
because MLX serves bf16. Free memory is the unified memory read through `sysctl`: the free, purgeable and
file-backed pages (speculative pages are already inside the file-backed count). When `sysctl` cannot be read,
`start` refuses to start the model. The peaks are estimates until they are measured on a real Mac: 11.5 GiB for a 4B model and 3.0 GiB for a
model of 1B or less, plus the text helper and the 1.5 GiB margin. A model above 1B also needs 16 GiB of memory in
total. The temperature is not read, so `server.gpu_max_temp_c` has no effect on a Mac, and the
`server.gpu_min_free_mib` limit is not applied to unified memory until it is measured on a real Mac.

### The GPU guard

Every model server and the text helper runs under a small supervisor process (`pagevow supervise`, hidden from help).
When `server.gpu_watch` is true and `nvidia-smi` is present (the unified memory on a Mac), the supervisor samples the GPU
once per second. It stops
its process when the temperature reaches `server.gpu_max_temp_c` (default 87) or free memory falls to
`server.gpu_min_free_mib` (default 1500). It then writes `guard: stopped <name>: <reason> (temp N C, free N MiB)`
(without the temperature on a Mac) to
the log and to `<user cache directory>/pagevow/run/<name>.tripped`, and exits with code 99. `status` and `doctor`
show that message until the next `pagevow start` of the same process clears it. A sample that fails is ignored; five
in a row end the watch and leave the process running.

### Logs and records

Logs are appended to `<user cache directory>/pagevow/logs/<name>.log` (mode 0600). Records are one JSON file per
process in `<user cache directory>/pagevow/run/<name>.json` (directory 0700, files 0600, written atomically). A record
holds names, pids, ports, the command, times and paths, never a key. A record is alive only while the pid exists, its
start time is the recorded one and its command line still fits. A model server or text helper record whose supervisor
is gone while its program still runs (same pid, start time, process group and command) is orphaned: `status` and
`doctor` show it with the fix `pagevow stop`, `start` refuses to reuse its port, and `stop` ends it. Any other record is
stale, and `start`, `stop`, `status` and `doctor` remove it and say so.

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

The server settings and their defaults:

```
server:
  kev_dir: "~/kev"
  start_timeout_seconds: 600    # 1 to 3600
  gpu_watch: true
  gpu_max_temp_c: 87            # 40 to 100
  gpu_min_free_mib: 1500        # 0 to 65536
text_helper:
  local: {enabled: false, repo: "unsloth/Qwen3-1.7B-GGUF", file: "Qwen3-1.7B-Q4_K_M.gguf", alias: "qwen3-1.7b", gpu_layers: 99, start_timeout_seconds: 900}
```

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
make check             # gofmt check, go vet, golangci-lint, go test -race
make release-snapshot  # builds every release archive into dist/ without publishing, needs goreleaser
```

Pushing a tag such as `v1.2.3` runs `.github/workflows/release.yml`: `make check`, then goreleaser builds Linux, macOS and
Windows archives (Windows on arm64 is left out, Chrome for Testing has no build for it) and publishes the GitHub release
with `checksums.txt`. CI runs `goreleaser check` on every change.

CI runs the offline tests with `-race` on Linux in the `check` job, which also cross-builds for macOS on amd64 and Windows on
386. The `test` matrix covers macOS and Windows. The manual `live` workflow (`.github/workflows/live.yml`) installs the browser
and checks for updates on all three systems.

Install the linter with `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0`.

Portions derive from browser-use/jev-ultrafast; see [NOTICE](NOTICE).
