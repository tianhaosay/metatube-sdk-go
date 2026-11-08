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

// The translation engine and its credentials are configured once, in the
// native MetaTube plugin settings page (Emby → Plugins → MetaTube). The
// backend reads that same MetaTube.json through the read-only mount and
// builds its translator from it. The plugin's own TranslationMode stays
// Disabled so a piece of text is never translated twice.
//
// This is the "one place to configure" linkage: change the engine/key in the
// plugin page and the backend picks it up without a restart.

// pluginSettingsConfig is the complete subset of MetaTube.json that the
// backend mirrors: connection info, translation engine/credentials and the
// four runtime toggles. All of them are configured in the native plugin
// settings page, which makes that page the single source of truth.
type pluginSettingsConfig struct {
	// Connection.
	Server string `json:"Server"`
	Token  string `json:"Token"`

	// Translation.
	TranslationMode   string `json:"TranslationMode"`
	TranslationEngine string `json:"TranslationEngine"`
	BaiduAppId        string `json:"BaiduAppId"`
	BaiduAppKey       string `json:"BaiduAppKey"`
	GoogleApiKey      string `json:"GoogleApiKey"`
	GoogleApiUrl      string `json:"GoogleApiUrl"`
	DeepLApiKey       string `json:"DeepLApiKey"`
	DeepLApiUrl       string `json:"DeepLApiUrl"`
	OpenAiApiKey      string `json:"OpenAiApiKey"`
	OpenAiApiUrl      string `json:"OpenAiApiUrl"`
	OpenAiModel       string `json:"OpenAiModel"`

	// Runtime toggles (rendered as checkboxes / inputs in the plugin page).
	DisableSummary      bool   `json:"DisableSummary"`
	EnableActorFallback bool   `json:"EnableActorFallback"`
	FanzaRateLimit      string `json:"FanzaRateLimit"`
	JavdbRateLimit      string `json:"JavdbRateLimit"`
}

// pluginTranslationConfig is the alias used by the translation-only helpers
// below; it is the same struct so there is exactly one JSON shape.
type pluginTranslationConfig = pluginSettingsConfig

// GetPluginTranslationInfo exposes the read-only translation linkage view: the
// engine and credentials come from the native plugin settings page.
func GetPluginTranslationInfo() map[string]any {
	return pluginTranslationInfo()
}

func pluginConfigPath() string {
	c := getConfig()
	if c == nil || c.EmbyPluginDir == "" {
		return ""
	}
	return filepath.Join(c.EmbyPluginDir, "MetaTube.json")
}

