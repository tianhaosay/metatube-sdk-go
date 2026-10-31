package javdb

import (
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/metatube-community/metatube-sdk-go/common/mapping"
	"github.com/metatube-community/metatube-sdk-go/model"
	"github.com/metatube-community/metatube-sdk-go/provider"
)

// Supported field groups for cross-provider merging (config key
// "field_sources"). "images" is an alias covering all image-related fields.
//
// Example:
//
//	MT_PROVIDER_JAVDB__FIELD_SOURCES=images=fanza
//
// ...keeps JavDB for tags/actors/title (and everything else) while taking
// poster / cover / preview images from the Fanza (DMM) provider, matched by
// movie number.
//
// Supported field groups:
//
//	images        - ThumbURL, BigThumbURL, CoverURL, BigCoverURL,
//	                PreviewImages, PreviewVideoURL, PreviewVideoHLSURL
//	title         - Title
//	summary       - Summary
//	director      - Director
//	actors        - Actors
//	maker         - Maker
//	label         - Label
//	series        - Series
//	genres        - Genres
//	score         - Score
//	runtime       - Runtime
//	release_date  - ReleaseDate
const (
	fieldImages      = "images"
	fieldTitle       = "title"
	fieldSummary     = "summary"
	fieldDirector    = "director"
	fieldActors      = "actors"
	fieldMaker       = "maker"
	fieldLabel       = "label"
	fieldSeries      = "series"
	fieldGenres      = "genres"
	fieldScore       = "score"
	fieldRuntime     = "runtime"
	fieldReleaseDate = "release_date"
)

// validFieldSources lists all mergeable field groups.
var validFieldSources = map[string]struct{}{
	fieldImages: {}, fieldTitle: {}, fieldSummary: {}, fieldDirector: {},
	fieldActors: {}, fieldMaker: {}, fieldLabel: {}, fieldSeries: {},
	fieldGenres: {}, fieldScore: {}, fieldRuntime: {}, fieldReleaseDate: {},
}

// SetConfig implements provider.ConfigSetter.
//
// Keys (all optional, env: MT_PROVIDER_JAVDB__<KEY>):
//
//	field_sources      - comma-separated `field=provider` pairs; metadata
//	                     fields listed here are taken from the given provider
//	                     instead of JavDB itself, e.g. `images=fanza`.
//	mapping_actor_file - path to an MDC-style mapping_actor.xml; actor names
//	                     are canonicalized through it (zh_cn output).
//	mapping_info_file  - path to an MDC-style mapping_info.xml; tags/genres
//	                     are translated (or deleted when the output is
//	                     "删除") through it.
//	clean_summary      - collapse consecutive newlines in Summary (default
//	                     true). Title/summary translation is handled
//	                     globally by the engine translator (MT_TRANSLATE_*).
func (db *JavDB) SetConfig(config provider.Config) error {
	if config == nil {
		return nil
	}

	// field_sources.
	if config.Has("field_sources") {
		if v, err := config.GetString("field_sources"); err == nil {
			sources := map[string]string{}
			for _, pair := range strings.Split(v, ",") {
				k, val, found := strings.Cut(strings.TrimSpace(pair), "=")
				if !found {
					continue
				}
				k = strings.ToLower(strings.TrimSpace(k))
				val = strings.TrimSpace(val)
				if _, ok := validFieldSources[k]; !ok || val == "" {
					log.Printf("[JavDB] ignore invalid field_sources entry: %s=%s", k, val)
					continue
				}
				sources[k] = val
			}
			db.mu.Lock()
			db.fieldSources = sources
			db.mu.Unlock()
		}
	}

	// mapping_actor_file.
	if config.Has("mapping_actor_file") {
		if v, err := config.GetString("mapping_actor_file"); err == nil && v != "" {
			db.mu.Lock()
			db.mappingPath = v
			db.mu.Unlock()
		}
	}

	// mapping_info_file.
	if config.Has("mapping_info_file") {
		if v, err := config.GetString("mapping_info_file"); err == nil && v != "" {
			m, err := mapping.LoadInfoFile(v)
			if err != nil {
				return fmt.Errorf("load info mapping file: %w", err)
			}
			db.mu.Lock()
			db.infoMapping = m
			db.mu.Unlock()
		}
	}

	// clean_summary: collapse consecutive newlines in Summary (default true).
	if config.Has("clean_summary") {
		if v, err := config.GetBool("clean_summary"); err == nil {
			db.mu.Lock()
			db.cleanSummary = v
			db.mu.Unlock()
		}
	}

	// rate_limit: minimum interval between consecutive javdb requests
	// (single-flight + cooldown, default 10s; <=0 disables). Applies only
	// to the javdb domain — other providers are unaffected.
	if config.Has("rate_limit") {
		if v, err := config.GetDuration("rate_limit"); err == nil {
			setJavdbRateLimit(v)
		}
	}

	// secondary_proxy: proxy applied to secondary provider instances created
	// for field merging (e.g. fanza needs a proxy to reach DMM from CN).
	if config.Has("secondary_proxy") {
		if v, err := config.GetString("secondary_proxy"); err == nil && v != "" {
			db.mu.Lock()
			db.secondaryProxy = v
			db.mu.Unlock()
		}
	}
	return nil
}

