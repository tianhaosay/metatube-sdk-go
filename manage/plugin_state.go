package manage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// pluginState reports what the Emby MetaTube plugin is configured to do, so the
// console can show the linkage between the plugin settings page (the single
// place where translation is configured) and the backend.
//
// The plugin exposes no readable configuration API on Emby 4.10, so this reads
// the plugin's JSON file from a read-only mount instead.
type pluginState struct {
	Found             bool     `json:"found"`
	Path              string   `json:"path,omitempty"`
	Server            string   `json:"server,omitempty"`
	TokenPresent      bool     `json:"token_present"`
	TranslationMode   string   `json:"translation_mode,omitempty"`
	TranslationEngine string   `json:"translation_engine,omitempty"`
	Warnings          []string `json:"warnings,omitempty"`
}

// pluginConfig is the subset of MetaTube.json the console cares about.
type pluginConfig struct {
	Server            string `json:"Server"`
	EnableAutoUpdate  bool   `json:"EnableAutoUpdate"`
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

	// The backend reads the plugin's engine/key and does the translation;
	// the plugin's own TranslationMode must stay Disabled to avoid double work.
	ti := GetPluginTranslationInfo()
	if enabled, _ := ti["enabled"].(bool); !enabled {
		out.Warnings = append(out.Warnings,
			"插件里没有可用的翻译引擎/密钥：后端翻译关闭。请到 Emby → 插件 → MetaTube 设置翻译引擎与密钥")
	}
	if !strings.EqualFold(pc.TranslationMode, "Disabled") && strings.TrimSpace(pc.TranslationMode) != "" {
		out.Warnings = append(out.Warnings,
			"插件翻译模式为 "+pc.TranslationMode+"：后端已在翻译，会重复。建议改回 Disabled")
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

// GetPluginTranslationInfo exposes the read-only translation linkage view.
func GetPluginTranslationInfo() map[string]any {
	return pluginTranslationInfo()
}

// PluginAutoUpdate reports whether the Emby plugin refreshes metadata on its
// own when an item changes. Wiping is useless while it is on.
func PluginAutoUpdate() bool {
	c := getConfig()
	if c == nil || c.EmbyPluginDir == "" {
		return false
	}
	path := filepath.Join(c.EmbyPluginDir, "MetaTube.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var cfg pluginConfig
	if json.Unmarshal(data, &cfg) != nil {
		return false
	}
	return cfg.EnableAutoUpdate
}
