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

	// Metadata translation. Translation is owned by the backend: engines
	// and credentials are configured here; the Emby/Jellyfin plugin keeps
	// its own translation disabled.
	Translate        bool   `json:"translate"`
	TranslateEngines string `json:"translate_engines"` // primary only; secondary is always googlefree
	TranslateLang    string `json:"translate_lang"`
	// TranslateTimeout is the per-engine timeout in seconds (default 15).
	// When the primary exceeds it, the free Google engine takes over.
	TranslateTimeout int `json:"translate_timeout,omitempty"`

	// Translation engine credentials. Empty values fall back to the
	// MT_TRANSLATE_<ENGINE>__<PARAM> environment variables.
	BaiduAppID   string `json:"baidu_app_id,omitempty"`
	BaiduAppKey  string `json:"baidu_app_key,omitempty"`
	DeepLAPIKey  string `json:"deepl_api_key,omitempty"`
	DeepLAPIUrl  string `json:"deepl_api_url,omitempty"`
	OpenAiAPIKey string `json:"openai_api_key,omitempty"`
	OpenAiAPIUrl string `json:"openai_api_url,omitempty"`
	OpenAiModel  string `json:"openai_model,omitempty"`
	GoogleAPIKey string `json:"google_api_key,omitempty"`
	GoogleAPIUrl string `json:"google_api_url,omitempty"`

	// Summary switch.
	DisableSummary bool `json:"disable_summary"`
}

var (
	settingsMu   sync.Mutex
	settings     Settings
	settingsFile string
	// settingsFromFile is true once a persisted settings file was loaded (or
	// saved). Until then the startup/env configuration must win.
	settingsFromFile bool
)

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
	settingsMu.Lock()
	fromFile := settingsFromFile
	settingsMu.Unlock()
	if !fromFile {
		// Nothing persisted yet: the startup/env configuration wins. Applying
		// an empty Settings here would wipe the env-built translator.
		return
	}
	if d, ok := parseDurationOr(s.FanzaRateLimit); ok {
		throttle.Set(fanza.Name, d)
	}
	if d, ok := parseDurationOr(s.JavdbRateLimit); ok {
		throttle.Set(javdb.Name, d)
	}
	settingsMu.Lock()
	app := getPipeline()
	settingsMu.Unlock()
	if app == nil {
		return
	}
	if s.Translate && strings.TrimSpace(s.TranslateEngines) != "" {
		app.SetTranslator(buildFailoverTranslator(s.TranslateEngines, s))
	} else {
		app.SetTranslator(nil)
	}
	app.SetTranslateLang(s.TranslateLang)
	app.SetDisableSummary(s.DisableSummary)
}

// translateCredentialOverlay maps GUI credential fields onto the engine
// param keys (the JSON tags used by translate.Build). Empty GUI values are
// skipped so environment variables keep working as fallbacks.
func translateCredentialOverlay(s Settings) map[string]map[string]string {
	overlay := map[string]map[string]string{}
	set := func(engine, key, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		if overlay[engine] == nil {
			overlay[engine] = map[string]string{}
		}
		overlay[engine][key] = strings.TrimSpace(value)
	}
	set("BAIDU", "baidu-app-id", s.BaiduAppID)
	set("BAIDU", "baidu-app-key", s.BaiduAppKey)
	set("DEEPL", "deepl-api-key", s.DeepLAPIKey)
	set("DEEPL", "deepl-api-url", s.DeepLAPIUrl)
	set("OPENAI", "openai-api-key", s.OpenAiAPIKey)
	set("OPENAI", "openai-api-url", s.OpenAiAPIUrl)
	set("OPENAI", "openai-model", s.OpenAiModel)
	set("GOOGLE", "google-api-key", s.GoogleAPIKey)
	set("GOOGLE", "google-api-url", s.GoogleAPIUrl)
	return overlay
}

// buildFailoverTranslator creates a failover translator from a CSV engine
// list. Engine params are read from the environment
// (MT_TRANSLATE_<ENGINE>__<PARAM>), the same source cmd uses at startup.
func buildFailoverTranslator(enginesCSV string, s Settings) translate.Translator {
	names := strings.Split(enginesCSV, ",")
	for i := range names {
		names[i] = strings.TrimSpace(names[i])
	}
	if len(names) == 0 || names[0] == "" {
		return nil
	}
	params := parseTranslateEnvParams()
	// GUI-configured credentials take precedence over env vars.
	for eng, kv := range translateCredentialOverlay(s) {
		if params[eng] == nil {
			params[eng] = map[string]string{}
		}
		for k, v := range kv {
			params[eng][k] = v
		}
	}
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
	if !strings.EqualFold(pn, "googlefree") {
		secondary, sn = build("googlefree")
	}
	timeout := 15 * time.Second
	if s.TranslateTimeout > 0 {
		timeout = time.Duration(s.TranslateTimeout) * time.Second
	}
	log.Printf("[manage] translator: primary=%s secondary=%s timeout=%s", pn, sn, timeout)
	return translate.NewFailover(primary, pn, secondary, sn, timeout, 5)
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
// is wired; translator application happens in SetPipeline).
func initSettings() {
	settingsMu.Lock()
	settingsFile = settingsFilePath()
	s, loaded := loadSettingsFileWithStatus()
	settings = s
	settingsFromFile = loaded
	settingsMu.Unlock()
	// Apply provider-level settings immediately (rate limits).
	applySettings(settings)
	log.Printf("[manage] settings loaded: file=%v fanza_limit=%q javdb_limit=%q translate=%v engines=%q",
		loaded, settings.FanzaRateLimit, settings.JavdbRateLimit, settings.Translate, settings.TranslateEngines)
}

// loadSettingsFileWithStatus reports whether a settings file actually existed,
// so callers can distinguish "no settings yet" from "settings say: off".
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

func loadSettingsFile() Settings {
	s, _ := loadSettingsFileWithStatus()
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
	settingsMu.Unlock()
	applySettings(s)
	log.Printf("[manage] settings updated: fanza_limit=%q javdb_limit=%q translate=%v engines=%q summary_off=%v",
		s.FanzaRateLimit, s.JavdbRateLimit, s.Translate, s.TranslateEngines, s.DisableSummary)
	return s
}