func readPluginTranslationConfig() (pluginSettingsConfig, bool) {
	path := pluginConfigPath()
	if path == "" {
		return pluginSettingsConfig{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return pluginSettingsConfig{}, false
	}
	var pc pluginSettingsConfig
	if json.Unmarshal(data, &pc) != nil {
		return pluginSettingsConfig{}, false
	}
	return pc, true
}

// pluginEngineName maps the plugin's TranslationEngine enum name onto the
// backend translator registry name.
func pluginEngineName(e string) string {
	switch strings.ToLower(strings.TrimSpace(e)) {
	case "baidu":
		return "baidu"
	case "google":
		return "google"
	case "googlefree", "google (free)":
		return "googlefree"
	case "deepl":
		return "deepl"
	case "openai":
		return "openai"
	default:
		return ""
	}
}

// pluginEngineParams maps the plugin's credential fields onto the backend
// engine's json-tagged params.
func pluginEngineParams(engine string, pc pluginTranslationConfig) map[string]string {
	p := map[string]string{}
	put := func(k, v string) {
		if strings.TrimSpace(v) != "" {
			p[k] = strings.TrimSpace(v)
		}
	}
	switch engine {
	case "baidu":
		put("baidu-app-id", pc.BaiduAppId)
		put("baidu-app-key", pc.BaiduAppKey)
	case "google":
		put("google-api-key", pc.GoogleApiKey)
		put("google-api-url", pc.GoogleApiUrl)
	case "deepl":
		put("deepl-api-key", pc.DeepLApiKey)
		put("deepl-api-url", pc.DeepLApiUrl)
	case "openai":
		put("openai-api-key", pc.OpenAiApiKey)
		put("openai-api-url", pc.OpenAiApiUrl)
		put("openai-model", pc.OpenAiModel)
	}
	return p
}

// pluginTranslationInfo is what the console shows (read-only).
func pluginTranslationInfo() map[string]any {
	pc, ok := readPluginTranslationConfig()
	if !ok {
		return map[string]any{
			"found":   false,
			"reason":  "未读取到插件配置（检查 MT_EMBY_PLUGIN_DIR 挂载）",
			"enabled": false,
		}
	}
	engine := pluginEngineName(pc.TranslationEngine)
	mode := pc.TranslationMode
	keySet := false
	switch engine {
	case "baidu":
		keySet = pc.BaiduAppId != "" && pc.BaiduAppKey != ""
	case "google":
		keySet = pc.GoogleApiKey != ""
	case "deepl":
		keySet = pc.DeepLApiKey != ""
	case "openai":
		keySet = pc.OpenAiApiKey != ""
	case "googlefree":
		keySet = true
	}
	return map[string]any{
		"found":              true,
		"engine":             pc.TranslationEngine,
		"backend_engine":     engine,
		"mode":               mode,
		"key_present":        keySet,
		"enabled":            engine != "" && keySet,
		"backend_translates": true,
		"plugin_mode_advice": "Disabled",
	}
}

// genreTranslator builds a translator used ONLY for Japanese genre/tag
// normalization, backed by the persistent genre dictionary.
//
// Title and summary translation is deliberately not done by the backend: the
// Emby plugin owns that (upstream metatube only exposes /v1/translate). This
// keeps a single translator for free text and guarantees a title is translated
// exactly once.
//
// The primary engine is the one configured in the plugin page; the free Google
// engine is the fallback when the primary fails, times out or returns empty.
func genreTranslator(pc pluginTranslationConfig) translate.Translator {
	engine := pluginEngineName(pc.TranslationEngine)
	if engine == "" || !translate.Exists(engine) {
		return nil
	}
	primary := translate.Build(engine, pluginEngineParams(engine, pc))
	if engine == "googlefree" {
		return primary
	}
	secondary := translate.Build("googlefree", nil)
	return translate.NewGenreFailover(primary, secondary)
}

var (
	pluginTransMu     sync.Mutex
	pluginTransSig    string
	pluginTransEngine string
)

func pluginTranslationSignature(pc pluginTranslationConfig) string {
	return strings.Join([]string{
		pc.TranslationMode, pc.TranslationEngine,
		pc.BaiduAppId, pc.BaiduAppKey, pc.GoogleApiKey, pc.GoogleApiUrl,
		pc.DeepLApiKey, pc.DeepLApiUrl, pc.OpenAiApiKey, pc.OpenAiApiUrl, pc.OpenAiModel,
	}, "|")
}

// applyPluginTranslation rebuilds the genre translator from the plugin config.
// Safe to call repeatedly (startup + hot reload).
func applyPluginTranslation() {
	app := getPipeline()
	if app == nil {
		return
	}
	pc, ok := readPluginTranslationConfig()
	if !ok {
		return
	}
	sig := pluginTranslationSignature(pc)
	pluginTransMu.Lock()
	if sig == pluginTransSig {
		pluginTransMu.Unlock()
		return
	}
	pluginTransSig = sig
	pluginTransMu.Unlock()

	t := genreTranslator(pc)
	engine := pluginEngineName(pc.TranslationEngine)
	app.SetTranslator(t)
	pluginTransMu.Lock()
	pluginTransEngine = engine
	pluginTransMu.Unlock()
	if t == nil {
		log.Printf("[manage] genre translation: no usable engine, dictionary-only")
	} else if engine == "googlefree" {
		log.Printf("[manage] genre translation: engine=googlefree (title/summary translated by the Emby plugin)")
	} else {
		log.Printf("[manage] genre translation: engine=%s primary, googlefree fallback (title/summary translated by the Emby plugin)", engine)
	}
}

// applyPluginSettings applies the plugin-mirrored runtime toggles to the live
// pipeline/providers. The native plugin settings page is the single source of
// truth for all of them.
func applyPluginSettings() {
	app := getPipeline()
	if app == nil {
		return
	}
	pc, ok := readPluginTranslationConfig()
	if !ok {
		return
	}
	app.SetDisableSummary(pc.DisableSummary)
	app.SetActorFallbackEnabled(pc.EnableActorFallback)
	if d, ok := parseDurationOr(pc.FanzaRateLimit); ok {
		throttle.Set(fanza.Name, d)
	}
	if d, ok := parseDurationOr(pc.JavdbRateLimit); ok {
		throttle.Set(javdb.Name, d)
	}
	settingsMu.Lock()
	settings = Settings{
		DisableSummary: pc.DisableSummary,
		ActorFallback:  pc.EnableActorFallback,
		FanzaRateLimit: pc.FanzaRateLimit,
		JavdbRateLimit: pc.JavdbRateLimit,
	}
	settingsMu.Unlock()
}

// StartPluginSettingsWatch applies all plugin-driven settings once immediately
// and then keeps hot-reloading them. Called from cmd.Router.
func StartPluginSettingsWatch() {
	applyPluginTranslation()
	applyPluginSettings()
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			applyPluginTranslation()
			applyPluginSettings()
		}
	}()
}
