package javdb

import (
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/gocolly/colly/v2"
	"golang.org/x/text/language"

	"github.com/metatube-community/metatube-sdk-go/common/fetch"
	"github.com/metatube-community/metatube-sdk-go/common/number"
	"github.com/metatube-community/metatube-sdk-go/common/parser"
	"github.com/metatube-community/metatube-sdk-go/common/throttle"
	"github.com/metatube-community/metatube-sdk-go/model"
	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/scraper"
)

var (
	_ provider.MovieProvider = (*JavDB)(nil)
	_ provider.MovieSearcher = (*JavDB)(nil)
	_ provider.ActorProvider = (*JavDB)(nil)
	_ provider.ActorSearcher = (*JavDB)(nil)
	_ provider.Fetcher       = (*JavDB)(nil)
	_ provider.ConfigSetter  = (*JavDB)(nil)
)

const (
	Name = "JavDB"
	// Priority 0 disables JavDB by default; Cloudflare throttles this domain.
	// Enable it as a long-tail fallback with MT_PROVIDER_JAVDB__PRIORITY=900.
	Priority = 0
	// defaultCool is the empirically safe interval for the javdb domain.
	defaultCool = 10 * time.Second
)

const (
	baseURL        = "https://javdb.com/"
	movieURL       = "https://javdb.com/v/%s"
	searchURL      = "https://javdb.com/search?q=%s&f=all"
	actorURL       = "https://javdb.com/actors/%s"
	actorSearchURL = "https://javdb.com/search?q=%s&f=actors"
)

var numberPattern = regexp.MustCompile(`(?i)^[a-z]{2,10}[-_ ]?\d{1,6}(?:[-_ ]?\d+)?$`)

type JavDB struct {
	*fetch.Fetcher
	*scraper.Scraper
}

func New() *JavDB {
	return &JavDB{
		Fetcher: fetch.Default(&fetch.Config{Referer: baseURL}),
		Scraper: scraper.NewDefaultScraper(
			Name, baseURL, Priority,
			language.Chinese, // zh-TW by default.
			scraper.WithHeaders(map[string]string{
				"Referer": baseURL,
			}),
			scraper.WithCookies(baseURL, []*http.Cookie{
				// Site locale: zh (traditional) / en.
				{Name: "locale", Value: "zh"},
				// Age verification.
				{Name: "over18", Value: "1"},
			})),
	}
}

func (db *JavDB) NormalizeMovieID(id string) string {
	return strings.TrimSpace(id)
}

func (db *JavDB) ParseMovieIDFromURL(rawURL string) (string, error) {
	homepage, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	return path.Base(homepage.Path), nil
}

// GetMovieInfoByID returns movie info by JavDB URL slug (e.g. "RkdrWK") or by
// movie number (e.g. "FLAV-359"); numbers are resolved through site search.
func (db *JavDB) GetMovieInfoByID(id string) (info *model.MovieInfo, err error) {
	id = db.NormalizeMovieID(id)
	if numberPattern.MatchString(strings.ReplaceAll(id, " ", "")) {
		// Looks like a movie number rather than a JavDB slug.
		var results []*model.MovieSearchResult
		if results, err = db.SearchMovie(id); err != nil {
			return
		}
		want := normalizeNumber(id)
		for _, r := range results {
			if normalizeNumber(r.Number) == want {
				return db.GetMovieInfoByURL(r.Homepage)
			}
		}
		return nil, provider.ErrInfoNotFound
	}
	return db.GetMovieInfoByURL(fmt.Sprintf(movieURL, id))
}

