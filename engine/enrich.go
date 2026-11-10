package engine

import (
	"gorm.io/gorm/clause"

	"github.com/metatube-community/metatube-sdk-go/model"
)

// enrichInfo applies the metadata pipeline to freshly scraped info before it is
// cached, so later reads never re-translate.
func (e *Engine) enrichInfo(info *model.MovieInfo) {
	if e.enrich != nil && info != nil {
		e.enrich.Apply(info)
	}
}

// enrichCached repairs a row served from the cache: metadata that is still
// untranslated (or a summary that should be hidden) is processed again, and
// missing actors are filled through the pipeline's fallback. The row is written
// back only when something actually changed, which is also the return value.
func (e *Engine) enrichCached(info *model.MovieInfo) bool {
	if e.enrich == nil || info == nil || !e.enrich.Ensure(info) {
		return false
	}
	e.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(info) // ignore error
	return true
}
