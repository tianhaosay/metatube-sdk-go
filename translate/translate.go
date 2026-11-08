package translate

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"

	"go.uber.org/atomic"
)

var ErrTranslator = &errorTranslator{errors.New("translate: unknown translator")}

type Translator interface {
	Translate(text, from, to string) (string, error)
}

var (
	_ error      = (*errorTranslator)(nil)
	_ Translator = (*errorTranslator)(nil)
)

type errorTranslator struct{ error }

func (e errorTranslator) Translate(string, string, string) (string, error) {
	return "", e.error
}

type factory struct {
	name string
	new  func() Translator
}

// Translators is the list of registered translators.
var (
	translatorsMu     sync.Mutex
	atomicTranslators atomic.Value
)

// translator is a generic type of Translator.
type translator[T any] interface {
	*T
	Translator
}

// Register registers a translator to package.
func Register[T any, P translator[T]](translator P) {
	translatorsMu.Lock()
	translators, _ := atomicTranslators.Load().([]factory)
	atomicTranslators.Store(append(translators, factory{
		name: reflect.TypeOf(translator).Elem().Name(),
		new:  func() Translator { return any(new(T)).(Translator) },
	}))
	translatorsMu.Unlock()
}

func match(name string) factory {
	translators := atomicTranslators.Load().([]factory)
	for _, t := range translators {
		if strings.EqualFold(t.name, name) {
			return t
		}
	}
	return factory{}
}

func New(name string, unmarshal func(any) error) Translator {
	f := match(name)
	if f.new == nil {
		return ErrTranslator
	}
	t := f.new()
	if err := unmarshal(t); err != nil {
		return &errorTranslator{err}
	}
	return t
}

// Exists reports whether an engine with the given name is registered.
func Exists(name string) bool {
	return match(name).new != nil
}

// Build creates a Translator by engine name with the given params.
//
// Param keys are matched case-insensitively (ignoring '-' and '_') against the
// engine's JSON-tagged fields, so environment-variable-friendly keys like
// "DEEPL_API_KEY" map onto the "deepl-api-key" field.
func Build(name string, params map[string]string) Translator {
	if !Exists(name) {
		return ErrTranslator
	}
	return New(name, func(v any) error {
		alias := map[string]string{} // normalized key -> json tag
		rv := reflect.TypeOf(v).Elem()
		for i := 0; i < rv.NumField(); i++ {
			tag := rv.Field(i).Tag.Get("json")
			if tag == "" {
				tag = rv.Field(i).Name
			}
			tag = strings.Split(tag, ",")[0]
			alias[normalizeParamKey(tag)] = tag
		}
		m := make(map[string]string, len(params))
		for k, val := range params {
			if tag, ok := alias[normalizeParamKey(k)]; ok {
				m[tag] = val
			}
		}
		data, err := json.Marshal(m)
		if err != nil {
			return err
		}
		return json.Unmarshal(data, v)
	})
}

// normalizeParamKey lower-cases a key and drops '-' and '_' so that
// "deepl-api-key", "DEEPL_API_KEY" and "DeeplApiKey" all compare equal.
func normalizeParamKey(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	return s
}
