// Package progress tracks how many of a batch of movies the backend has
// actually answered. Emby's refresh call only queues work, so a "request sent"
// counter lies: the real progress is "how many items already came back from
// this service".
package progress

import (
	"strings"
	"sync"
)

var (
	mu      sync.Mutex
	pending map[string]struct{}
	total   int
	answers int64
)

// Answers returns how many movie lookups this service has answered so far.
func Answers() int64 {
	mu.Lock()
	defer mu.Unlock()
	return answers
}

// Unanswered reports how many keys of the current batch are still outstanding.
func Unanswered() int {
	mu.Lock()
	defer mu.Unlock()
	return len(pending)
}

// Key normalizes a provider id ("FANZA:ssis00001") into a lookup key.
func Key(provider, id string) string {
	return strings.ToLower(strings.TrimSpace(provider) + ":" + strings.TrimSpace(id))
}

// Begin starts a new batch; marking is only counted for these keys.
func Begin(keys []string) {
	mu.Lock()
	defer mu.Unlock()
	pending = make(map[string]struct{}, len(keys))
	for _, k := range keys {
		if k != "" {
			pending[k] = struct{}{}
		}
	}
	total = len(pending)
}

// Mark records that the backend answered for this key.
func Mark(key string) {
	mu.Lock()
	defer mu.Unlock()
	answers++
	if pending == nil {
		return
	}
	delete(pending, key)
}

// Status reports how many keys of the current batch are still outstanding.
func Status() (done, total int, remaining int) {
	mu.Lock()
	defer mu.Unlock()
	return total - len(pending), total, len(pending)
}

// Reset clears the batch.
func Reset() {
	mu.Lock()
	defer mu.Unlock()
	pending = nil
	total = 0
}
