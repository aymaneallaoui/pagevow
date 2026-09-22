# pagevow

pagevow runs browser tests written as goals. A decision model drives a real Chromium step by step, an independent
verifier checks the final page, and every test leaves PNG screenshots. A Claude Code plugin runs the suite after a
coding task and sends failures back to Claude.

Status: phase 0. The command tree, configuration, keychain handling and terminal output exist. Running tests, the
browser, the model backends, the Stop hook and the plugin are not implemented yet; those commands print
`not implemented yet (phase N)` and exit with code 2. The design is in [docs/SPEC.md](docs/SPEC.md).

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

| Command | Phase 0 |
|---|---|
| `pagevow version` | works |
| `pagevow status [--json]` | works: active backend, URLs, browser settings, config file |
| `pagevow use local\|jev\|custom\|cascade` | works: writes the backend into the config file |
| `pagevow keys set\|unset\|list` | works: keychain entries, values are never printed |
| `pagevow init [DIR]` | works: writes a starter `pagevow.yaml` |
| `pagevow run`, `start`, `stop`, `doctor` | phases 2 and 3 |
| `pagevow install`, `update` | phase 5 |
| `pagevow hook stop`, `plugin install\|uninstall\|path` | phase 4 |

## Configuration

The config file is `pagevow/config.yaml` in the user config directory. Environment variables named `PAGEVOW_*`
override it, and command line flags override both. Key fields hold references, never secrets: `keychain:NAME` or
`env:NAME`.

```
pagevow keys set typesafe < key.txt    # value comes from stdin or a hidden prompt
pagevow use jev
pagevow status
```

## Development

```
make check      # gofmt check, go vet, golangci-lint, go test -race
```

Install the linter with `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0`.

Portions derive from browser-use/jev-ultrafast; see [NOTICE](NOTICE).
