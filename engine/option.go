package engine

import (
	"log"
	"time"

	mt "github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/translate"
)

type Option func(*Engine)

func WithEngineName(name string) Option {
	return func(e *Engine) {
		e.name = name
	}
}

func WithRequestTimeout(timeout time.Duration) Option {
	return func(e *Engine) {
		e.timeout = timeout
	}
}

func WithActorProviderConfig(name string, config mt.Config) Option {
	return func(e *Engine) {
		e.actorProviderConfigs.Set(name, config)
	}
}

func WithMovieProviderConfig(name string, config mt.Config) Option {
	return func(e *Engine) {
		e.movieProviderConfigs.Set(name, config)
	}
}

func WithTranslator(translator translate.Translator) Option {
	return func(e *Engine) {
		e.SetTranslator(translator)
	}
}

func WithTranslateLang(lang string) Option {
	return func(e *Engine) {
		if lang != "" {
			e.translateLang = lang
		}
	}
}

func WithDisableSummary(disable bool) Option {
	return func(e *Engine) {
		e.disableSummary = disable
	}
}

// WithLogger overrides the engine logger (used by the manage console to
// mirror engine logs into the in-memory log ring).
func WithLogger(logger *log.Logger) Option {
	return func(e *Engine) {
		e.logger = logger
	}
}

// SetTranslator swaps the metadata translator at runtime (GUI settings).
func (e *Engine) SetTranslator(t translate.Translator) {
	e.cfgMu.Lock()
	e.translator = t
	e.cfgMu.Unlock()
}

// SetDisableSummary toggles the summary switch at runtime (GUI settings).
func (e *Engine) SetDisableSummary(v bool) {
	e.cfgMu.Lock()
	e.disableSummary = v
	e.cfgMu.Unlock()
}

// SetTranslateLang updates the translation target language at runtime.
func (e *Engine) SetTranslateLang(lang string) {
	e.cfgMu.Lock()
	if lang != "" {
		e.translateLang = lang
	}
	e.cfgMu.Unlock()
}
