package manage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// ignoredDuplicates is a local list of duplicate groups the operator decided
// NOT to merge (e.g. two different people who happen to share a name). It is
// stored next to the mapping tables so it survives restarts.
var (
	ignoreMu     sync.Mutex
	ignoreLoaded bool
	ignoreSet    map[string]struct{}
)

func ignoreFilePath() string {
	c := getConfig()
	if c == nil || c.ActorMappingPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(c.ActorMappingPath), "ignore_duplicates.json")
}

func loadIgnoreList() map[string]struct{} {
	ignoreMu.Lock()
	defer ignoreMu.Unlock()
	if ignoreLoaded {
		return ignoreSet
	}
	ignoreSet = map[string]struct{}{}
	ignoreLoaded = true
	path := ignoreFilePath()
	if path == "" {
		return ignoreSet
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ignoreSet
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return ignoreSet
	}
	for _, k := range list {
		ignoreSet[k] = struct{}{}
	}
	return ignoreSet
}

func saveIgnoreList(set map[string]struct{}) error {
	path := ignoreFilePath()
	if path == "" {
		return nil
	}
	list := make([]string, 0, len(set))
	for k := range set {
		list = append(list, k)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// IgnoredDuplicateGroups returns the keys currently ignored.
func IgnoredDuplicateGroups() []string {
	set := loadIgnoreList()
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}

// IgnoreDuplicateGroup adds a group key to the ignore list.
func IgnoreDuplicateGroup(key string) error {
	if key == "" {
		return nil
	}
	set := loadIgnoreList()
	ignoreMu.Lock()
	defer ignoreMu.Unlock()
	if _, ok := set[key]; ok {
		return nil
	}
	set[key] = struct{}{}
	return saveIgnoreList(set)
}

// UnignoreDuplicateGroup removes a key from the ignore list.
func UnignoreDuplicateGroup(key string) error {
	set := loadIgnoreList()
	ignoreMu.Lock()
	defer ignoreMu.Unlock()
	delete(set, key)
	return saveIgnoreList(set)
}

func isIgnored(key string) bool {
	_, ok := loadIgnoreList()[key]
	return ok
}
