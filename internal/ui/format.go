package ui

import (
    "encoding/json"
    "fmt"
    "sort"

    "logsense/internal/model"
)

func getCol(e model.LogEntry, c string) string {
    switch c {
    case "ts", "time", "timestamp":
        if e.Timestamp != nil {
            return e.Timestamp.Format("2006-01-02 15:04:05")
        }
        if v, ok := e.Fields[c]; ok {
            return anyToString(v)
        }
    case "level", "lvl", "severity":
        return e.Level
    case "source", "component":
        if e.Source != "" {
            return e.Source
        }
        if v, ok := e.Fields[c]; ok {
            return anyToString(v)
        }
    case "msg", "message":
        if v, ok := e.Fields[c]; ok {
            return anyToString(v)
        }
    }
    // For any non-special column, if the field exists, return it.
    if v, ok := e.Fields[c]; ok {
        return anyToString(v)
    }
    // Fallback first string field
    keys := make([]string, 0, len(e.Fields))
    for k := range e.Fields {
        keys = append(keys, k)
    }
    sort.Strings(keys)
    for _, k := range keys {
        if v, ok := e.Fields[k]; ok {
            return anyToString(v)
        }
    }
    return e.Raw
}

func anyToString(v any) string {
    switch t := v.(type) {
    case string:
        return t
    case float64, float32, int, int32, int64, uint, uint32, uint64, bool:
        return fmt.Sprint(t)
    default:
        b, _ := json.Marshal(t)
        return string(b)
    }
}