// getActorMapping lazily loads the actor mapping table; a missing file (e.g.
// before the first online sync) only logs a warning and returns nil, and the
// next request retries (the online sync may have created the file by then).
func (db *JavDB) getActorMapping() *mapping.ActorMap {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.actorMapping != nil {
		return db.actorMapping
	}
	if db.mappingPath == "" {
		return nil
	}
	m, err := mapping.LoadActorFile(db.mappingPath)
	if err != nil {
		log.Printf("[JavDB] actor mapping not loaded yet: %v", err)
		return nil
	}
	db.actorMapping = m
	return m
}

// postProcess applies mapping tables and cross-provider field merging.
// Note: title/summary translation is handled globally by the engine-level
// translator (see cmd config), not here.
func (db *JavDB) postProcess(info *model.MovieInfo) {
	db.mu.Lock()
	fieldSources := db.fieldSources
	actorMapping := db.actorMapping
	infoMapping := db.infoMapping
	cleanSummary := db.cleanSummary
	db.mu.Unlock()
	if actorMapping == nil {
		actorMapping = db.getActorMapping()
	}

	// 1. Mapping tables (actor canonicalization + tag translation/removal).
	if actorMapping != nil {
		for i, name := range info.Actors {
			if e, ok := actorMapping.Lookup(name); ok {
				info.Actors[i] = e.Name
			}
		}
	}
	if infoMapping != nil {
		genres := make([]string, 0, len(info.Genres))
		for _, tag := range info.Genres {
			e, ok := infoMapping.Lookup(tag)
			if !ok {
				genres = append(genres, tag)
				continue
			}
			if e.IsDelete() {
				continue // drop the tag.
			}
			genres = append(genres, e.Name)
		}
		info.Genres = dedupe(genres)
	}

	// 2. Cross-provider field merging.
	if len(fieldSources) > 0 {
		for field, source := range fieldSources {
			if strings.EqualFold(source, Name) {
				continue // merging from ourselves is a no-op.
			}
			secondary, err := db.getSecondaryProvider(source)
			if err != nil {
				log.Printf("[JavDB] field merge: provider %s not found: %v", source, err)
				continue
			}
			secondaryInfo, err := secondary.GetMovieInfoByID(info.Number)
			if err != nil || secondaryInfo == nil {
				log.Printf("[JavDB] field merge: fetch %s info for %s from %s failed: %v",
					field, info.Number, source, err)
				continue
			}
			applyField(info, secondaryInfo, field)
		}
	}

	// Collapse consecutive newlines in Summary.
	if cleanSummary {
		info.Summary = collapseNewlines(info.Summary)
	}
}

var newlinePattern = regexp.MustCompile(`\r?\n(\r?\n)+`)

func collapseNewlines(s string) string {
	if s == "" {
		return s
	}
	return strings.TrimSpace(newlinePattern.ReplaceAllString(s, "\n"))
}

// getSecondaryProvider returns a cached MovieProvider instance by
// case-insensitive name. Freshly created instances are unconfigured, so the
// configured secondary proxy (and nothing else) is applied to them.
func (db *JavDB) getSecondaryProvider(name string) (provider.MovieProvider, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if p, ok := db.secProviders[strings.ToLower(name)]; ok {
		return p, nil
	}
	p, err := provider.GetMovieProviderByName(name)
	if err != nil {
		return nil, err
	}
	if db.secondaryProxy != "" {
		if ps, ok := p.(provider.ProxySetter); ok {
			if err := ps.SetProxy(db.secondaryProxy); err != nil {
				log.Printf("[JavDB] field merge: set proxy for secondary %s: %v", name, err)
			}
		}
	}
	db.secProviders[strings.ToLower(name)] = p
	return p, nil
}

// applyField copies the given field group from src to dst.
func applyField(dst, src *model.MovieInfo, field string) {
	switch field {
	case fieldImages:
		if src.ThumbURL != "" {
			dst.ThumbURL = src.ThumbURL
		}
		if src.BigThumbURL != "" {
			dst.BigThumbURL = src.BigThumbURL
		}
		if src.CoverURL != "" {
			dst.CoverURL = src.CoverURL
		}
		if src.BigCoverURL != "" {
			dst.BigCoverURL = src.BigCoverURL
		}
		if len(src.PreviewImages) > 0 {
			dst.PreviewImages = src.PreviewImages
		}
		if src.PreviewVideoURL != "" {
			dst.PreviewVideoURL = src.PreviewVideoURL
		}
		if src.PreviewVideoHLSURL != "" {
			dst.PreviewVideoHLSURL = src.PreviewVideoHLSURL
		}
	case fieldTitle:
		if src.Title != "" {
			dst.Title = src.Title
		}
	case fieldSummary:
		if src.Summary != "" {
			dst.Summary = src.Summary
		}
	case fieldDirector:
		if src.Director != "" {
			dst.Director = src.Director
		}
	case fieldActors:
		if len(src.Actors) > 0 {
			dst.Actors = src.Actors
		}
	case fieldMaker:
		if src.Maker != "" {
			dst.Maker = src.Maker
		}
	case fieldLabel:
		if src.Label != "" {
			dst.Label = src.Label
		}
	case fieldSeries:
		if src.Series != "" {
			dst.Series = src.Series
		}
	case fieldGenres:
		if len(src.Genres) > 0 {
			dst.Genres = src.Genres
		}
	case fieldScore:
		if src.Score > 0 {
			dst.Score = src.Score
		}
	case fieldRuntime:
		if src.Runtime > 0 {
			dst.Runtime = src.Runtime
		}
	case fieldReleaseDate:
		if !time.Time(src.ReleaseDate).IsZero() {
			dst.ReleaseDate = src.ReleaseDate
		}
	}
}
