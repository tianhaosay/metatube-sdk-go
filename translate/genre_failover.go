package translate

import (
	"errors"
	"strings"
)

// ErrEmptyResult is returned when an engine reports success but produces no
// text; several free engines do this on quota/error paths, which used to stop
// the chain silently.
var ErrEmptyResult = errors.New("translate: empty result")

// GenreFailover is a two-engine translator used ONLY for genre/tag
// normalization.
//
// It is deliberately not a general metadata translator: upstream metatube does
// not translate metadata at all (the Emby plugin calls /v1/translate), so the
// backend only needs a small, predictable helper for Japanese tags. Keeping it
// separate avoids the old behaviour where title/summary were translated twice.
//
// Behaviour: primary first; when it fails, times out or returns an empty
// string, the secondary (free Google) answers instead. When both fail the
// caller keeps the original Japanese text.
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
	if err == nil {
		err = ErrEmptyResult
	}
	primaryErr := err

	out, err = f.secondary.Translate(text, from, to)
	if err == nil && strings.TrimSpace(out) != "" {
		return out, nil
	}
	if err == nil {
		err = ErrEmptyResult
	}
	_ = primaryErr // the caller only needs to know that nothing worked
	return "", err
}
