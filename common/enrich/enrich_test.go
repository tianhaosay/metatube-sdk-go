package enrich

import (
	"strings"
	"testing"

	"github.com/metatube-community/metatube-sdk-go/common/mapping"
	"github.com/metatube-community/metatube-sdk-go/model"
)

type fakeActors map[string]string

func (f fakeActors) Lookup(name string) (*mapping.ActorEntry, bool) {
	if v, ok := f[name]; ok {
		return &mapping.ActorEntry{Name: v}, true
	}
	return nil, false
}

type fakeGenres map[string]string

func (f fakeGenres) Lookup(name string) (*mapping.InfoEntry, bool) {
	if v, ok := f[name]; ok {
		return &mapping.InfoEntry{Name: v}, true // "" means delete
	}
	return nil, false
}

type fakeTranslator struct {
	out   string
	err   error
	calls []string
}

func (f *fakeTranslator) Translate(text, from, to string) (string, error) {
	f.calls = append(f.calls, text)
	if f.err != nil {
		return "", f.err
	}
	return f.out, nil
}

func TestApply_MapsActorsAndGenres(t *testing.T) {
	p := New(Config{
		Actors: fakeActors{"夏目響": "夏目响"},
		Info:   fakeGenres{"ドラマ": "剧情", "ハイビジョン": mapping.DeleteMark},
	})
	info := &model.MovieInfo{
		Title:  "タイトル",
		Actors: []string{"夏目響", "夏目響", " unknown "},
		Genres: []string{"ドラマ", "ハイビジョン", "巨乳"},
	}
	p.Apply(info)

	if got := []string(info.Actors); len(got) != 2 || got[0] != "夏目响" || got[1] != "unknown" {
		t.Fatalf("actors = %v", got)
	}
	if got := []string(info.Genres); len(got) != 2 || got[0] != "剧情" || got[1] != "巨乳" {
		t.Fatalf("genres = %v (delete entry should be dropped)", got)
	}
	if info.Title != "タイトル" {
		t.Fatalf("title should be untouched without a translator, got %q", info.Title)
	}
}

func TestApply_TranslationAndSummary(t *testing.T) {
	tr := &fakeTranslator{out: "中文"}
	p := New(Config{Translator: tr, CleanSummary: true})
	info := &model.MovieInfo{
		Number:  "FLAV-359",
		Title:   "日本語タイトル",
		Summary: "line1\n\n\n\nline2",
	}
	p.Apply(info)
	if info.Title != "中文" || info.Summary != "中文" {
		t.Fatalf("translated fields = %q / %q", info.Title, info.Summary)
	}
	if len(tr.calls) != 2 {
		t.Fatalf("expected 2 translate calls, got %d", len(tr.calls))
	}

	// translation failure keeps the original text.
	p2 := New(Config{Translator: &fakeTranslator{err: errTest}})
	info2 := &model.MovieInfo{Title: "日本語", Summary: "本文"}
	p2.Apply(info2)
	if info2.Title != "日本語" || info2.Summary != "本文" {
		t.Fatalf("failed translation must keep originals, got %q / %q", info2.Title, info2.Summary)
	}
}

func TestApply_DisableSummary(t *testing.T) {
	p := New(Config{DisableSummary: true})
	info := &model.MovieInfo{Title: "t", Summary: "should vanish"}
	p.Apply(info)
	if info.Summary != "" {
		t.Fatalf("summary should be cleared, got %q", info.Summary)
	}
}

func TestApply_ActorFallback(t *testing.T) {
	calls := 0
	p := New(Config{
		Actors: fakeActors{"JavDB Actress": "中文名"},
		ActorFallback: func(number string) []string {
			calls++
			if number == "ABC-001" {
				return []string{"JavDB Actress"}
			}
			return nil
		},
	})

	// No actors -> fallback fills them, then mapping canonicalizes.
	info := &model.MovieInfo{Number: "ABC-001"}
	p.Apply(info)
	if len(info.Actors) != 1 || info.Actors[0] != "中文名" {
		t.Fatalf("fallback actors = %v", info.Actors)
	}

	// Actors present -> fallback must not be called.
	info2 := &model.MovieInfo{Number: "ABC-002", Actors: []string{"有演员"}}
	p.Apply(info2)
	if calls != 1 {
		t.Fatalf("fallback should only run for movies without actors, ran %d times", calls)
	}
}

func TestCollapseNewlines(t *testing.T) {
	got := collapseNewlines("\n\na\n\n\n\nb\n\n")
	if got != "a\nb" {
		t.Fatalf("collapseNewlines = %q", got)
	}
	if strings.Contains(got, "\n\n") {
		t.Fatal("consecutive newlines were not collapsed")
	}
}

var errTest = &testError{}

type testError struct{}

func (*testError) Error() string { return "boom" }

func TestActorFallbackDisabled(t *testing.T) {
	calls := 0
	p := New(Config{
		Actors: fakeActors{"JavDB Actress": "中文名"},
		ActorFallback: func(string) []string {
			calls++
			return []string{"JavDB Actress"}
		},
	})
	// Default (zero value) keeps the fallback enabled.
	if got := p.Snapshot().ActorFallbackDisabled; got {
		t.Fatalf("zero value should leave fallback enabled")
	}

	// Turn it off at runtime: no lookup may happen any more.
	p.SetActorFallbackEnabled(false)
	info := &model.MovieInfo{Number: "ABC-003"}
	p.Apply(info)
	if calls != 0 {
		t.Fatalf("fallback must not run when disabled, ran %d times", calls)
	}
	if len(info.Actors) != 0 {
		t.Fatalf("actors should stay empty, got %v", info.Actors)
	}

	// Re-enable: it runs again.
	p.SetActorFallbackEnabled(true)
	info2 := &model.MovieInfo{Number: "ABC-004"}
	p.Apply(info2)
	if calls != 1 || len(info2.Actors) != 1 || info2.Actors[0] != "中文名" {
		t.Fatalf("fallback should run after re-enable, calls=%d actors=%v", calls, info2.Actors)
	}
}
