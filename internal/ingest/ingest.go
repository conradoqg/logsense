package ingest

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"time"

	"github.com/nxadm/tail"
)

type SourceKind string

const (
	SourceStdin SourceKind = "stdin"
	SourceFile  SourceKind = "file"
	SourceDemo  SourceKind = "demo"
)

type Options struct {
	Source         SourceKind
	Path           string
	Follow         bool
	ScanBufSize    int   // per-line max (bytes)
	BlockSizeBytes int64 // only for non-follow file read; 0 = all
}

type Line struct {
	Text   string
	Source string
	When   time.Time
}

func Read(ctx context.Context, opt Options) (<-chan Line, <-chan error) {
	out := make(chan Line, 1024)
	errs := make(chan error, 1)

	go func() {
		defer close(out)
		defer close(errs)

		switch opt.Source {
		case SourceStdin:
			readFromReader(ctx, os.Stdin, "stdin", opt.ScanBufSize, out, errs)
		case SourceFile:
			if opt.Follow {
				readFromTail(ctx, opt.Path, out, errs)
			} else if opt.BlockSizeBytes > 0 {
				readFromFileBlock(ctx, opt.Path, opt.BlockSizeBytes, opt.ScanBufSize, out, errs)
			} else {
				f, err := os.Open(opt.Path)
				if err != nil {
					errs <- err
					return
				}
				defer f.Close()
				readFromReader(ctx, f, opt.Path, opt.ScanBufSize, out, errs)
			}
		case SourceDemo:
			demo(ctx, out)
		default:
			errs <- errors.New("unknown source kind")
		}
	}()

	return out, errs
}

func readFromReader(ctx context.Context, r io.Reader, src string, maxBuf int, out chan<- Line, errs chan<- error) {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 1024*64)
	scanner.Buffer(buf, maxBuf)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		out <- Line{Text: scanner.Text(), Source: src, When: time.Now()}
	}
	if err := scanner.Err(); err != nil {
		errs <- err
	}
}

func readFromTail(ctx context.Context, path string, out chan<- Line, errs chan<- error) {
	t, err := tail.TailFile(path, tail.Config{
		Follow:    true,
		ReOpen:    true,
		MustExist: true,
		Logger:    tail.DiscardingLogger,
		Poll:      true,
		Location:  &tail.SeekInfo{Offset: 0, Whence: io.SeekEnd},
	})
	if err != nil {
		errs <- err
		return
	}
	defer t.Cleanup()
	for {
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case l, ok := <-t.Lines:
			if !ok {
				return
			}
			if l.Err != nil {
				errs <- l.Err
				continue
			}
			out <- Line{Text: l.Text, Source: path, When: time.Now()}
		}
	}
}

func readFromFileBlock(ctx context.Context, path string, blockBytes int64, maxBuf int, out chan<- Line, errs chan<- error) {
	f, err := os.Open(path)
	if err != nil {
		errs <- err
		return
	}
	defer f.Close()
	// Determine start offset
	var start int64 = 0
	if blockBytes > 0 {
		if st, err := f.Stat(); err == nil {
			if st.Size() > blockBytes {
				start = st.Size() - blockBytes
			}
		}
	}
	if start > 0 {
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			errs <- err
			return
		}
		// Drop partial first line
		br := bufio.NewReader(f)
		if _, err := br.ReadString('\n'); err != nil && err != io.EOF {
			errs <- err
			return
		}
		// Continue with scanner on remaining reader
		readFromReader(ctx, br, path, maxBuf, out, errs)
		return
	}
	readFromReader(ctx, f, path, maxBuf, out, errs)
}

// Read last N blocks (blockBytes each) from file and return lines (utility for paging older data).
// Removed file block helpers (ReadFileBlocks/ReadFileBlock) and scanAll since paging was dropped.

func demo(ctx context.Context, out chan<- Line) {
	samples := []string{
		`{"ts":"2025-01-01T12:00:00Z","level":"info","service":"api","msg":"server started","port":8080}`,
		`time=2025-01-01T12:00:01Z level=warn user_id=42 msg="slow request" path=/v1/items lat_ms=512`,
		`127.0.0.1 - - [01/Jan/2025:12:00:02 +0000] "GET /index.html HTTP/1.1" 200 1234 "-" "curl/8.0"`,
		`<34>1 2025-01-01T12:00:03Z myhost app - - - User login ok`,
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	i := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			out <- Line{Text: samples[i%len(samples)], Source: "demo", When: time.Now()}
			i++
		}
	}
}
