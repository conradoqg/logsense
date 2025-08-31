package detect

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"logsense/internal/model"
	"logsense/internal/util/logx"
)

// cacheDir returns a directory under the OS temp dir to store schema caches.
func cacheDir() string {
	return filepath.Join(os.TempDir(), "logsense-schema-cache")
}

// cacheKey derives a stable key from the absolute file path.
func cacheKey(filePath string) (string, error) {
	if strings.TrimSpace(filePath) == "" {
		return "", errors.New("empty path")
	}
	abs, err := filepath.Abs(filePath)
	if err != nil {
		return "", err
	}
	h := sha1.Sum([]byte(abs))
	return hex.EncodeToString(h[:]), nil
}

// LoadSchemaFromCache attempts to read a cached schema for a given file path.
func LoadSchemaFromCache(filePath string) (model.Schema, bool) {
	key, err := cacheKey(filePath)
	if err != nil {
		return model.Schema{}, false
	}
	p := filepath.Join(cacheDir(), fmt.Sprintf("schema_%s.json", key))
	f, err := os.Open(p)
	if err != nil {
		return model.Schema{}, false
	}
	defer f.Close()
	var s model.Schema
	if err := json.NewDecoder(f).Decode(&s); err != nil {
		return model.Schema{}, false
	}
	return s, true
}

// SaveSchemaToCache writes the schema to cache keyed by file path.
func SaveSchemaToCache(filePath string, s model.Schema) error {
	key, err := cacheKey(filePath)
	if err != nil {
		return err
	}
	dir := cacheDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	p := filepath.Join(dir, fmt.Sprintf("schema_%s.json", key))
	tmp := p + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		return err
	}
	logx.Infof("detect: cached schema saved to %s", p)
	return nil
}
