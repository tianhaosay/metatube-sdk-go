// Package seed embeds the genre mapping table that ships with the build and
// installs it on first start, so a fresh deployment has a working genre
// dictionary without any manual file copying.
package seed

import (
	_ "embed"
	"os"
	"path/filepath"
)

//go:embed mapping_info.xml
var mappingInfo []byte

// MappingInfoXML returns the built-in genre mapping table.
func MappingInfoXML() []byte {
	return mappingInfo
}

// EnsureMappingInfo writes the built-in table to path when no table exists yet.
// An existing file is left untouched, so user edits always win.
func EnsureMappingInfo(path string) (bool, error) {
	if path == "" || len(mappingInfo) == 0 {
		return false, nil
	}
	if _, err := os.Stat(path); err == nil {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, mappingInfo, 0o644); err != nil {
		return false, err
	}
	return true, nil
}
