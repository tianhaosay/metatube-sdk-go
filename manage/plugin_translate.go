package manage

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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

// pluginTranslationConfig is the translation-related subset of MetaTube.json.
type pluginTranslationConfig struct {
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
}

func pluginConfigPath() string {
	c := getConfig()
	if c == nil || c.EmbyPluginDir == "" {
		return ""
	}
	return filepath.Join(c.EmbyPluginDir, "MetaTube.json")
}

func readPluginTranslationConfig() (pluginTranslationConfig, bool) {
	path := pluginConfigPath()
	if path == "" {
		return pluginTranslationConfig{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return pluginTranslationConfig{}, false
	}
	var pc pluginTranslationConfig
	if json.Unmarshal(data, &pc) != nil {
		return pluginTranslationConfig{}, false
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
		"found":               true,
		"engine":              pc.TranslationEngine,
		"backend_engine":      engine,
		"mode":                mode,
		"key_present":         keySet,
		"enabled":             engine != "" && keySet,
		"backend_translates":  true,
		"plugin_mode_advice":  "Disabled",
	}
}

// buildTranslatorFromPlugin creates the runtime translator from the plugin's
// settings. The free Google engine is always the secondary fallback.
func buildTranslatorFromPlugin(pc pluginTranslationConfig) (translate.Translator, string) {
	engine := pluginEngineName(pc.TranslationEngine)
	if engine == "" || !translate.Exists(engine) {
		return nil, ""
	}
	primary := translate.Build(engine, pluginEngineParams(engine, pc))
	var secondary translate.Translator
	secondaryName := ""
	if engine != "googlefree" {
		secondary = translate.Build("googlefree", nil)
		secondaryName = "googlefree"
	}
	return translate.NewFailover(primary, engine, secondary, secondaryName, 15*time.Second, 5), engine
}

var (
	pluginTransMu       sync.Mutex
	pluginTransSig      string
	pluginTransEngine   string
	pluginTransConfigOK bool
)

func pluginTranslationSignature(pc pluginTranslationConfig) string {
	return strings.Join([]string{
		pc.TranslationMode, pc.TranslationEngine,
		pc.BaiduAppId, pc.BaiduAppKey, pc.GoogleApiKey, pc.GoogleApiUrl,
		pc.DeepLApiKey, pc.DeepLApiUrl, pc.OpenAiApiKey, pc.OpenAiApiUrl, pc.OpenAiModel,
	}, "|")
}

// applyPluginTranslation rebuilds the pipeline translator from the plugin
// config. It is safe to call repeatedly (startup + hot reload + settings save).
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

	t, engine := buildTranslatorFromPlugin(pc)
	app.SetTranslator(t)
	pluginTransMu.Lock()
	pluginTransEngine = engine
	pluginTransConfigOK = ok && engine != ""
	pluginTransMu.Unlock()
	if engine == "" {
		log.Printf("[manage] translation: no usable engine in plugin config, backend translation off")
	} else {
		log.Printf("[manage] translation: engine=%s (from Emby plugin config), secondary=googlefree, plugin_mode=%s", engine, pc.TranslationMode)
	}
}

// StartPluginTranslationWatch applies the plugin translation config once
// immediately and then keeps hot-reloading it. Called from cmd.Router after
// the pipeline has been attached.
func StartPluginTranslationWatch() {
	applyPluginTranslation()
	watchPluginTranslation()
}

// watchPluginTranslation polls MetaTube.json so changes made in the plugin
// settings page take effect without restarting the backend.
func watchPluginTranslation() {
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			applyPluginTranslation()
		}
	}()
}
