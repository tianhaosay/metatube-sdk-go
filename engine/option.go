package engine

import (
	"log"
	"time"

	"github.com/metatube-community/metatube-sdk-go/common/enrich"
	mt "github.com/metatube-community/metatube-sdk-go/provider"
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

// WithEnrich installs the metadata post-processing pipeline; without it the
// engine serves provider output untouched.
func WithEnrich(p *enrich.Pipeline) Option {
	return func(e *Engine) {
		e.enrich = p
	}
}

// WithLogger overrides the engine logger, e.g. to mirror engine logs into the
// console's in-memory ring buffer.
func WithLogger(logger *log.Logger) Option {
	return func(e *Engine) {
		e.logger = logger
	}
}
