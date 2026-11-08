package enrich

import (
	"log"
	"sync"

	"github.com/metatube-community/metatube-sdk-go/common/mapping"
)

// LazyActor loads an actor mapping table on first use and keeps it.
//
// The file may not exist at startup (the manage console creates it during the
// first online sync), so loading is lazy and retried until the file appears; the
// table then hot-reloads on mtime change.
type LazyActor struct {
	path string

	mu  sync.Mutex
	cur *mapping.ActorMap
}

// NewLazyActor returns a loader for the actor mapping file at path.
func NewLazyActor(path string) *LazyActor { return &LazyActor{path: path} }

// Load returns the table, loading it once it has become available.
func (l *LazyActor) Load() *mapping.ActorMap {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cur != nil {
		return l.cur
	}
	m, err := mapping.LoadActorFile(l.path)
	if err != nil {
		return nil // file not there yet; retried on the next lookup.
	}
	log.Printf("[enrich] actor mapping loaded: %d entries", m.Len())
	l.cur = m
	return m
}

func (l *LazyActor) Lookup(name string) (*mapping.ActorEntry, bool) {
	m := l.Load()
	if m == nil {
		return nil, false
	}
	return m.Lookup(name)
}

// LazyInfo is the genre/tag counterpart of LazyActor.
type LazyInfo struct {
	path string

	mu  sync.Mutex
	cur *mapping.InfoMap
}

// NewLazyInfo returns a loader for the genre mapping file at path.
func NewLazyInfo(path string) *LazyInfo { return &LazyInfo{path: path} }

// Load returns the table, loading it once it has become available.
func (l *LazyInfo) Load() *mapping.InfoMap {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cur != nil {
		return l.cur
	}
	m, err := mapping.LoadInfoFile(l.path)
	if err != nil {
		return nil
	}
	log.Printf("[enrich] genre mapping loaded")
	l.cur = m
	return m
}

func (l *LazyInfo) Lookup(name string) (*mapping.InfoEntry, bool) {
	m := l.Load()
	if m == nil {
		return nil, false
	}
	return m.Lookup(name)
}
