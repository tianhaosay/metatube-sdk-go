package manage

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/metatube-community/metatube-sdk-go/provider/fanza"
	"github.com/metatube-community/metatube-sdk-go/provider/javdb"
	"github.com/metatube-community/metatube-sdk-go/translate"
)

// Settings are the user-adjustable runtime knobs exposed in the GUI.
// They persist to a JSON file next to the mapping table and are applied to
// the running engine/providers immediately on save.
type Settings struct {
	// Rate limits (cooldown between consecutive requests per provider).
	// "0" disables; duration strings like "3s" / "500ms".
	FanzaRateLimit string `json:"fanza_rate_limit"`
	JavdbRateLimit string `json:"javdb_rate_limit"`

	// Metadata translation.
	Translate        bool   `json:"translate"`
	TranslateEngines string `json:"translate_engines"` // primary,secondary
	TranslateLang    string `json:"translate_lang"`

	// Summary switch.
	DisableSummary bool `json:"disable_summary"`
}

type translateApp interface {
	SetTranslator(t translate.Translator)
	SetDisableSummary(v bool)
}

var (
	settingsMu   sync.Mutex
	settings     Settings
	settingsFile string
	settingsApp  translateApp
)

// SetTranslateApp wires the engine so settings can swap the translator at
// runtime. Called from cmd.Router after the engine is created.
func SetTranslateApp(app translateApp) {
	settingsMu.Lock()
	settingsApp = app
	settingsMu.Unlock()
	// Re-apply persisted settings now that the engine is available.
	applySettings(settings)
}

// parseDurationOr handles "" / "0" as disabled (0).
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

// applySettings pushes the given settings onto live components.
func applySettings(s Settings) {
	if d, ok := parseDurationOr(s.FanzaRateLimit); ok {
		fanza.SetRateLimit(d)
	}
	if d, ok := parseDurationOr(s.JavdbRateLimit); ok {
		javdb.SetRateLimit(d)
	}
	settingsMu.Lock()
	app := settingsApp
	settingsMu.Unlock()
	if app == nil {
		return
	}
	if s.Translate && strings.TrimSpace(s.TranslateEngines) != "" {
		app.SetTranslator(buildFailoverTranslator(s.TranslateEngines))
	} else {
		app.SetTranslator(nil)
	}
	app.SetDisableSummary(s.DisableSummary)
}

// buildFailoverTranslator creates a failover translator from a CSV engine
// list. Engine params are read from the environment
// (MT_TRANSLATE_<ENGINE>__<PARAM>), the same source cmd uses at startup.
func buildFailoverTranslator(enginesCSV string) translate.Translator {
	names := strings.Split(enginesCSV, ",")
	for i := range names {
		names[i] = strings.TrimSpace(names[i])
	}
	if len(names) == 0 || names[0] == "" {
		return nil
	}
	params := parseTranslateEnvParams()
	build := func(name string) (translate.Translator, string) {
		if name == "" || !translate.Exists(name) {
			return nil, ""
		}
		return translate.Build(name, params[strings.ToUpper(name)]), name
	}
	primary, pn := build(names[0])
	if primary == nil {
		return nil
	}
	var secondary translate.Translator
	sn := ""
	if len(names) > 1 {
		secondary, sn = build(names[1])
	}
	return translate.NewFailover(primary, pn, secondary, sn, 15*time.Second, 5)
}

// parseTranslateEnvParams mirrors cmd.parseTranslateEngineParams.
func parseTranslateEnvParams() map[string]map[string]string {
	result := map[string]map[string]string{}
	const prefix = "MT_TRANSLATE_"
	for _, env := range os.Environ() {
		key, value, found := strings.Cut(env, "=")
		if !found || !strings.HasPrefix(key, prefix) {
			continue
		}
		rest := strings.TrimPrefix(key, prefix)
		eng, param, found := strings.Cut(rest, "__")
		if !found || eng == "" || param == "" {
			continue
		}
		if result[eng] == nil {
			result[eng] = map[string]string{}
		}
		result[eng][param] = value
	}
	return result
}

func settingsFilePath() string {
	c := getConfig()
	if c == nil || c.ActorMappingPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(c.ActorMappingPath), "gui_settings.json")
}

// initSettings loads persisted settings once at startup (before the engine
// is wired; translator application happens in SetTranslateApp).
func initSettings() {
	settingsMu.Lock()
	settingsFile = settingsFilePath()
	settings = loadSettingsFile()
	settingsMu.Unlock()
	// Apply provider-level settings immediately (rate limits).
	applySettings(settings)
	log.Printf("[manage] settings loaded: fanza_limit=%q javdb_limit=%q translate=%v engines=%q",
		settings.FanzaRateLimit, settings.JavdbRateLimit, settings.Translate, settings.TranslateEngines)
}

func loadSettingsFile() Settings {
	var s Settings
	path := settingsFilePath()
	if path == "" {
		return s
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	_ = json.Unmarshal(data, &s)
	return s
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

// GetSettings returns the current settings for the GUI.
func GetSettings() Settings {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	return settings
}

// UpdateSettings validates, persists and applies new settings.
func UpdateSettings(s Settings) Settings {
	settingsMu.Lock()
	settings = s
	saveSettingsFile(s)
	app := settingsApp
	settingsMu.Unlock()
	applySettings(s)
	log.Printf("[manage] settings updated: fanza_limit=%q javdb_limit=%q translate=%v engines=%q summary_off=%v",
		s.FanzaRateLimit, s.JavdbRateLimit, s.Translate, s.TranslateEngines, s.DisableSummary)
	_ = app
	return s
}
