package manifest

import (
	"errors"
	"path/filepath"

	v1 "github.com/octacian/backlot/api/v1"
)

// SourceError attaches safe source identity to the shared error itself so JSON
// extraction cannot discard it. Only sanitized domain errors retain messages.
func SourceError(err error, layer, path string) error {
	if err == nil {
		return nil
	}
	detail := &v1.PlanError{Code: "configuration_source", Message: "configuration source unavailable; check file path and permissions"}
	var original *v1.PlanError
	if errors.As(err, &original) {
		if original.SourceLayer != "" {
			return original
		}
		copy := *original
		detail = &copy
	}
	if path != "" && path != "stdin" {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
	}
	detail.SourceLayer, detail.SourcePath = layer, path
	if detail.Code == "permissions" {
		detail.Message = "configuration source must be owned by this user, private (directories 0700, files 0600), regular and not a symlink; inspect project setup and restore file permissions"
	}
	switch layer {
	case "machine":
		detail.Message += "; edit selected machine file " + path + " to correct this source"
	case "private", "registry":
		detail.Message += "; inspect project setup and restore or repair the selected file"
	case "import":
		detail.Message += "; correct the setup import before retrying"
	case "manifest":
		detail.Message += "; correct the selected manifest"
	}
	return detail
}
