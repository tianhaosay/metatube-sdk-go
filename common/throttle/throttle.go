// Package throttle is the single cooldown registry used by every provider.
//
// Providers call Wait(name) before an outbound request; the manage console
// adjusts the interval at runtime. Interval 0 (the default) means no
// throttling at all, so a provider only pays for what it asks for.
//
// Unlike the previous per-provider implementations, an entry sleeps while
// holding only its own lock: a slow javdb request never blocks fanza, and a
// settings update (Set) never waits behind a sleeping request.
package throttle

import (
	"sync"
	"time"
)

type entry struct {
	mu       sync.Mutex
	interval time.Duration
	last     time.Time
}

var entries sync.Map // provider name -> *entry

func get(name string) *entry {
	if v, ok := entries.Load(name); ok {
		return v.(*entry)
	}
	e := &entry{}
	actual, _ := entries.LoadOrStore(name, e)
	return actual.(*entry)
}

// Set configures the minimum interval between two consecutive requests for
// name. Zero or negative disables throttling.
func Set(name string, d time.Duration) {
	e := get(name)
	e.mu.Lock()
	e.interval = d
	e.mu.Unlock()
}

// Interval reports the configured interval for name.
func Interval(name string) time.Duration {
	e := get(name)
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.interval
}

// Wait blocks until the cooldown since the previous request for name has
// elapsed, then records the current time as the last request.
func Wait(name string) {
	e := get(name)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.interval <= 0 {
		e.last = time.Now()
		return
	}
	if elapsed := time.Since(e.last); elapsed < e.interval {
		time.Sleep(e.interval - elapsed)
	}
	e.last = time.Now()
}
