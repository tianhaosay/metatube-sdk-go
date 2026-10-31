package fanza

import (
	"sync"
	"time"
)

// fanzaCooldown enforces a minimum interval between consecutive requests to
// the DMM domain (single-flight + cooldown). It applies only to FANZA
// provider requests (movie search/info/reviews); other providers are
// unaffected. The DMM search page silently returns an empty result page when
// hit too frequently (anti-scraping), which previously made bulk scraping
// lose results after the first few items. Default cooldown is 3 seconds;
// it can be changed (or disabled with a negative value) via the
// `rate_limit` config key (MT_PROVIDER_FANZA__RATE_LIMIT).
type fanzaCooldown struct {
	mu       sync.Mutex
	interval time.Duration
	last     time.Time
}

var cooldown = &fanzaCooldown{interval: 3 * time.Second}

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
