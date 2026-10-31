package fanza

import (
	"sync"
	"time"
)

// fanzaCooldown enforces a minimum interval between consecutive requests to
// the DMM domain (single-flight + cooldown). It applies only to FANZA
// provider requests (movie search/info/reviews); other providers are
// unaffected.
//
// Default is DISABLED (0), matching upstream metatube behaviour: DMM's
// anti-scraping on the search page is intermittent, and the cooldown trades
// scrape speed for long-tail reliability. Users can enable it (e.g. 3s) via
// the `rate_limit` config key (MT_PROVIDER_FANZA__RATE_LIMIT) or from a
// future GUI setting; a negative value also disables it.
type fanzaCooldown struct {
	mu       sync.Mutex
	interval time.Duration
	last     time.Time
}

var cooldown = &fanzaCooldown{interval: 0}

func setFanzaRateLimit(d time.Duration) {
	cooldown.mu.Lock()
	cooldown.interval = d
	cooldown.mu.Unlock()
}

// waitFanzaCooldown blocks until the cooldown since the previous DMM
// request has elapsed, then records the current time as the last request.
func waitFanzaCooldown() {
	cooldown.mu.Lock()
	defer cooldown.mu.Unlock()
	if cooldown.interval <= 0 {
		return
	}
	if elapsed := time.Since(cooldown.last); elapsed < cooldown.interval {
		time.Sleep(cooldown.interval - elapsed)
	}
	cooldown.last = time.Now()
}

// SetRateLimit adjusts the DMM cooldown at runtime (GUI settings).
// A zero or negative duration disables the cooldown.
func SetRateLimit(d time.Duration) { setFanzaRateLimit(d) }
