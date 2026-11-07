package manage

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/metatube-community/metatube-sdk-go/common/throttle"
	"github.com/metatube-community/metatube-sdk-go/provider/fanza"
	"github.com/metatube-community/metatube-sdk-go/provider/javdb"
)

// Settings are the user-adjustable runtime knobs exposed in the console.
// They persist to a JSON file next to the mapping table and are applied to
// the running engine/providers immediately on save.
//
// Translation is NOT here: its engine and credentials come from the native
// MetaTube plugin settings (see plugin_translate.go).
type Settings struct {
	// Rate limits (cooldown between consecutive requests per provider).
	FanzaRateLimit string `json:"fanza_rate_limit"`
	JavdbRateLimit string `json:"javdb_rate_limit"`

	// ActorFallback enables the JavDB actor fallback: when the primary
	// provider returns zero actors, the backend asks JavDB once. Default on.
	ActorFallback bool `json:"actor_fallback"`

	// DisableSummary clears the summary instead of translating it.
	DisableSummary bool `json:"disable_summary"`
}

var (
	settingsMu       sync.Mutex
	settings         Settings
	settingsFile     string
	settingsFromFile bool
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

// applySettings pushes settings onto live components. Translation is applied
// separately (from the plugin config), so it is never touched here.
func applySettings(s Settings) {
	if d, ok := parseDurationOr(s.FanzaRateLimit); ok {
		throttle.Set(fanza.Name, d)
	}
	if d, ok := parseDurationOr(s.JavdbRateLimit); ok {
		throttle.Set(javdb.Name, d)
	}
	app := getPipeline()
	if app == nil {
		return
	}
	app.SetActorFallbackEnabled(s.ActorFallback)
	app.SetDisableSummary(s.DisableSummary)
}

func settingsFilePath() string {
	c := getConfig()
	if c == nil || c.ActorMappingPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(c.ActorMappingPath), "gui_settings.json")
}

// initSettings loads persisted settings at startup. ActorFallback defaults to
// true, including when no settings file exists yet.
func initSettings() {
	settingsMu.Lock()
	settingsFile = settingsFilePath()
	s, loaded := loadSettingsFileWithStatus()
	if !loaded {
		s.ActorFallback = true
	}
	settings = s
	settingsFromFile = loaded
	settingsMu.Unlock()
	applySettings(settings)
	log.Printf("[manage] settings loaded: file=%v fanza_limit=%q javdb_limit=%q actor_fallback=%v",
		loaded, settings.FanzaRateLimit, settings.JavdbRateLimit, settings.ActorFallback)
}

func loadSettingsFileWithStatus() (Settings, bool) {
	var s Settings
	path := settingsFilePath()
	if path == "" {
		return s, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return s, false
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, false
	}
	return s, true
}

func saveSettingsFile(s Settings) {
	path := settingsFilePath()
	if path == "" {
		return
	}
	if data, err := json.MarshalIndent(s, "", "  "); err == nil {
		_ = os.WriteFile(path, data, 0o644)
	}
}

// GetSettings returns the current settings for the console.
func GetSettings() Settings {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	return settings
}

// UpdateSettings validates, persists and applies new settings, then rebuilds
// the translator from the plugin config (keeping the two in sync).
func UpdateSettings(s Settings) Settings {
	settingsMu.Lock()
	settings = s
	settingsFromFile = true
	saveSettingsFile(s)
	settingsMu.Unlock()
	applySettings(s)
	applyPluginTranslation()
	log.Printf("[manage] settings updated: fanza_limit=%q javdb_limit=%q actor_fallback=%v",
		s.FanzaRateLimit, s.JavdbRateLimit, s.ActorFallback)
	return s
}
