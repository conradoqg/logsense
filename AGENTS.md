# Repository Guidelines

## Project Structure & Modules

- `cmd/logsense/`: CLI entrypoint (`main.go`).
- `internal/`: core packages:
  - `config`, `ingest`, `detect`, `parse`, `filter`, `export`, `model`, `ui`, `util`.
- `testdata/`: sample logs used by tests and manual runs.
- `Makefile`: common tasks. Go 1.22+ is required.

Suggested flow: ingest → detect schema → parse to `model` → render in `ui` or `export`.

## Build, Test, and Dev Commands

- `make build`: builds `logsense` binary to project root.
- `make run`: runs `cmd/logsense` (honors CLI flags).
- `make test`: runs all Go tests (`./...`).
- `make lint`: `go vet` static checks.

Examples:
- `go build -o logsense ./cmd/logsense`
- `cat testdata/json_lines.ndjson | ./logsense`

## Coding Style & Naming

- Formatting: run `gofmt -s -w .` (Go default formatting). Prefer tabs as emitted by `gofmt`.
- Linting: keep `go vet ./...` clean; avoid unused code and dead exports.
- Package layout: keep user-visible code inside `cmd/` and internal logic under `internal/<domain>`.
- Naming: exported identifiers use Go’s `CamelCase`; private use `camelCase`. Keep files cohesive by domain (e.g., `parser.go`, `heuristics.go`).

## Testing Guidelines

- Framework: standard `testing` package.
- Location: tests live next to sources as `*_test.go` (e.g., `internal/parse/parser_test.go`).
- Data: prefer fixtures in `testdata/` and reference with relative paths.
- Running: `make test` or `go test ./...`. Add targeted tests for new parsers/heuristics and UI-free logic.

## Commit & Pull Requests

- Commits: use concise, imperative subjects (e.g., "add logfmt parser"). If helpful, follow Conventional Commits (`feat:`, `fix:`, `refactor:`) for clarity and changelogability.
- PRs: include purpose, key changes, flags/env impacted, and screenshots/gifs for UI behavior. Link issues. Keep PRs focused and small when possible.

## Security & Configuration

- Do not commit secrets. LLM usage reads `OPENAI_API_KEY` and supports `LOGSENSE_OPENAI_MODEL` and `LOGSENSE_OPENAI_BASE_URL`.
- Local runs can disable networked features with `--offline`.
- Useful flags: `--file`, `--follow`, `--stdin`, `--max-buffer`, `--block-size-mb`, `--theme`, `--format`, `--export`/`--out`.
