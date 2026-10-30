package engine

import (
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
		e.translator = translator
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
