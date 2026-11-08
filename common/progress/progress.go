// Package progress counts how many movie lookups this service has answered.
// Emby's refresh call only queues work, so the counter tracks returned answers
// rather than sent requests.
package progress

import (
	"sync"
)

var (
	mu      sync.Mutex
	answers int64
)

// Answers returns how many movie lookups this service has answered so far.
func Answers() int64 {
	mu.Lock()
	defer mu.Unlock()
	return answers
}

// Mark records one answered movie lookup.
func Mark() {
	mu.Lock()
	answers++
	mu.Unlock()
}
