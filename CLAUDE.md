# pagevow

@AGENTS.md

## Skills in this repository

`.claude/skills/` holds the Go skills that apply to this code. Load the ones that match the task before you write or
review code.

| Skill | Use for |
|---|---|
| `golang-error-handling` | wrapping, sentinel errors, `errors.Is` and `errors.As`, error text |
| `golang-concurrency` | goroutines, channels, locks, goroutine leaks |
| `golang-context` | deadlines, cancellation, context passed first |
| `golang-safety` | nil, slices, maps, integer conversion, panics |
| `golang-security` | command execution, file modes, secrets, input from pages and config |
| `golang-performance` | allocation and hot path patterns |
| `golang-benchmark` | benchmarks, pprof, benchstat |
| `my-golang-performancer` | the goperf.dev patterns with their numbers |

## Review checklist

A review of this repository checks these points, in this order.

1. Process handling. pagevow signals only a process that it started and that passes the record check (pid, start
   time, command line). A process group is signalled only when it can be tied to a record. No orphaned model server
   or browser after `stop`, after a failed `start`, or after a test.
2. GPU safety. `start` refuses when the known peak plus the margin does not fit. Mode `default` is refused for models
   other than 0.8B. The supervisor stops the model on a guard breach.
3. Secrets. No key value in a log, a trace, a record, a spec file, an error text or JSON output. Key references only
   in the config file.
4. Request bytes. The body sent to `/v1/systemone` keeps its key order and content. The decision model gives
   different answers when the key order changes.
5. Browser actions. No retry of a browser mutation. Targets map to observed elements. Password fields are never
   read or typed.
6. Errors. Wrapped with context, compared with `errors.Is` or `errors.As`, no panic outside start-up.
7. Context. Every exported function that can block takes `context.Context` first and returns when it is cancelled.
8. Concurrency. Every goroutine has an owner and an end. Tests pass with `-race`.
9. Portability. Code behind build tags compiles for Linux, macOS and Windows. Paths come from `os.UserConfigDir`
   and `os.UserCacheDir`.
10. Tests. Offline: no network, no model, no paid API, no real keychain. A real browser only behind the `browser`
    build tag.
11. Performance. Measure before a change: a benchmark or a profile. No optimisation without numbers.
12. Comments. Only where needed, two sentences at most.
