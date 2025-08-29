package export

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"errors"
	"os"
	"sort"

	"logsense/internal/model"
)

func ToCSV(path string, entries []model.LogEntry) error {
	if len(entries) == 0 {
		return errors.New("no entries")
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	cols := columns(entries)
	if err := w.Write(cols); err != nil {
		return err
	}
	for _, e := range entries {
		row := make([]string, len(cols))
		for i, c := range cols {
			if c == "ts" && e.Timestamp != nil {
				row[i] = e.Timestamp.Format("2006-01-02T15:04:05Z07:00")
				continue
			}
			if c == "level" {
				row[i] = e.Level
				continue
			}
			if c == "raw" {
				row[i] = e.Raw
				continue
			}
			if v, ok := e.Fields[c]; ok {
				b, _ := json.Marshal(v)
				row[i] = string(b)
			}
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}
	return nil
}

func ToNDJSON(path string, entries []model.LogEntry) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	bw := bufio.NewWriter(f)
	defer bw.Flush()
	for _, e := range entries {
		b, _ := json.Marshal(e)
		if _, err := bw.Write(append(b, '\n')); err != nil {
			return err
		}
	}
	return nil
}

func columns(entries []model.LogEntry) []string {
	set := map[string]struct{}{"raw": {}}
	for _, e := range entries {
		for k := range e.Fields {
			set[k] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	// prefer ts, level first
	res := []string{"ts", "level"}
	for _, c := range out {
		if c != "ts" && c != "level" {
			res = append(res, c)
		}
	}
	return res
}
