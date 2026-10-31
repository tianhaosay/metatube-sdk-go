package javdb

import (
	"sync"
	"time"
)

// javdbCooldown enforces a minimum interval between consecutive requests to
// the javdb domain (single-flight + cooldown). It applies only to JavDB
// provider requests (movie search/info and actor search/info); all other
// providers scrape at full speed. Default cooldown is 10 seconds, matching
// the empirically safe rate against Cloudflare throttling; it can be
// changed (or disabled with a negative value) via the `rate_limit` config
// key (MT_PROVIDER_JAVDB__RATE_LIMIT).
type javdbCooldown struct {
	mu       sync.Mutex
	interval time.Duration
	last     time.Time
}

var cooldown = &javdbCooldown{interval: 10 * time.Second}

func setJavdbRateLimit(d time.Duration) {
	cooldown.mu.Lock()
	cooldown.interval = d
	cooldown.mu.Unlock()
}

// waitJavdbCooldown blocks until the cooldown since the previous javdb
// request has elapsed, then records the current time as the last request.
func waitJavdbCooldown() {
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
