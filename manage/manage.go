// Package manage provides a user-friendly actor management module for
// metatube-plus, aligned with avdb's implementation:
//
//   - duplicate actor detection & merge (via the AvdbMagicTools Emby plugin)
//   - missing-avatar scanning & one-click completion (gfriends)
//   - online actor-mapping table sync (li-peifeng/Jav-Actors-Mapping)
//
// Configuration comes from environment variables (see cmd), and a simple
// embedded web UI is served at /manage/.
package manage

import (
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/metatube-community/metatube-sdk-go/common/mapping"
)

// Config holds the manage module configuration.
type Config struct {
	// Emby server connection.
	EmbyURL      string
	EmbyAPIKey   string
	EmbyUserID   string
	EmbyLibraryIDs []string

	// Local actor mapping table path (shared with the JavDB provider).
	ActorMappingPath string

	// MappingOnline enables the online source sync (default true when a
	// mapping path is configured).
	MappingOnline bool
	// MappingSyncInterval is the periodic sync interval (default 24h; <=0
	// disables the background ticker).
	MappingSyncInterval time.Duration
}

var (
	mu sync.RWMutex
	cfg *Config
	actorMap *mapping.ActorMap
	syncState = jobStatus{}
)

// Setup initializes the manage module and starts the mapping sync loop.
func Setup(c *Config) {
	mu.Lock()
	cfg = c
	mu.Unlock()

	if c.ActorMappingPath != "" {
		if m, err := mapping.LoadActorFile(c.ActorMappingPath); err != nil {
			log.Printf("[manage] actor mapping load failed: %v", err)
		} else {
			mu.Lock()
			actorMap = m
			mu.Unlock()
		}
	}
	initSettings()
	if c.MappingOnline {
		go func() {
			// Initial sync shortly after startup.
			time.Sleep(10 * time.Second)
			if res, err := SyncMappingNow(); err != nil {
				log.Printf("[manage] initial mapping sync failed: %v", err)
			} else if res.Updated {
				log.Printf("[manage] initial mapping sync: %s", res.Message)
			}
			if c.MappingSyncInterval <= 0 {
				return
			}
			ticker := time.NewTicker(c.MappingSyncInterval)
			defer ticker.Stop()
			for range ticker.C {
				if _, err := SyncMappingNow(); err != nil {
					log.Printf("[manage] periodic mapping sync failed: %v", err)
				}
			}
		}()
	}
	log.Printf("[manage] initialized: emby=%s mapping=%s online-sync=%v interval=%s",
		c.EmbyURL, c.ActorMappingPath, c.MappingOnline, c.MappingSyncInterval)
}

func getConfig() *Config {
	mu.RLock()
	defer mu.RUnlock()
	return cfg
}

// getActorMap returns the loaded actor mapping (nil when unavailable).
func getActorMap() *mapping.ActorMap {
	mu.RLock()
	defer mu.RUnlock()
	return actorMap
}

// SyncMappingNow runs one online mapping sync and hot-reloads the table.
func SyncMappingNow() (*mapping.SyncResult, error) {
	c := getConfig()
	if c == nil || c.ActorMappingPath == "" {
		return nil, ErrNotConfigured
	}
	res, err := mapping.SyncOnlineActorMapping(c.ActorMappingPath)
	if err != nil {
		syncState.fail(err.Error())
		return nil, err
	}
	syncState.finish(map[string]any{
		"updated": res.Updated,
		"version": res.Version,
		"records": res.Records,
		"message": res.Message,
	})
	// Hot reload the table after a successful sync.
	if res.Updated {
		if m, err := mapping.LoadActorFile(c.ActorMappingPath); err != nil {
			log.Printf("[manage] mapping reload failed: %v", err)
		} else {
			mu.Lock()
			actorMap = m
			mu.Unlock()
		}
	}
	return res, nil
}

// MappingStatus describes the current mapping table state for the UI.
func MappingStatus() map[string]any {
	c := getConfig()
	status := map[string]any{
		"path":     "",
		"entries":  0,
		"version":  "",
		"syncing":  false,
		"online":   false,
	}
	if c == nil {
		return status
	}
	status["path"] = c.ActorMappingPath
	status["online"] = c.MappingOnline
	status["sync"] = syncState.get()
	if data, err := readVersionFile(c.ActorMappingPath + ".version"); err == nil {
		status["version"] = data
	}
	if m := getActorMap(); m != nil {
		status["entries"] = m.Len()
	}
	return status
}

// ActorMapping returns the current actor mapping, loading it lazily.
func ActorMapping() *mapping.ActorMap {
	if m := getActorMap(); m != nil {
		return m
	}
	c := getConfig()
	if c == nil || c.ActorMappingPath == "" {
		return nil
	}
	m, err := mapping.LoadActorFile(c.ActorMappingPath)
	if err != nil {
		return nil
	}
	mu.Lock()
	actorMap = m
	mu.Unlock()
	return m
}

// jobStatus is a simple one-slot background job status holder.
type jobStatus struct {
	mu       sync.Mutex
	running  bool
	progress float64
	message  string
	result   map[string]any
	error    string
}

func (s *jobStatus) start() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return false
	}
	s.running = true
	s.progress = 0
	s.message = "准备中"
	s.result = nil
	s.error = ""
	return true
}

func (s *jobStatus) update(progress float64, message string) {
	s.mu.Lock()
	s.progress = progress
	s.message = message
	s.mu.Unlock()
}

func (s *jobStatus) finish(result map[string]any) {
	s.mu.Lock()
	s.running = false
	s.progress = 100
	s.result = result
	s.message = "完成"
	s.mu.Unlock()
}

func (s *jobStatus) fail(err string) {
	s.mu.Lock()
	s.running = false
	s.error = err
	s.message = "失败"
	s.mu.Unlock()
}

func (s *jobStatus) get() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]any{
		"running":  s.running,
		"progress": s.progress,
		"message":  s.message,
		"result":   s.result,
		"error":    s.error,
	}
}

func readVersionFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
