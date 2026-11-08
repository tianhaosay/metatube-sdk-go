package manage

import (
	"encoding/json"
	"os"
)

// The Emby MetaTube plugin exposes no readable configuration API on 4.10, so
// the backend reads the plugin's own JSON file through a read-only mount.
//
// Everything the backend mirrors lives in pluginSettingsConfig (see
// plugin_translate.go); this file holds the plugin-specific helpers that are
// not about translation or runtime toggles.

// pluginMeta is the minimal subset needed for non-translation decisions.
type pluginMeta struct {
	Server           string `json:"Server"`
	Token            string `json:"Token"`
	EnableAutoUpdate bool   `json:"EnableAutoUpdate"`
}

// readPluginMeta loads MetaTube.json and reports whether it was available.
func readPluginMeta() (pluginMeta, bool) {
	var m pluginMeta
	path := pluginConfigPath()
	if path == "" {
		return m, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return m, false
	}
	if json.Unmarshal(data, &m) != nil {
		return m, false
	}
	return m, true
}

// PluginAutoUpdate reports whether the Emby plugin refreshes metadata on its
// own when an item changes. Wiping is useless while it is on.
func PluginAutoUpdate() bool {
	m, ok := readPluginMeta()
	if !ok {
		return false
	}
	return m.EnableAutoUpdate
}
