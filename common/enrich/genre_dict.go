package enrich

import (
	"encoding/json"
	"log"
	"os"
	"sync"
	"time"
)

// GenreDict is a persistent Japanese -> Chinese tag dictionary.
//
// DMM's tag vocabulary is a few hundred words; the shipped mapping_info.xml
// only covers part of it. Rather than waiting for an upstream table update,
// the pipeline translates an unknown tag once, remembers it here, and reuses
// it forever. The file stays hand-editable, so a bad translation can be fixed
// without touching the service.
type GenreDict struct {
	path string

	mu       sync.Mutex
	entries  map[string]string
	dirty    bool
	lastSave time.Time
}

// LoadGenreDict reads the dictionary file; a missing or malformed file
// yields an empty dictionary (never an error — the pipeline must keep working).
func LoadGenreDict(path string) *GenreDict {
	d := &GenreDict{path: path, entries: map[string]string{}}
	if path == "" {
		return d
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return d
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		log.Printf("[enrich] genre dictionary %s unreadable (%v), starting empty", path, err)
		return d
	}
	d.entries = m
	return d
}

// Lookup returns the stored translation for a Japanese tag.
func (d *GenreDict) Lookup(tag string) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	v, ok := d.entries[tag]
	return v, ok
}

// Set stores a translation and persists the dictionary (throttled to once
// every two seconds so a bulk scrape does not rewrite the file per tag).
func (d *GenreDict) Set(tag, translated string) {
	if d.path == "" || tag == "" || translated == "" || tag == translated {
		return
	}
	d.mu.Lock()
	if d.entries[tag] == translated {
		d.mu.Unlock()
		return
	}
	d.entries[tag] = translated
	d.dirty = true
	due := time.Since(d.lastSave) > 2*time.Second
	var data []byte
	if due {
		data, _ = json.MarshalIndent(d.entries, "", "  ")
		d.dirty = false
		d.lastSave = time.Now()
	}
	d.mu.Unlock()

	if due && data != nil {
		if err := os.WriteFile(d.path, data, 0o644); err != nil {
			log.Printf("[enrich] save genre dictionary: %v", err)
		}
	}
}

// Len reports how many tags are cached.
func (d *GenreDict) Len() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.entries)
}

// Save flushes pending changes.
func (d *GenreDict) Save() {
	if d.path == "" {
		return
	}
	d.mu.Lock()
	if !d.dirty {
		d.mu.Unlock()
		return
	}
	data, _ := json.MarshalIndent(d.entries, "", "  ")
	d.dirty = false
	d.lastSave = time.Now()
	d.mu.Unlock()
	if err := os.WriteFile(d.path, data, 0o644); err != nil {
		log.Printf("[enrich] save genre dictionary: %v", err)
	}
}
