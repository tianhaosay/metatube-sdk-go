package translate

import (
	"errors"
	"strings"
)

// ErrEmptyResult is returned when an engine reports success but produces no
// text.
var ErrEmptyResult = errors.New("translate: empty result")

// GenreFailover is a two-engine translator used only for genre/tag
// normalization. It tries the primary first and falls back to the free Google
// engine when the primary fails, times out or returns an empty string.
type GenreFailover struct {
	primary   Translator
	secondary Translator
}

// NewGenreFailover pairs a primary engine with the free Google fallback.
func NewGenreFailover(primary, secondary Translator) Translator {
	if primary == nil {
		return secondary
	}
	if secondary == nil {
		return primary
	}
	return &GenreFailover{primary: primary, secondary: secondary}
}

func (f *GenreFailover) Translate(text, from, to string) (string, error) {
	out, err := f.primary.Translate(text, from, to)
	if err == nil && strings.TrimSpace(out) != "" {
		return out, nil
	}

	out, err = f.secondary.Translate(text, from, to)
	if err == nil && strings.TrimSpace(out) != "" {
		return out, nil
	}
	if err == nil {
		err = ErrEmptyResult
	}
	return "", err
}
