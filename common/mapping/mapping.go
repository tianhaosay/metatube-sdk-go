// Package mapping loads MDC-style XML mapping tables (mapping_actor.xml /
// mapping_info.xml) and exposes name lookup helpers.
//
// File format:
//
//	<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
//	<actor>
//	  <a zh_cn="AIKA" zh_tw="AIKA" jp="AIKA" keyword=",AIKA,優木あいか,..." href="https://javdb.com/actors/z0GW"/>
//	  ...
//	</actor>
//	<info>
//	  <a zh_cn="删除" zh_tw="删除" jp="删除" keyword=",中文,中文字幕,字幕,"/>
//	  <a zh_cn="3D卡通" zh_tw="3D卡通" jp="3Dエロアニメ" keyword=",3D卡通,3Dエロアニメ,"/>
//	  ...
//	</info>
//
// For actor tables: keyword holds the alias list (each name enclosed by
// commas); zh_cn/zh_tw/jp hold the canonical output name per language; href
// holds an optional actor homepage (usually the JavDB actor page).
//
// For info (tag) tables: keyword holds the alias list; zh_cn holds the
// canonical output word. The reserved value "删除" (DeleteMark) means the
// matched word should be removed from the output.
//
// Files are re-loaded automatically when their mtime changes, so users can
// edit the mapping files in place without restarting the server.
package mapping

import (
	"encoding/xml"
	"errors"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// DeleteMark is the reserved zh_cn output value indicating that the matched
// keyword should be deleted from the output list.
const DeleteMark = "删除"

type xmlEntry struct {
	ZhCn      string `xml:"zh_cn,attr"`
	ZhTw      string `xml:"zh_tw,attr"`
	Jp        string `xml:"jp,attr"`
	Keyword   string `xml:"keyword,attr"`
	Href      string `xml:"href,attr"`
	TmdbID    string `xml:"tmdb_id,attr"`
	BioGraphy string `xml:"bio_graphy,attr"`
	Verified  string `xml:"verified,attr"`
}

func (e xmlEntry) aliases() []string {
	var out []string
	for _, k := range strings.Split(e.Keyword, ",") {
		if k = strings.TrimSpace(k); k != "" {
			out = append(out, k)
		}
	}
	return out
}

// ActorEntry is one canonical actor record.
type ActorEntry struct {
	Name      string   // zh_cn canonical name.
	Jp        string   // jp canonical name (may be empty).
	ZhTw      string   // zh_tw canonical name (may be empty).
	Href      string   // actor homepage, e.g. javdb actor URL (may be empty).
	TmdbID    string   // TMDB person id (avdb online table, may be empty).
	BioGraphy string   // actor biography (avdb online table, may be empty).
	Aliases   []string // raw keyword list.
}

// InfoEntry is one canonical tag/word record.
type InfoEntry struct {
	Name    string   // zh_cn canonical word (may be DeleteMark).
	Jp      string   // jp canonical word (may be empty).
	ZhTw    string   // zh_tw canonical name (may be empty).
	Aliases []string // raw keyword list.
}

func (e *InfoEntry) IsDelete() bool { return e.Name == DeleteMark }

// baseMap holds the common parsing / auto-reload machinery. It is safe for
// concurrent use. Type-specific conversion is injected via the build and
// keys function fields.
type baseMap[T any] struct {
	mu       sync.RWMutex
	path     string
	mtime    time.Time
	entries  map[string]*T // alias (and canonical names) -> entry.
	build    func(*xmlEntry) (*T, bool)
	keys     func(*T) []string
	aliases  func(*T) []string
	minEntry int // minimum entry count sanity check (0 disables).
	nEntry   int
	nAlias   int
}

var ErrTooFewEntries = errors.New("mapping: too few entries, file rejected")

// load parses the file and swaps the internal map on success. On parse
// errors or sanity-check failures the old map is kept intact.
func (m *baseMap[T]) load() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.New("mapping: parse panic")
		}
	}()
	content, err := os.ReadFile(m.path)
	if err != nil {
		return err
	}
	// Both root layouts are supported:
	//   mdc style:      <actor><a .../></actor>
	//   avdb online:    <actor-mapping><actor><a .../></actor></actor-mapping>
	var root struct {
		A      []xmlEntry `xml:"a"`
		Actors []struct {
			A []xmlEntry `xml:"a"`
		} `xml:"actor"`
	}
	if err = xml.Unmarshal(content, &root); err != nil {
		return err
	}
	entries := make(map[string]*T, len(root.A)*2)
	nAlias := 0
	var all []xmlEntry
	all = append(all, root.A...)
	for _, actor := range root.Actors {
		all = append(all, actor.A...)
	}
	for i := range all {
		e := all[i]
		item, ok := m.build(&e)
		if !ok {
			continue
		}
		// Alias list + canonical names are all registered as lookup keys.
		for _, alias := range m.aliases(item) {
			if _, exists := entries[alias]; !exists {
				entries[alias] = item
				nAlias++
			}
		}
		for _, k := range m.keys(item) {
			if k != "" {
				if _, exists := entries[k]; !exists {
					entries[k] = item
				}
			}
		}
	}
	if m.minEntry > 0 && len(all) < m.minEntry {
		return ErrTooFewEntries
	}
	// Anti-truncation guard: refuse to swap to a much smaller table.
	m.mu.RLock()
	old := m.nEntry
	m.mu.RUnlock()
	if old > 0 && len(all) < old/2 {
		return ErrTooFewEntries
	}
	m.mu.Lock()
	m.entries = entries
	m.nEntry = len(all)
	m.nAlias = nAlias
	m.mu.Unlock()
	log.Printf("[mapping] loaded %s: %d entries, %d aliases", m.path, len(all), nAlias)
	return nil
}

