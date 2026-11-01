package manage

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// tmdbClient is a minimal TMDB person client used to complete actor profiles
// (avatar + gender). It is optional: without an API key every method is a
// no-op, so the completion flow degrades to Gfriends only.
type tmdbClient struct {
	key  string
	lang string
	http *http.Client
}

func newTMDBClient(cfg *Config) *tmdbClient {
	if cfg == nil || cfg.TMDBAPIKey == "" {
		return nil
	}
	lang := cfg.TMDBLanguage
	if lang == "" {
		lang = "zh-CN"
	}
	client := &http.Client{Timeout: 20 * time.Second}
	if cfg.TMDBProxy != "" {
		if u, err := url.Parse(cfg.TMDBProxy); err == nil {
			client.Transport = &http.Transport{Proxy: http.ProxyURL(u)}
		}
	}
	return &tmdbClient{key: cfg.TMDBAPIKey, lang: lang, http: client}
}

// TMDBPerson is the subset of a TMDB person record the completion flow needs.
type TMDBPerson struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Gender      int    `json:"gender"` // 1 = female, 2 = male
	ProfilePath string `json:"profile_path"`
}

// Gender constants as returned by TMDB.
const (
	GenderFemale = 1
	GenderMale   = 2
)

// searchPerson looks up a person by exact-ish name. TMDB search is fuzzy, so
// the caller compares names before trusting the result.
func (t *tmdbClient) searchPerson(name string) (*TMDBPerson, error) {
	if t == nil || name == "" {
		return nil, fmt.Errorf("tmdb: not configured")
	}
	q := url.Values{}
	q.Set("api_key", t.key)
	q.Set("query", name)
	q.Set("language", t.lang)
	u := "https://api.themoviedb.org/3/search/person?" + q.Encode()

	resp, err := t.http.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tmdb search: http %d", resp.StatusCode)
	}
	var out struct {
		Results []TMDBPerson `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	for i := range out.Results {
		if strings.EqualFold(strings.TrimSpace(out.Results[i].Name), strings.TrimSpace(name)) {
			return &out.Results[i], nil
		}
	}
	if len(out.Results) > 0 {
		return &out.Results[0], nil
	}
	return nil, nil
}

// imageURL returns the full-size profile image URL, or "" when the person has
// no profile picture.
func (p *TMDBPerson) imageURL() string {
	if p == nil || p.ProfilePath == "" {
		return ""
	}
	return "https://image.tmdb.org/t/p/original" + p.ProfilePath
}
