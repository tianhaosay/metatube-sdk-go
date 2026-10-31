// Package enrich is the single metadata post-processing pipeline.
//
// Every MovieInfo returned by any provider goes through Pipeline.Apply before
// it is cached or served:
//
//	actor names  -> canonical Chinese names (mapping table)
//	genres/tags  -> translated Chinese tags, or dropped when marked delete
//	summary      -> optional newline cleanup, optional removal
//	title/summary-> translated (failover-capable), original text on failure
//
// The previous design split this across two places: actor/genre mapping lived
// inside the JavDB provider (so it silently did nothing whenever JavDB was
// disabled) while translation lived in the engine. One pipeline, one place.
package enrich

import (
	"regexp"
	"strings"
	"sync"

	"github.com/metatube-community/metatube-sdk-go/common/mapping"
	"github.com/metatube-community/metatube-sdk-go/model"
	"github.com/metatube-community/metatube-sdk-go/translate"
)

// ActorLookup resolves an actor name to its mapping entry.
type ActorLookup interface {
	Lookup(name string) (*mapping.ActorEntry, bool)
}

// InfoLookup resolves a genre/tag to its mapping entry.
type InfoLookup interface {
	Lookup(name string) (*mapping.InfoEntry, bool)
}

// Config is the complete post-processing configuration.
type Config struct {
	Actors         ActorLookup
	Info           InfoLookup
	Translator     translate.Translator
	TranslateLang  string
	DisableSummary bool
	CleanSummary   bool
}

// Pipeline applies a Config to movie metadata. It is safe for concurrent use
// and its configuration can be swapped at runtime (manage console).
type Pipeline struct {
	mu  sync.RWMutex
	cfg Config
}

func New(cfg Config) *Pipeline { return &Pipeline{cfg: cfg} }

// Snapshot returns the current configuration.
func (p *Pipeline) Snapshot() Config {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.cfg
}

// ActorMap returns the typed mapping table when the configured lookup is a
// mapping.ActorMap (the production case). Callers that need entry details
// (tmdb id, aliases) use this; the pipeline itself only needs Lookup.
func (p *Pipeline) ActorMap() *mapping.ActorMap {
	switch a := p.Snapshot().Actors.(type) {
	case *mapping.ActorMap:
		return a
	case *LazyActor:
		return a.Load()
	}
	return nil
}

// Configure mutates the configuration under lock.
func (p *Pipeline) Configure(fn func(*Config)) {
	p.mu.Lock()
	fn(&p.cfg)
	p.mu.Unlock()
}

func (p *Pipeline) SetTranslator(t translate.Translator) {
	p.Configure(func(c *Config) { c.Translator = t })
}

func (p *Pipeline) SetTranslateLang(lang string) {
	p.Configure(func(c *Config) {
		if lang != "" {
			c.TranslateLang = lang
		}
	})
}

func (p *Pipeline) SetDisableSummary(v bool) {
	p.Configure(func(c *Config) { c.DisableSummary = v })
}

func (p *Pipeline) SetMaps(actors ActorLookup, info InfoLookup) {
	p.Configure(func(c *Config) {
		c.Actors = actors
		c.Info = info
	})
}

// Apply rewrites the metadata in place. It never returns an error: a failing
// translator or a missing mapping table leaves the original text untouched.
func (p *Pipeline) Apply(info *model.MovieInfo) {
	if info == nil {
		return
	}
	cfg := p.Snapshot()

	// 1. Actor names -> canonical Chinese name.
	if cfg.Actors != nil && len(info.Actors) > 0 {
		actors := make([]string, 0, len(info.Actors))
		for _, name := range info.Actors {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if e, ok := cfg.Actors.Lookup(name); ok && e.Name != "" {
				name = e.Name
			}
			actors = append(actors, name)
		}
		info.Actors = dedupe(actors)
	}

	// 2. Genres -> translated tags (or dropped).
	if cfg.Info != nil && len(info.Genres) > 0 {
		genres := make([]string, 0, len(info.Genres))
		for _, tag := range info.Genres {
			tag = strings.TrimSpace(tag)
			if tag == "" {
				continue
			}
			if e, ok := cfg.Info.Lookup(tag); ok {
				if e.IsDelete() {
					continue
				}
				if e.Name != "" {
					tag = e.Name
				}
			}
			genres = append(genres, tag)
		}
		info.Genres = dedupe(genres)
	}

	// 3. Summary housekeeping.
	switch {
	case cfg.DisableSummary:
		info.Summary = ""
	case cfg.CleanSummary:
		info.Summary = collapseNewlines(info.Summary)
	}

	// 4. Translation (title first: it is the field users notice).
	if cfg.Translator == nil {
		return
	}
	target := cfg.TranslateLang
	if target == "" {
		target = "zh-CN"
	}
	if info.Title != "" {
		if t := translateField(cfg.Translator, info.Title, target, info.Number); t != "" {
			info.Title = t
		}
	}
	if info.Summary != "" {
		if t := translateField(cfg.Translator, info.Summary, target, info.Number); t != "" {
			info.Summary = t
		}
	}
}

func translateField(t translate.Translator, text, target, ref string) string {
	if rt, ok := t.(translate.RefTranslator); ok {
		if out, err := rt.TranslateWithRef(text, "auto", target, ref); err == nil && out != "" {
			return out
		}
		return ""
	}
	if out, err := t.Translate(text, "auto", target); err == nil && out != "" {
		return out
	}
	return ""
}

var newlinePattern = regexp.MustCompile(`\r?\n(\r?\n)+`)

func collapseNewlines(s string) string {
	if s == "" {
		return s
	}
	return strings.TrimSpace(newlinePattern.ReplaceAllString(s, "\n"))
}

func dedupe(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, v := range in {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
