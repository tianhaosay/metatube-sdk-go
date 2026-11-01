package manage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// pluginState reports what the Emby MetaTube plugin is configured to do, so the
// console can warn about duplicated work (e.g. the plugin translating too).
//
// The plugin exposes no readable configuration API on Emby 4.10 (the endpoint
// answers 500 for simple-UI plugins), so this reads the plugin's JSON file from
// a read-only mount instead. When the directory is not mounted the endpoint
// simply reports found=false.
type pluginState struct {
	Found            bool     `json:"found"`
	Path             string   `json:"path,omitempty"`
	Server           string   `json:"server,omitempty"`
	TokenPresent     bool     `json:"token_present"`
	TranslationMode  string   `json:"translation_mode,omitempty"`
	TranslationEngine string  `json:"translation_engine,omitempty"`
	Warnings         []string `json:"warnings,omitempty"`
}

// pluginConfig is the subset of MetaTube.json the console cares about.
type pluginConfig struct {
	Server            string `json:"Server"`
	Token             string `json:"Token"`
	TranslationMode   string `json:"TranslationMode"`
	TranslationEngine string `json:"TranslationEngine"`
}

// GetPluginState reads the plugin configuration file when available.
func GetPluginState() map[string]any {
	c := getConfig()
	out := &pluginState{}
	dir := ""
	if c != nil {
		dir = c.EmbyPluginDir
	}
	if dir == "" {
		return map[string]any{"found": false, "reason": "未挂载 Emby 插件目录（MT_EMBY_PLUGIN_DIR）"}
	}
	path := filepath.Join(dir, "MetaTube.json")
	data, err := os.ReadFile(path)
	if err != nil {
		out.Path = path
		return map[string]any{"found": false, "path": path, "reason": "读取失败: " + err.Error()}
	}
	var pc pluginConfig
	if err := json.Unmarshal(data, &pc); err != nil {
		return map[string]any{"found": false, "path": path, "reason": "解析失败: " + err.Error()}
	}
	out.Found = true
	out.Path = path
	out.Server = pc.Server
	out.TokenPresent = strings.TrimSpace(pc.Token) != ""
	out.TranslationMode = pc.TranslationMode
	out.TranslationEngine = pc.TranslationEngine

	if !strings.EqualFold(pc.TranslationMode, "Disabled") && strings.TrimSpace(pc.TranslationMode) != "" {
		out.Warnings = append(out.Warnings,
			"插件翻译模式为 "+pc.TranslationMode+"：会与后端翻译重复，建议改为 Disabled")
	}
	if s := GetSettings(); s.Translate && pc.TranslationMode != "Disabled" && pc.TranslationMode != "" {
		out.Warnings = append(out.Warnings,
			"后端已开启翻译，插件也在翻译：同一段文字会被翻两次")
	}
	if pc.Server != "" && !strings.Contains(pc.Server, "8080") {
		out.Warnings = append(out.Warnings, "插件指向的后端地址不是 8080："+pc.Server)
	}
	return map[string]any{
		"found":              out.Found,
		"path":               out.Path,
		"server":             out.Server,
		"token_present":      out.TokenPresent,
		"translation_mode":   out.TranslationMode,
		"translation_engine": out.TranslationEngine,
		"warnings":           out.Warnings,
	}
}
