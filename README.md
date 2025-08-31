# logsense

Logsense is a fast, keyboard-driven log viewer for the terminal. It auto-detects log formats, streams data from files or stdin, and renders a structured, filterable table so you can spot issues quickly without leaving the shell. Built with Bubble Tea, Bubbles, and Lipgloss.

Highlights:

- Auto-detects formats: JSON Lines, logfmt, Apache Combined, RFC5424 syslog (with sensible fallback).
- Streaming support: can follow files like tail -f; by default starts from existing content (non-follow) and can read only the last N MB for quick scans.
- Powerful TUI: instant search (plain or regex), column stats, inspector, copy line, pause/resume, toggle follow.
- Structured export: write filtered results to CSV or JSON.
- Works anywhere: read from a path or pipe from any command.
- Optional AI assist: use OpenAI to improve schema detection and explain records (can be fully disabled with --offline).

## Installation

Requires Go 1.22+.

```
go run ./cmd/logsense --help
```

Build locally:

```
make build
./logsense --version
```

## Basic Usage

- Read from stdin:

```
cat testdata/json_lines.ndjson | logsense
```

- Read a file (not following by default; press `t` or use `--follow` to tail):

```
logsense --file=/var/log/app.log
```

- Demo mode (no input):

```
logsense
```

## Key Flags

- `--file=PATH`: log file path
- `--follow`: when using `--file`, start in follow mode (tail -f)
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
- `--version`: print version and exit

## Docker

Pull the image from GHCR:

```
docker pull ghcr.io/conradoqg/logsense:latest
```

Run with a file mounted (recommended):

```
docker run --rm -it \
  -v /var/log:/var/log:ro \
  --env OPENAI_API_KEY \
  ghcr.io/conradoqg/logsense:latest \
  --file /var/log/syslog
```

Important: the TUI requires a TTY. Piping into Docker with only `-i` does not allocate a TTY, so the UI will not render. Use one of the options below when you need to feed logs from another command into the containerized Logsense.

### Feed live logs into Docker (TTY-safe options)

- Write to a file and mount it (simple and robust):

  - One-liner (background writer + TUI reader):

    ```
    kubectl logs deploy/ingress-nginx-controller -f -n tks-system > /tmp/ingress.log 2>/dev/null & writer=$! \
    && docker run --rm -it -v /tmp:/data ghcr.io/conradoqg/logsense:latest --file /data/ingress.log \
    ; kill $writer
    ```

  - Two terminals:

    - A: `kubectl logs deploy/ingress-nginx-controller -f -n tks-system > /tmp/ingress.log 2>/dev/null`
    - B: `docker run --rm -it -v /tmp:/data ghcr.io/conradoqg/logsense:latest --file /data/ingress.log`

  - Tips: truncate first with `: > /tmp/ingress.log`; limit initial read with `--block-size-mb 50`.

- Use a PTY wrapper to keep a TTY across a pipe:

  ```
  kubectl logs deploy/ingress-nginx-controller -f -n tks-system 2>/dev/null \
    | script -q -c "docker run --rm -it ghcr.io/conradoqg/logsense:latest --stdin" /dev/null
  ```

  The `script` command allocates a pseudo‑TTY so the TUI can render while reading stdin.

- Use a named pipe (FIFO):

  ```
  tmpdir=$(mktemp -d); mkfifo "$tmpdir/pipe"
  kubectl logs deploy/ingress-nginx-controller -f -n tks-system > "$tmpdir/pipe" 2>/dev/null & kubepid=$!
  docker run --rm -it -v "$tmpdir:/data" ghcr.io/conradoqg/logsense:latest --file /data/pipe
  kill "$kubepid" 2>/dev/null; rm -rf "$tmpdir"
  ```

Notes:
- Redirect `2>/dev/null` to drop aliases/warnings from `kubectl` that would otherwise mix with logs.
- On macOS/Windows, ensure the mounted directory (e.g., `/tmp`) is shared with Docker Desktop.

Local piping (no Docker) works normally and preserves the TUI:

```
kubectl logs deploy/ingress-nginx-controller -f -n tks-system | ./logsense --stdin
```

Images are multi-arch (linux/amd64, linux/arm64) and built on tags and main branch.

## Releases

Official binaries for Linux, macOS, and Windows are attached to GitHub Releases: https://github.com/conradoqg/logsense/releases

Docker images are published to GHCR at `ghcr.io/conradoqg/logsense` with matching tags and `latest` on the default branch.

## Shortcuts

- Space: Pause/Resume
- `/`: Search (plain text or regex between slashes, e.g. `/error|warn/`)
- `f`: Filters tab (WIP)
- `Enter`: Inspector
- `c`: Copy current line
- `t`: Toggle follow
- `e`: Export filtered view (uses `--export` and `--out` when provided)
- `i`: Explain (OpenAI)
- `d`: Detect format
- `g/G`: Go to top/bottom
- `?`: Help (popup)
- `x`: Stats for selected column (min/avg/max, distribution or distinct values)

## OpenAI

Environment variables:

- `OPENAI_API_KEY`: required to use LLM features
- `LOGSENSE_OPENAI_MODEL`: default `gpt-5-mini`
- `LOGSENSE_OPENAI_BASE_URL`: proxy compatibility

The client uses `github.com/sashabaranov/go-openai` with timeouts. Calls are indicated in the UI (spinner + message).

## Notes

- Large files are read in blocks (last N MB) to avoid excessive memory usage.
- Format detection uses a fast heuristic on the first ~10 lines. Use `r` to trigger re-detection; if OpenAI is configured, it will ask the LLM then (with a status bar indicator).

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
- Explain via OpenAI with optional redaction
- Schema cache per source signature
- Markdown summary export
- Polished dark/light themes
