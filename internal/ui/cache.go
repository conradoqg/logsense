package ui

import (
	"logsense/internal/detect"
	"logsense/internal/model"
)

func detectSaveSchema(path string, s model.Schema) error {
	return detect.SaveSchemaToCache(path, s)
}
