package manage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// refreshProgress remembers which items a scope already refreshed, so a batch
// run can be paused and resumed (or repeated next day) without redoing work.
type refreshProgress struct {
	Key  string   `json:"key"`
	Done []string `json:"done"`
}

var progressMu sync.Mutex

func progressFilePath() string {
	cfg := getConfig()
	if cfg == nil || cfg.MappingActorFile == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(cfg.MappingActorFile), "refresh_progress.json")
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

// ResetRefreshProgress clears the saved progress of one scope.
func ResetRefreshProgress(scope, pathPrefix string) map[string]any {
	key := scope + "|" + pathPrefix
	prog := loadRefreshProgress()
	if prog.Key == key {
		saveRefreshProgress(refreshProgress{Key: key})
	}
	return map[string]any{"reset": true, "key": key}
}

// ProgressSummary reports how many items of a scope are already done.
func ProgressSummary(scope, pathPrefix string) map[string]any {
	key := scope + "|" + pathPrefix
	prog := loadRefreshProgress()
	done := 0
	if prog.Key == key {
		done = len(prog.Done)
	}
	return map[string]any{"key": key, "done": done}
}
