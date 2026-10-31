package javdb

import (
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/gocolly/colly/v2"

	"github.com/metatube-community/metatube-sdk-go/common/throttle"
	"github.com/metatube-community/metatube-sdk-go/model"
	"github.com/metatube-community/metatube-sdk-go/provider"
)

var (
	_ provider.ActorProvider = (*JavDB)(nil)
	_ provider.ActorSearcher = (*JavDB)(nil)
)

func (db *JavDB) NormalizeActorID(id string) string {
	return strings.TrimSpace(id)
}

func (db *JavDB) ParseActorIDFromURL(rawURL string) (string, error) {
	homepage, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	return path.Base(homepage.Path), nil
}

// GetActorInfoByID gets actor info by JavDB actor ID (the URL slug, e.g.
// "kp5J").
func (db *JavDB) GetActorInfoByID(id string) (info *model.ActorInfo, err error) {
	return db.GetActorInfoByURL(fmt.Sprintf(actorURL, db.NormalizeActorID(id)))
}

func (db *JavDB) GetActorInfoByURL(rawURL string) (info *model.ActorInfo, err error) {
	throttle.Wait(Name)
	id, err := db.ParseActorIDFromURL(rawURL)
	if err != nil {
		return
	}

	info = &model.ActorInfo{
		ID:       id,
		Provider: db.Name(),
		Homepage: rawURL,
		Aliases:  []string{},
		Images:   []string{},
	}

	c := db.ClonedCollector()

	// Name: <span class="actor-section-name">NAME1, NAME2</span>
	c.OnXML(`//span[contains(@class,"actor-section-name")]`, func(e *colly.XMLElement) {
		if info.Name == "" {
			names := strings.Split(strings.TrimSpace(e.Text), ",")
			info.Name = strings.TrimSpace(names[0])
			for _, alias := range names[1:] {
				if alias = strings.TrimSpace(alias); alias != "" {
					info.Aliases = append(info.Aliases, alias)
				}
			}
		}
	})

	// Previous names: <span class="section-meta">alias1, alias2</span>
	c.OnXML(`//h2[contains(@class,"title")]//span[contains(@class,"section-meta")]`, func(e *colly.XMLElement) {
		text := strings.TrimSpace(e.Text)
		// Skip video count lines like "493 部影片".
		if text == "" || strings.Contains(text, "部影片") {
			return
		}
		for _, alias := range strings.Split(text, ",") {
			if alias = strings.TrimSpace(alias); alias != "" {
				info.Aliases = append(info.Aliases, alias)
			}
		}
	})

	// Avatar: <span class="avatar" style="background-image: url(...)"/>
	c.OnXML(`//div[contains(@class,"actor-avatar")]//span[contains(@class,"avatar")]`, func(e *colly.XMLElement) {
		style := e.Attr("style")
		if start := strings.Index(style, "url("); start >= 0 {
			u := style[start+4:]
			if end := strings.Index(u, ")"); end >= 0 {
				u = strings.Trim(strings.TrimSpace(u[:end]), "'\"")
				if u != "" {
					info.Images = append(info.Images, e.Request.AbsoluteURL(u))
				}
			}
		}
	})

	err = c.Visit(rawURL)
	if err != nil {
		return
	}
	if info.Name == "" {
		return nil, provider.ErrInfoNotFound
	}
	info.Aliases = dedupe(info.Aliases)
	info.Images = dedupe(info.Images)
	return
}

// SearchActor searches actors by keyword.
func (db *JavDB) SearchActor(keyword string) (results []*model.ActorSearchResult, err error) {
	throttle.Wait(Name)
	c := db.ClonedCollector()

	c.OnXML(`//div[contains(@class,"actor-list")]//div[contains(@class,"item")]//a[contains(@class,"box")]`, func(e *colly.XMLElement) {
		homepage := e.Request.AbsoluteURL(e.Attr("href"))
		id, _ := db.ParseActorIDFromURL(homepage)
		name := strings.TrimSpace(e.ChildText(`.//strong`))
		var avatar string
		if style := e.ChildAttr(`.//span[contains(@class,"avatar")]`, "style"); style != "" {
			if start := strings.Index(style, "url("); start >= 0 {
				u := style[start+4:]
				if end := strings.Index(u, ")"); end >= 0 {
					u = strings.Trim(strings.TrimSpace(u[:end]), "'\"")
					avatar = e.Request.AbsoluteURL(u)
				}
			}
		}
		result := &model.ActorSearchResult{
			ID:       id,
			Name:     name,
			Provider: db.Name(),
			Homepage: homepage,
			Images:   []string{},
		}
		if avatar != "" {
			result.Images = append(result.Images, avatar)
		}
		if result.IsValid() {
			results = append(results, result)
		}
	})

	if err = c.Visit(fmt.Sprintf(actorSearchURL, url.QueryEscape(keyword))); err != nil {
		return nil, err
	}
	return
}