// reloadIfChanged stats the file and reloads on mtime change.
func (m *baseMap[T]) reloadIfChanged() {
	st, err := os.Stat(m.path)
	if err != nil {
		return // keep current map.
	}
	m.mu.RLock()
	same := st.ModTime().Equal(m.mtime)
	m.mu.RUnlock()
	if same {
		return
	}
	if err := m.load(); err != nil {
		log.Printf("[mapping] reload %s failed: %v", m.path, err)
		return
	}
	m.mu.Lock()
	m.mtime = st.ModTime()
	m.mu.Unlock()
}

func (m *baseMap[T]) lookup(name string) (*T, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, false
	}
	m.reloadIfChanged()
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.entries[name]
	return e, ok
}

// ActorMap is a loaded mapping_actor.xml.
type ActorMap struct {
	base baseMap[ActorEntry]
}

func (m *ActorMap) build(e *xmlEntry) (*ActorEntry, bool) {
	name := strings.TrimSpace(e.ZhCn)
	if name == "" {
		return nil, false
	}
	return &ActorEntry{
		Name:      name,
		Jp:        strings.TrimSpace(e.Jp),
		ZhTw:      strings.TrimSpace(e.ZhTw),
		Href:      strings.TrimSpace(e.Href),
		TmdbID:    strings.TrimSpace(e.TmdbID),
		BioGraphy: strings.TrimSpace(e.BioGraphy),
		Aliases:   e.aliases(),
	}, true
}

func (m *ActorMap) keys(e *ActorEntry) []string {
	return []string{e.Name, e.Jp, e.ZhTw}
}

// LoadActorFile parses the given mapping_actor.xml path.
func LoadActorFile(path string) (*ActorMap, error) {
	m := &ActorMap{}
	m.base.path = path
	m.base.minEntry = 100
	m.base.build = m.build
	m.base.keys = m.keys
	m.base.aliases = func(e *ActorEntry) []string { return e.Aliases }
	if err := m.base.load(); err != nil {
		return nil, err
	}
	if st, err := os.Stat(path); err == nil {
		m.base.mtime = st.ModTime()
	}
	return m, nil
}

// Lookup returns the canonical actor entry for any of its aliases.
func (m *ActorMap) Lookup(name string) (*ActorEntry, bool) {
	if e, ok := m.base.lookup(name); ok {
		return e, true
	}
	// Providers often append aliases in parentheses
	// ("みひな （あずみひな、永井みひな）", "真野祈（真北祈）"); retry without them.
	if trimmed := stripTrailingAliases(name); trimmed != name {
		if e, ok := m.base.lookup(trimmed); ok {
			return e, true
		}
	}
	return nil, false
}

// stripTrailingAliases removes trailing "（…）" / "(…)" alias groups.
func stripTrailingAliases(name string) string {
	s := strings.TrimSpace(name)
	for {
		changed := false
		for _, pair := range [][2]string{{"（", "）"}, {"(", ")"}} {
			if i := strings.LastIndex(s, pair[0]); i > 0 && strings.HasSuffix(s, pair[1]) {
				s = strings.TrimSpace(s[:i])
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return s
}

// Len returns the number of entries in the table.
func (m *ActorMap) Len() int {
	m.base.mu.RLock()
	defer m.base.mu.RUnlock()
	return m.base.nEntry
}

// InfoMap is a loaded mapping_info.xml.
type InfoMap struct {
	base baseMap[InfoEntry]
}

func (m *InfoMap) build(e *xmlEntry) (*InfoEntry, bool) {
	// Delete entries carry no zh_cn output other than DeleteMark, which is
	// fine. Require at least one alias to be useful.
	name := strings.TrimSpace(e.ZhCn)
	if name == "" || (len(e.aliases()) == 0) {
		return nil, false
	}
	return &InfoEntry{
		Name:    name,
		Jp:      strings.TrimSpace(e.Jp),
		ZhTw:    strings.TrimSpace(e.ZhTw),
		Aliases: e.aliases(),
	}, true
}

func (m *InfoMap) keys(e *InfoEntry) []string {
	return []string{e.Name, e.Jp, e.ZhTw}
}

// LoadInfoFile parses the given mapping_info.xml path.
func LoadInfoFile(path string) (*InfoMap, error) {
	m := &InfoMap{}
	m.base.path = path
	m.base.minEntry = 10
	m.base.build = m.build
	m.base.keys = m.keys
	m.base.aliases = func(e *InfoEntry) []string { return e.Aliases }
	if err := m.base.load(); err != nil {
		return nil, err
	}
	if st, err := os.Stat(path); err == nil {
		m.base.mtime = st.ModTime()
	}
	return m, nil
}

// Lookup returns the canonical info entry for any of its aliases. Callers
// should check IsDelete() on the result.
func (m *InfoMap) Lookup(word string) (*InfoEntry, bool) {
	return m.base.lookup(word)
}

// Len returns the number of entries in the table.
func (m *InfoMap) Len() int {
	m.base.mu.RLock()
	defer m.base.mu.RUnlock()
	return m.base.nEntry
}