func (db *JavDB) GetMovieInfoByURL(rawURL string) (info *model.MovieInfo, err error) {
	throttle.Wait(Name)
	id, err := db.ParseMovieIDFromURL(rawURL)
	if err != nil {
		return
	}

	info = &model.MovieInfo{
		ID:            id,
		Provider:      db.Name(),
		Homepage:      rawURL,
		Actors:        []string{},
		PreviewImages: []string{},
		Genres:        []string{},
	}

	c := db.ClonedCollector()

	// Title: <h2 class="title is-4"><strong class="current-title">TITLE</strong></h2>
	c.OnXML(`//div[contains(@class,"video-detail")]//h2[contains(@class,"title")]`, func(e *colly.XMLElement) {
		if n := strings.TrimSpace(e.ChildText(`.//strong[1]`)); n != "" {
			info.Number = n
		}
		if t := strings.TrimSpace(e.ChildText(`.//strong[contains(@class,"current-title")]`)); t != "" {
			info.Title = t
		}
	})

	// Cover: <img class="video-cover" src="...">
	c.OnXML(`//img[contains(@class,"video-cover")]`, func(e *colly.XMLElement) {
		info.CoverURL = e.Request.AbsoluteURL(e.Attr("src"))
		info.BigCoverURL = info.CoverURL
	})

	// Panel fields: <div class="panel-block"><strong>LABEL:</strong>
	// <span class="value">...</span></div>
	c.OnXML(`//nav[contains(@class,"movie-panel-info")]//div[contains(@class,"panel-block")]`, func(e *colly.XMLElement) {
		label := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(e.ChildText(`.//strong[1]`)), ":"))
		value := strings.TrimSpace(e.ChildText(`.//span[contains(@class,"value")]`))
		var valueLink string
		if v := strings.TrimSpace(e.ChildText(`.//span[contains(@class,"value")]//a[1]`)); v != "" {
			valueLink = v
		}
		switch label {
		case "番號", "番号", "id":
			if v := strings.ReplaceAll(value, " ", ""); v != "" {
				info.Number = v
			}
		case "日期", "發行日期", "released date", "released":
			info.ReleaseDate = parser.ParseDate(value)
		case "時長", "length":
			info.Runtime = parser.ParseRuntime(value)
		case "導演", "director":
			info.Director = orDefault(valueLink, value)
		case "片商", "製作", "maker", "studio":
			info.Maker = orDefault(valueLink, value)
		case "發行", "發行商", "publisher", "label":
			info.Label = orDefault(valueLink, value)
		case "系列", "series":
			info.Series = orDefault(valueLink, value)
		case "評分", "rating", "score":
			info.Score = parser.ParseScore(value)
		}
	})

	// Genres: <a href="/tags?...">TAG</a> links inside the genre panel block.
	c.OnXML(`//nav[contains(@class,"movie-panel-info")]//span[contains(@class,"value")]/a[starts-with(@href,"/tags")]`, func(e *colly.XMLElement) {
		if t := strings.TrimSpace(e.Text); t != "" {
			info.Genres = append(info.Genres, t)
		}
	})

	// Actors: <a class="actor-female|actor-male" href="/actors/xx">NAME</a>
	c.OnXML(`//nav[contains(@class,"movie-panel-info")]//span[contains(@class,"value")]/a[contains(@class,"actor-")]`, func(e *colly.XMLElement) {
		if t := strings.TrimSpace(e.Text); t != "" {
			info.Actors = append(info.Actors, t)
		}
	})

	// Preview images: <a class="tile-item" href="FULL-SIZE"> in .preview-images
	c.OnXML(`//div[contains(@class,"preview-images")]//a[contains(@class,"tile-item")]`, func(e *colly.XMLElement) {
		if u := e.Attr("href"); u != "" {
			info.PreviewImages = append(info.PreviewImages, e.Request.AbsoluteURL(u))
		}
	})

	if err = c.Visit(rawURL); err != nil {
		return
	}

	// Deduplicate genres & actors.
	info.Genres = dedupe(info.Genres)
	info.Actors = dedupe(info.Actors)

	return
}

func (db *JavDB) NormalizeMovieKeyword(keyword string) string {
	if number.IsSpecial(keyword) {
		// JavDB search is keyword-based, so special numbers (heyzo, fc2, ...)
		// are kept as-is and left to the search.
		return keyword
	}
	// JavDB searches accept both dashed and undashed numbers.
	return strings.ToUpper(number.Trim(keyword))
}

func (db *JavDB) SearchMovie(keyword string) (results []*model.MovieSearchResult, err error) {
	throttle.Wait(Name)
	c := db.ClonedCollector()

	c.OnXML(`//div[contains(@class,"movie-list")]//div[contains(@class,"item")]//a[contains(@class,"box")]`, func(e *colly.XMLElement) {
		homepage := e.Request.AbsoluteURL(e.Attr("href"))
		id, _ := db.ParseMovieIDFromURL(homepage)
		number := strings.TrimSpace(e.ChildText(`.//div[contains(@class,"video-title")]//strong`))
		title := strings.TrimSpace(strings.TrimPrefix(
			strings.TrimSpace(e.ChildText(`.//div[contains(@class,"video-title")]`)), number))
		var cover string
		css := e.ChildAttr(`.//div[contains(@class,"cover")]//img`, "src")
		if css != "" {
			cover = e.Request.AbsoluteURL(css)
		}
		result := &model.MovieSearchResult{
			ID:       id,
			Number:   number,
			Title:    title,
			Provider: db.Name(),
			Homepage: homepage,
			ThumbURL: cover,
			CoverURL: cover,
		}
		// Date lives in the first .meta line.
		dateText := strings.TrimSpace(e.ChildText(`.//div[contains(@class,"meta")]//span[1]`))
		result.ReleaseDate = parser.ParseDate(dateText)
		// Score: "4.41分" / "4.41分, 由443人評價".
		scoreText := strings.TrimSpace(e.ChildText(`.//div[contains(@class,"score")]//span[contains(@class,"value")]`))
		result.Score = parser.ParseScore(scoreText)
		if result.IsValid() {
			results = append(results, result)
		}
	})

	if err = c.Visit(fmt.Sprintf(searchURL, url.QueryEscape(keyword))); err != nil {
		return nil, err
	}
	return
}

func dedupe(in []string) (out []string) {
	seen := make(map[string]struct{}, len(in))
	for _, v := range in {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return
}

func orDefault(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

func normalizeNumber(n string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(n), "-", ""))
}

func init() {
	throttle.Set(Name, defaultCool)
	provider.Register(Name, New)
}
