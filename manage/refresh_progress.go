package manage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// refreshProgress records which item ids one scope already refreshed, so a
// batch run can resume without redoing work.
type refreshProgress struct {
	Key  string   `json:"key"`
	Done []string `json:"done"`
}

var progressMu sync.Mutex

// progressKey identifies one resume scope. The mode is part of the key because
// a wipe run and a refresh run must not share their done list.
func progressKey(mode, scope, pathPrefix string) string {
	return mode + "|" + scope + "|" + pathPrefix
}

func progressFilePath() string {
	cfg := getConfig()
	if cfg == nil || cfg.ActorMappingPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(cfg.ActorMappingPath), "refresh_progress.json")
}

func loadRefreshProgress() refreshProgress {
	progressMu.Lock()
	defer progressMu.Unlock()
	path := progressFilePath()
	if path == "" {
		return refreshProgress{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return refreshProgress{}
	}
	var prog refreshProgress
	if json.Unmarshal(data, &prog) != nil {
		return refreshProgress{}
	}
	return prog
}

func saveRefreshProgress(prog refreshProgress) {
	progressMu.Lock()
	defer progressMu.Unlock()
	path := progressFilePath()
	if path == "" {
		return
	}
	if data, err := json.Marshal(prog); err == nil {
		_ = os.WriteFile(path, data, 0o644)
	}
}

// ResetRefreshProgress clears the saved progress of one scope, regardless of
// whether it belongs to a refresh or a wipe run.
func ResetRefreshProgress(scope, pathPrefix string) map[string]any {
	suffix := "|" + scope + "|" + pathPrefix
	prog := loadRefreshProgress()
	reset := false
	if prog.Key == "refresh"+suffix || prog.Key == "wipe"+suffix {
		saveRefreshProgress(refreshProgress{})
		reset = true
	} else if strings.HasSuffix(prog.Key, suffix) {
		saveRefreshProgress(refreshProgress{})
		reset = true
	}
	return map[string]any{"reset": reset, "scope": strings.TrimPrefix(suffix, "|")}
}
