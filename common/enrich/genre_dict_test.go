package enrich

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/metatube-community/metatube-sdk-go/model"
)

func TestGenreDict_TranslateThenReuse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "genre_dict.json")

	// First pass: unknown kana tag -> translator -> dictionary.
	p := New(Config{
		Genres:     LoadGenreDict(path),
		Translator: &fakeTranslator{out: "剧情"},
	})
	info := &model.MovieInfo{Number: "ABC-001", Genres: []string{"ドラマ", "巨乳"}}
	p.Apply(info)
	if got := []string(info.Genres); len(got) != 2 || got[0] != "剧情" || got[1] != "巨乳" {
		t.Fatalf("genres = %v", got)
	}

	// The dictionary must be persisted for the next process.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("dictionary not written: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("dictionary file empty")
	}

	// Second pass: no translator, cold start from disk -> dictionary hit.
	tr := &fakeTranslator{out: "WRONG"}
	p2 := New(Config{Genres: LoadGenreDict(path), Translator: tr})
	info2 := &model.MovieInfo{Number: "ABC-002", Genres: []string{"ドラマ"}}
	p2.Apply(info2)
	if got := []string(info2.Genres); len(got) != 1 || got[0] != "剧情" {
		t.Fatalf("dictionary hit expected, got %v", got)
	}
	if len(tr.calls) != 0 {
		t.Fatalf("translator should not be called on a dictionary hit, called %d times", len(tr.calls))
	}
}

func TestGenreDict_IgnoresBadTranslation(t *testing.T) {
	dir := t.TempDir()
	p := New(Config{
		Genres:     LoadGenreDict(filepath.Join(dir, "d.json")),
		Translator: &fakeTranslator{out: "还有かな"},
	})
	info := &model.MovieInfo{Genres: []string{"ドラマ"}}
	p.Apply(info)
	if got := []string(info.Genres); len(got) != 1 || got[0] != "ドラマ" {
		t.Fatalf("a still-Japanese translation must be rejected, got %v", got)
	}
}
