# logsense

Fast, pleasant log TUI built with Bubble Tea, Bubbles and Lipgloss. It reads logs from files (with follow/tail) and/or stdin, detects format, parses into a structured table with filters, search, and inspector. Optional OpenAI integration can help detect schemas, summarize, and explain.

## Installation

Requires Go 1.22+.

```
go run ./cmd/logsense --help
```

Build locally:

```
go build -o logsense ./cmd/logsense
```

## Basic Usage

- Read from stdin:

```
cat testdata/json_lines.ndjson | logsense
```

- Read a file with follow:

```
logsense --file=/var/log/app.log --follow
```

- Demo mode (no input):

```
logsense
```

## Key Flags

- `--file=PATH`: log file path
- `--follow`: follow (tail -f)
- `--stdin`: force stdin (auto-detected when piped)
- `--max-buffer=50000`: ring buffer size
- `--block-size-mb=16`: when reading a file without `--follow`, only load the last N MB (0 = whole file)
- `--theme=dark|light`
- `--offline`: disable OpenAI
- `--no-cache`: disable schema cache (skip read/write)
- `--openai-model=...`, `--openai-base-url=...`
- `--log-level=info|debug`
- `--time-layout=...`: force time layout
- `--format=json|regex|logfmt|apache|syslog`: force format
- `--export=csv|json --out=PATH`: export filtered view

## Shortcuts

- Space: Pause/Resume
- `/`: Search (plain text or regex between slashes, e.g. `/error|warn/`)
- `f`: Filters tab (WIP)
- `Enter`: Inspector
- `c`: Clear visible buffer
- `t`: Toggle follow
- `e`: Export filtered view (uses `--export` and `--out` when provided)
- `s`: Summarize (OpenAI, coming soon)
- `i`: Explain (OpenAI, coming soon)
- `r`: Re-detect format
- `g/G`: Go to top/bottom
- `?`: Help (popup)
- `x`: Stats for selected column (min/avg/max, distribution or distinct values)

## OpenAI

Environment variables:

- `OPENAI_API_KEY`: required to use LLM features
- `LOGSENSE_OPENAI_MODEL`: default `gpt-4o-mini`
- `LOGSENSE_OPENAI_BASE_URL`: proxy compatibility

The client uses `github.com/sashabaranov/go-openai` with timeouts. Calls are indicated in the UI (spinner + message).

## Notes

- Large files are read in blocks (last N MB) to avoid excessive memory usage.
- Format detection uses a fast heuristic on the first ~10 lines. If many parse failures occur consecutively, re-detection is triggered. If inconclusive and OpenAI is configured, it will ask the LLM (with a status bar indicator).

## Tests and Examples

```
go test ./...
```

Sample files in `testdata/`:

- `json_lines.ndjson`
- `logfmt.log`
- `apache_combined.log`
- `syslog.log`
- `k8s_container.json`

### Log Simulator (loggen)

Generate synthetic logs in real time (to test `--follow` and `--stdin`):

- Write files in `./simulateddata` for multiple formats:

```
make simulate-logs RATE=10 DURATION=30s
# Or choose specific formats (comma-separated):
make simulate-logs FORMAT=text,json_lines RATE=5 DURATION=10s
```

- Send a format to stdout (for piping):

```
make simulate-stdin FORMAT=json_lines RATE=5 DURATION=10s | ./logsense
```

Supported formats (loggen): `text`, `json_lines`.

Log generator behavior:
- On start, the output file is always truncated.
- On SIGINT/SIGTERM (Ctrl+C), the generator exits and removes any created files.
- For `json_lines`, a random schema is chosen once at startup and all records follow it.
- For `text`, a template is chosen once at startup and all lines follow it.

## Roadmap

- Advanced filters (level, time range, govaluate expressions)
- Summarize/Explain via OpenAI with optional redaction
- Schema cache per source signature
- Markdown summary export
- Polished dark/light themes
