package manage

import (
	"strings"
	"sync"
	"time"
)

// Settings are the runtime toggles shown read-only in the console.
//
// Their single source of truth is the native MetaTube plugin settings page
// (MetaTube.json, see plugin_translate.go). The console never writes them.
type Settings struct {
	FanzaRateLimit string `json:"fanza_rate_limit"`
	JavdbRateLimit string `json:"javdb_rate_limit"`
	ActorFallback  bool   `json:"actor_fallback"`
	DisableSummary bool   `json:"disable_summary"`
}

var (
	settingsMu sync.Mutex
	settings   Settings
)

func parseDurationOr(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0, true
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, false
	}
	return d, true
}

// GetSettings returns the plugin-mirrored settings for the console's read-only
// view. Startup defaults (actor fallback on) apply until the first plugin sync.
func GetSettings() Settings {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	return settings
}

// initSettings seeds the mirror with safe defaults. Real values arrive from
// MetaTube.json via StartPluginSettingsWatch and applyPluginSettings.
func initSettings() {
	settingsMu.Lock()
	settings = Settings{ActorFallback: true}
	settingsMu.Unlock()
}
