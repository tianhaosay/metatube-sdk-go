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

// The translation engine and its credentials are configured once, in the native
// MetaTube plugin settings page. The backend reads the same MetaTube.json
// through a read-only mount and builds its translator from it.
//
// Titles and summaries are translated by the Emby plugin, whose TranslationMode
// must therefore not be Disabled. The backend translator only normalizes genres,
// so no text is translated twice.
//
// Changing the engine or key in the plugin page is picked up without a restart.

// pluginSettingsConfig is the complete subset of MetaTube.json that the backend
// mirrors: connection info, translation engine and credentials, and the four
// runtime toggles. The plugin settings page is their single source of truth.
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

	// Runtime toggles, shown as checkboxes / inputs in the plugin page.
	DisableSummary      bool   `json:"DisableSummary"`
	EnableActorFallback bool   `json:"EnableActorFallback"`
	FanzaRateLimit      string `json:"FanzaRateLimit"`
	JavdbRateLimit      string `json:"JavdbRateLimit"`
}

// GetPluginTranslationInfo exposes the read-only translation linkage view; the
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
func pluginEngineParams(engine string, pc pluginSettingsConfig) map[string]string {
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
		"found":          true,
		"engine":         pc.TranslationEngine,
		"backend_engine": engine,
		"mode":           mode,
		"key_present":    keySet,
		"enabled":        engine != "" && keySet,
	}
}

// genreTranslator builds the translator used only for Japanese genre and tag
// normalization; titles and summaries are translated by the Emby plugin. The
// configured engine is primary, with free Google as fallback on failure, timeout
// or an empty result.
func genreTranslator(pc pluginSettingsConfig) translate.Translator {
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
	pluginTransMu sync.Mutex
	// pluginTransSig is the config signature the current translator was built
	// from; it skips rebuilds when nothing changed.
	pluginTransSig string
)

func pluginTranslationSignature(pc pluginSettingsConfig) string {
	return strings.Join([]string{
		pc.TranslationMode, pc.TranslationEngine,
		pc.BaiduAppId, pc.BaiduAppKey, pc.GoogleApiKey, pc.GoogleApiUrl,
		pc.DeepLApiKey, pc.DeepLApiUrl, pc.OpenAiApiKey, pc.OpenAiApiUrl, pc.OpenAiModel,
	}, "|")
}

// applyPluginTranslation rebuilds the genre translator from the plugin config.
// Safe to call repeatedly (startup and hot reload).
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
	app.SetTranslator(t, engine)
	if t == nil {
		log.Printf("[manage] genre translation: no usable engine, dictionary-only")
	} else if engine == "googlefree" {
		log.Printf("[manage] genre translation: engine=googlefree (title/summary translated by the Emby plugin)")
	} else {
		log.Printf("[manage] genre translation: engine=%s primary, googlefree fallback (title/summary translated by the Emby plugin)", engine)
	}
}

// applyPluginSettings applies the plugin-mirrored runtime toggles to the live
// pipeline and providers. The plugin settings page is the only source for them.
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

// StartPluginSettingsWatch applies all plugin-driven settings once, then polls
// MetaTube.json every 15s for changes. Called from cmd.Router.
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
