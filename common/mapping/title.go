package mapping

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

var errTitleParse = errors.New("mapping: title file parse error")

// TitleMap is a number -> title mapping loaded from an MDC-style JSON file
// (c_number.json, the cached Sehuatang Chinese titles):
//
//	{
//	  "001HMNF-071": "世界弹丸 我们的性交和胸部 NONOKA",
//	  ...
//	}
//
// Keys are matched case-insensitively after removing dashes, underscores and
// spaces, so "FLAV-359", "flav359" and "FLAV_359" all match. The file is
// reloaded automatically when its mtime changes.
type TitleMap struct {
	mu      sync.RWMutex
	path    string
	mtime   time.Time
	titles  map[string]string // normalized number -> title
	nEntry  int
}

func normalizeNumberKey(s string) string {
	s = strings.ToUpper(s)
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '-', '_', ' ':
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// LoadTitleFile parses the given c_number.json path.
func LoadTitleFile(path string) (*TitleMap, error) {
	m := &TitleMap{path: path}
	if err := m.load(); err != nil {
		return nil, err
	}
	if st, err := os.Stat(path); err == nil {
		m.mtime = st.ModTime()
	}
	return m, nil
}

func (m *TitleMap) load() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errTitleParse
		}
	}()
	content, err := os.ReadFile(m.path)
	if err != nil {
		return err
	}
	var raw map[string]string
	if err = json.Unmarshal(content, &raw); err != nil {
		return err
	}
	titles := make(map[string]string, len(raw))
	for number, title := range raw {
		number = strings.TrimSpace(number)
		title = strings.TrimSpace(title)
		if number == "" || title == "" {
			continue
		}
		titles[normalizeNumberKey(number)] = title
	}
	// Anti-truncation guard: refuse to swap to a much smaller table.
	m.mu.RLock()
	old := m.nEntry
	m.mu.RUnlock()
	if old > 0 && len(raw) < old/2 {
		return ErrTooFewEntries
	}
	m.mu.Lock()
	m.titles = titles
	m.nEntry = len(raw)
	m.mu.Unlock()
	log.Printf("[mapping] loaded %s: %d titles", m.path, len(raw))
	return nil
}

func (m *TitleMap) reloadIfChanged() {
	st, err := os.Stat(m.path)
	if err != nil {
		return
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

// Lookup returns the mapped title for the given movie number.
func (m *TitleMap) Lookup(number string) (string, bool) {
	number = strings.TrimSpace(number)
	if number == "" {
		return "", false
	}
	m.reloadIfChanged()
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.titles[normalizeNumberKey(number)]
	return t, ok
}

// Len returns the number of entries in the table.
func (m *TitleMap) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.nEntry
}
