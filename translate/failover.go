package translate

import (
	"encoding/json"
	"errors"
	"log"
	"reflect"
	"strings"
	"sync"
	"time"
)

// ErrTranslateTimeout is returned when a translation exceeds its deadline.
var ErrTranslateTimeout = errors.New("translate: timeout")

// RefTranslator is an optional extension of Translator that accepts a
// reference string (e.g. the movie number) for diagnostics logging.
type RefTranslator interface {
	Translator
	TranslateWithRef(text, from, to, ref string) (string, error)
}

// translateWithTimeout runs t.Translate with a deadline. The underlying
// goroutine is abandoned (not killed) when the deadline expires.
func translateWithTimeout(t Translator, text, from, to string, timeout time.Duration) (result string, err error) {
	if timeout <= 0 {
		return t.Translate(text, from, to)
	}
	done := make(chan struct{})
	go func() {
		result, err = t.Translate(text, from, to)
		close(done)
	}()
	select {
	case <-done:
		return result, err
	case <-time.After(timeout):
		return "", ErrTranslateTimeout
	}
}

// Failover pairs a primary translator with a secondary one.
//
// Behaviour:
//   - Primary is always tried first; on success the consecutive-takeover
//     counter resets.
//   - When the primary fails (timeout or error), the secondary takes over.
//     If the secondary succeeds, the takeover is counted; after `threshold`
//     consecutive takeovers the secondary is permanently promoted to
//     primary (and the old primary demoted to secondary).
//   - When both engines fail, the call returns an error and the caller
//     should keep the original (e.g. Japanese) text. Failures are logged
//     with the reference string for later inspection.
type Failover struct {
	mu sync.Mutex

	primaryName   string
	secondaryName string
	primary       Translator
	secondary     Translator // may be nil: no secondary engine.

	timeout   time.Duration
	threshold int

	consecutive int
}

func NewFailover(primary Translator, primaryName string, secondary Translator, secondaryName string, timeout time.Duration, threshold int) *Failover {
	if threshold <= 0 {
		threshold = 5
	}
	return &Failover{
		primaryName:   primaryName,
		secondaryName: secondaryName,
		primary:       primary,
		secondary:     secondary,
		timeout:       timeout,
		threshold:     threshold,
	}
}

// Translate implements Translator (without a reference label).
func (f *Failover) Translate(text, from, to string) (string, error) {
	return f.TranslateWithRef(text, from, to, "-")
}

// TranslateWithRef translates text with primary/secondary failover. ref is
// a diagnostic label (e.g. the movie number) used in log lines.
func (f *Failover) TranslateWithRef(text, from, to, ref string) (string, error) {
	f.mu.Lock()
	primary, secondary := f.primary, f.secondary
	primaryName, secondaryName := f.primaryName, f.secondaryName
	f.mu.Unlock()

	result, err := translateWithTimeout(primary, text, from, to, f.timeout)
	if err == nil {
		f.resetConsecutive()
		return result, nil
	}
	primaryErr := err
	log.Printf("[TRANSLATE] primary engine %s failed for %s: %v", primaryName, ref, primaryErr)

	if secondary == nil {
		log.Printf("[TRANSLATE] no secondary engine, keep original for %s (%s failed: %v)", ref, primaryName, primaryErr)
		return "", primaryErr
	}

	result, err = translateWithTimeout(secondary, text, from, to, f.timeout)
	if err != nil {
		log.Printf("[TRANSLATE] all engines failed for %s (primary %s: %v; secondary %s: %v), keep original", ref, primaryName, primaryErr, secondaryName, err)
		return "", err
	}

	f.countTakeover(ref, primaryErr)
	return result, nil
}

func (f *Failover) resetConsecutive() {
	f.mu.Lock()
	f.consecutive = 0
	f.mu.Unlock()
}

func (f *Failover) countTakeover(ref string, primaryErr error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.consecutive++
	if f.consecutive < f.threshold {
		log.Printf("[TRANSLATE] secondary %s took over for %s (%d/%d consecutive takeovers)", f.secondaryName, ref, f.consecutive, f.threshold)
		return
	}
	// Promote the secondary engine to primary.
	f.primary, f.secondary = f.secondary, f.primary
	f.primaryName, f.secondaryName = f.secondaryName, f.primaryName
	f.consecutive = 0
	log.Printf("[TRANSLATE] primary engine %s kept failing (%v), promoted secondary %s to primary after %d consecutive takeovers",
		f.secondaryName /* old primary after swap */, primaryErr, f.primaryName, f.threshold)
}

// Exists reports whether a translator engine with the given name is
// registered.
func Exists(name string) bool {
	return match(name).new != nil
}

// Build creates a Translator by engine name with the given params. Param
// keys are matched case-insensitively (ignoring '-' and '_') against the
// engine's JSON-tagged fields, so environment-variable-friendly keys like
// "DEEPL_API_KEY" map to the "deepl-api-key" field.
func Build(name string, params map[string]string) Translator {
	f := match(name)
	if f.new == nil {
		return ErrTranslator
	}
	t := f.new()
	unmarshal := func(v any) error {
		alias := map[string]string{} // normalized key -> json tag.
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
		for k, v := range params {
			if tag, ok := alias[normalizeParamKey(k)]; ok {
				m[tag] = v
			}
		}
		data, err := json.Marshal(m)
		if err != nil {
			return err
		}
		return json.Unmarshal(data, v)
	}
	if err := unmarshal(t); err != nil {
		return &errorTranslator{err}
	}
	return t
}

func normalizeParamKey(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	return s
}
