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

func TestApply_KeepsTitleAndSummaryUntranslated(t *testing.T) {
	// The backend leaves title/summary untouched even when a translator is
	// configured; only genres are translated.
	tr := &fakeTranslator{out: "中文"}
	p := New(Config{Translator: tr, CleanSummary: true})
	info := &model.MovieInfo{
		Number:  "FLAV-359",
		Title:   "日本語タイトル",
		Summary: "line1\n\n\n\nline2",
	}
	p.Apply(info)
	if info.Title != "日本語タイトル" {
		t.Fatalf("title must stay untranslated, got %q", info.Title)
	}
	if info.Summary != "line1\nline2" {
		t.Fatalf("summary should only be newline-collapsed, got %q", info.Summary)
	}
	for _, c := range tr.calls {
		if c == "日本語タイトル" || strings.Contains(c, "line1") {
			t.Fatalf("translator must not receive title/summary, got call %q", c)
		}
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

	// No actors: the fallback fills them, then mapping canonicalizes.
	info := &model.MovieInfo{Number: "ABC-001"}
	p.Apply(info)
	if len(info.Actors) != 1 || info.Actors[0] != "中文名" {
		t.Fatalf("fallback actors = %v", info.Actors)
	}

	// Actors present: the fallback must not run.
	info2 := &model.MovieInfo{Number: "ABC-002", Actors: []string{"有演员"}}
	p.Apply(info2)
	if calls != 1 {
		t.Fatalf("fallback should only run for movies without actors, ran %d times", calls)
	}
}

func TestEnsureOnlyReportsRealChanges(t *testing.T) {
	fallbackCalls := 0
	p := New(Config{
		Actors: fakeActors{"夏目あいか": "夏目响"},
		ActorFallback: func(number string) []string {
			fallbackCalls++
			if number == "ABC-001" {
				return []string{"夏目あいか"}
			}
			return nil
		},
	})

	// Nothing to improve: no change, so a cached row is not written back.
	clean := &model.MovieInfo{
		Number: "ABC-000", Title: "中文标题",
		Actors: []string{"夏目响"}, Genres: []string{"剧情"},
	}
	if p.Ensure(clean) {
		t.Fatal("clean metadata must report no change")
	}

	// Japanese actor name (with kana): rewritten and reported as a change.
	info := &model.MovieInfo{Number: "ABC-000", Title: "中文标题", Actors: []string{"夏目あいか"}}
	if !p.Ensure(info) || info.Actors[0] != "夏目响" {
		t.Fatalf("japanese actor should be rewritten: actors=%v", info.Actors)
	}

	// Missing actors, fallback finds nothing: still nothing changed.
	empty := &model.MovieInfo{Number: "ABC-002", Title: "中文标题"}
	if p.Ensure(empty) {
		t.Fatal("an empty fallback result must not count as a change")
	}
	if fallbackCalls != 1 {
		t.Fatalf("fallback consulted %d times, want 1", fallbackCalls)
	}

	// Missing actors the fallback can fill: reported as a change.
	filled := &model.MovieInfo{Number: "ABC-001", Title: "中文标题"}
	if !p.Ensure(filled) || len(dedupe(filled.Actors)) != 1 || filled.Actors[0] != "夏目响" {
		t.Fatalf("fallback actors should be applied: %v", filled.Actors)
	}
}

func TestSetActorFallback(t *testing.T) {
	p := New(Config{})
	info := &model.MovieInfo{Number: "ABC-009"}
	if p.Ensure(info) {
		t.Fatal("no fallback configured, nothing to do")
	}
	p.SetActorFallback(func(string) []string { return []string{"新演员"} })
	if !p.Ensure(info) || len(info.Actors) != 1 || info.Actors[0] != "新演员" {
		t.Fatalf("runtime fallback should apply: %v", info.Actors)
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

	// Disabled at runtime: no lookup may run.
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
