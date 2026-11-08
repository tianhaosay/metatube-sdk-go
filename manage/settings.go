package manage

import (
	"strings"
	"sync"
	"time"
)

// Settings are the runtime toggles shown (read-only) in the console.
//
// The single source of truth is the native MetaTube plugin settings page
// (MetaTube.json); see plugin_translate.go. The console does NOT write these
// anymore — editing happens in Emby → Plugins → MetaTube.
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
// view. Startup defaults (fallback on) apply until the first plugin sync.
func GetSettings() Settings {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	return settings
}

// initSettings seeds the mirror with safe defaults. The real values arrive
// from MetaTube.json via StartPluginTranslationWatch → applyPluginSettings.
func initSettings() {
	settingsMu.Lock()
	settings = Settings{ActorFallback: true}
	settingsMu.Unlock()
}


