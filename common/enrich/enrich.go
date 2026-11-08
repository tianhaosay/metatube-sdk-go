// Package enrich post-processes movie metadata. Every MovieInfo returned by a
// provider passes through Pipeline.Apply before it is cached or served:
//
//	actor names -> canonical Chinese names (mapping table)
//	genres/tags -> translated Chinese tags, or dropped when marked delete
//	summary     -> optional newline cleanup, optional removal
//
// Title and summary translation is not performed here: the Emby plugin calls
// /v1/translate with its own engine and settings. Genre translation is tag
// normalization backed by the persistent dictionary, not free-text translation.
package enrich

import (
	"log"
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
	TranslatorName string
	TranslateLang  string
	DisableSummary bool
	CleanSummary   bool

	// Genres is the persistent Japanese->Chinese tag dictionary. Tags the
	// curated mapping table misses are translated once and remembered here.
	Genres *GenreDict

	// ActorFallback is consulted only when a provider returned no actors at
	// all (common for compilations/amateur titles on DMM). It receives the
	// movie number and returns actor names from another source, or nil.
	ActorFallback func(number string) []string

	// ActorFallbackDisabled turns the fallback off. It is phrased negatively so
	// the zero value means "enabled", matching the console/plugin default.
	ActorFallbackDisabled bool
}

// Pipeline applies a Config to movie metadata. It is safe for concurrent use
// and its configuration can be swapped at runtime (manage console).
type Pipeline struct {
	mu  sync.RWMutex
	cfg Config
}

// New returns a pipeline using cfg as its initial configuration.
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

// SetTranslator replaces the translator used for genre tags. name labels the
// engine in log lines.
func (p *Pipeline) SetTranslator(t translate.Translator, name string) {
	p.Configure(func(c *Config) {
		c.Translator = t
		c.TranslatorName = name
	})
}

// SetDisableSummary toggles clearing the summary.
func (p *Pipeline) SetDisableSummary(v bool) {
	p.Configure(func(c *Config) { c.DisableSummary = v })
}

// SetActorFallbackEnabled toggles the actor fallback without rebuilding the
// pipeline, so a settings change takes effect immediately.
func (p *Pipeline) SetActorFallbackEnabled(v bool) {
	p.Configure(func(c *Config) { c.ActorFallbackDisabled = !v })
}

// Apply rewrites the metadata in place. It never returns an error: a failing
// translator or a missing mapping table leaves the original text untouched.
// Title and summary are left as-is and are translated by the Emby plugin.
func (p *Pipeline) Apply(info *model.MovieInfo) {
	if info == nil {
		return
	}
	cfg := p.Snapshot()

	// Actor fallback first: only runs when the provider returned no actors, so
	// the common path pays nothing. Gated by the console switch.
	if !cfg.ActorFallbackDisabled && len(info.Actors) == 0 && info.Number != "" && cfg.ActorFallback != nil {
		if extra := cfg.ActorFallback(info.Number); len(extra) > 0 {
			info.Actors = extra
		}
	}

	// Map actor names to their canonical Chinese names.
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

	// Map genres to canonical tags; delete-marked entries are dropped.
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

	// Tags still in Japanese: dictionary first, translation once per tag with
	// the result cached in the dictionary.
	if (cfg.Genres != nil || cfg.Translator != nil) && len(info.Genres) > 0 {
		target := cfg.TranslateLang
		if target == "" {
			target = "zh-CN"
		}
		genres := make([]string, 0, len(info.Genres))
		for _, g := range info.Genres {
			g = strings.TrimSpace(g)
			if g == "" {
				continue
			}
			if kanaRe.MatchString(g) {
				translated := ""
				if cfg.Genres != nil {
					if v, ok := cfg.Genres.Lookup(g); ok {
						translated = v
					}
				}
				if translated == "" && cfg.Translator != nil {
					if t := translateField(cfg.Translator, g, target); t != "" && !kanaRe.MatchString(t) {
						engine := cfg.TranslatorName
						if engine == "" {
							engine = "unknown"
						}
						log.Printf("[TRANSLATE] number=%s field=genre engine=%s result=ok from=%q to=%q", info.Number, engine, g, t)
						translated = t
						if cfg.Genres != nil {
							cfg.Genres.Set(g, t)
						}
					}
				}
				if translated != "" {
					g = translated
				}
			}
			genres = append(genres, g)
		}
		info.Genres = dedupe(genres)
	}

	// Summary housekeeping, independent of any translator: a disabled summary is
	// cleared here so nothing downstream can re-add it.
	switch {
	case cfg.DisableSummary:
		info.Summary = ""
	case cfg.CleanSummary:
		info.Summary = collapseNewlines(info.Summary)
	}
}

// hanRe matches Chinese characters. A zh-CN translation containing none is
// treated as failed (free engines occasionally return a romanization).
var hanRe = regexp.MustCompile(`[\x{4e00}-\x{9fff}]`)

// translateField translates one genre/tag and validates the result is usable
// Chinese. An empty return means "keep the original".
func translateField(t translate.Translator, text, target string) string {
	if t == nil {
		return ""
	}
	if out, err := t.Translate(text, "auto", target); err == nil && acceptableTranslation(text, out, target) {
		return out
	}
	return ""
}

// acceptableTranslation rejects results that cannot be a Chinese translation:
// no Han characters, or still full of kana.
func acceptableTranslation(src, out, target string) bool {
	if out == "" {
		return false
	}
	if strings.HasPrefix(strings.ToLower(target), "zh") {
		if !hanRe.MatchString(out) {
			return false
		}
		if kanaRe.MatchString(out) && !kanaRe.MatchString(src) {
			return false
		}
	}
	return true
}

var newlinePattern = regexp.MustCompile(`\r?\n(\r?\n)+`)

// kanaRe matches hiragana/katakana, i.e. "this text is still Japanese".
var kanaRe = regexp.MustCompile(`[\x{3040}-\x{30ff}]`)

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

// NeedsWork reports whether info still contains Japanese text the pipeline can
// improve. Cached rows pass through this check so a library refresh can fix old
// entries without re-scraping them.
func NeedsWork(info *model.MovieInfo) bool {
	if info == nil {
		return false
	}
	if kanaRe.MatchString(info.Title) || kanaRe.MatchString(info.Summary) {
		return true
	}
	for _, g := range info.Genres {
		if kanaRe.MatchString(g) {
			return true
		}
	}
	for _, a := range info.Actors {
		if kanaRe.MatchString(a) {
			return true
		}
	}
	return false
}
