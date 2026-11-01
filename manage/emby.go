package manage

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrNotConfigured is returned when the manage module is not configured.
var ErrNotConfigured = errors.New("manage: not configured")

// EmbyClient is a minimal Emby REST client for the manage module.
type EmbyClient struct {
	base   string
	apiKey string
	userID string
	client *http.Client
}

func newEmbyClient(c *Config) (*EmbyClient, error) {
	if c == nil || c.EmbyURL == "" || c.EmbyAPIKey == "" {
		return nil, ErrNotConfigured
	}
	return &EmbyClient{
		base:   strings.TrimRight(c.EmbyURL, "/"),
		apiKey: c.EmbyAPIKey,
		userID: c.EmbyUserID,
		client: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// resolveUserID returns the configured user id, or discovers an administrator
// id on first use. The user-scoped item endpoint is the only one that returns
// a DTO Emby accepts back on POST /Items/{id}.
func (c *EmbyClient) resolveUserID() string {
	if c.userID != "" {
		return c.userID
	}
	var users []struct {
		Id     string `json:"Id"`
		Policy struct {
			IsAdministrator bool `json:"IsAdministrator"`
		} `json:"Policy"`
	}
	if err := c.do(http.MethodGet, "/Users", nil, nil, &users); err != nil {
		return ""
	}
	for _, u := range users {
		if u.Policy.IsAdministrator {
			c.userID = u.Id
			return u.Id
		}
	}
	return ""
}

func (c *EmbyClient) do(method, path string, query url.Values, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	u := c.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequest(method, u, reader)
	if err != nil {
		return err
	}
	req.Header.Set("X-Emby-Token", c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("emby %s %s: http %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// Person is an Emby person entry used by the manage module.
type Person struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	HasPrimary  bool              `json:"has_primary"`
	HasOverview bool              `json:"has_overview"`
	ProviderIDs map[string]string `json:"provider_ids,omitempty"`
}

type embyItem struct {
	Id          string            `json:"Id"`
	Name        string            `json:"Name"`
	Type        string            `json:"Type"`
	ImageTags   map[string]string `json:"ImageTags"`
	ProviderIds map[string]string `json:"ProviderIds"`
	Genres      []string          `json:"Genres"`
	Studios     []struct {
	Overview    string            `json:"Overview"`
	Path        string            `json:"Path"`
	Studios     []struct {
		Name string `json:"Name"`
	} `json:"Studios"`
	People      []struct {
		Name string `json:"Name"`
		Type string `json:"Type"`
	} `json:"People"`
}

// GenresForAudit returns the "type" genres only. Studio / series / actor names
// that the provider packed into the genre list are removed, and Emby's separate
// Tags and Studios collections are never consulted: only 类型 is audited.
func (i embyItem) GenresForAudit() []string {
	skip := map[string]struct{}{}
	for _, s := range i.Studios {
		if s.Name != "" {
			skip[strings.ToLower(strings.TrimSpace(s.Name))] = struct{}{}
		}
	}
	for _, p := range i.People {
		if p.Name != "" {
			skip[strings.ToLower(strings.TrimSpace(p.Name))] = struct{}{}
		}
	}
	prefixes := []string{"片商:", "片商：", "系列:", "系列：", "厂商:", "厂商：", "studio:", "series:", "maker:", "label:"}
	out := make([]string, 0, len(i.Genres))
	for _, g := range i.Genres {
		name := strings.TrimSpace(g)
		if name == "" {
			continue
		}
		low := strings.ToLower(name)
		skipIt := false
		for _, p := range prefixes {
			if strings.HasPrefix(low, p) {
				skipIt = true
				break
			}
		}
		if skipIt {
			continue
		}
		if _, ok := skip[low]; ok {
			continue
		}
		out = append(out, name)
	}
	return out
}

// Actors returns the actor names attached to the item.
func (i embyItem) Actors() []string {
	out := make([]string, 0, len(i.People))
	for _, p := range i.People {
		if p.Type == "Actor" && p.Name != "" {
			out = append(out, p.Name)
		}
	}
	return out
}

// Number extracts a display label from MetaTube provider ids (e.g.
// "FANZA:1start00015" -> "1START00015"), falling back to the name prefix.
func (i embyItem) Number() string {
	for _, key := range []string{"MetaTube", "Fanza", "FANZA"} {
		if v, ok := i.ProviderIds[key]; ok && v != "" {
			if idx := strings.LastIndex(v, ":"); idx >= 0 {
				return strings.ToUpper(v[idx+1:])
			}
			return strings.ToUpper(v)
		}
	}
	if fields := strings.Fields(i.Name); len(fields) > 0 {
		return strings.ToUpper(fields[0])
	}
	return ""
}

func (i embyItem) toPerson() Person {
	p := Person{
		ID:          i.Id,
		Name:        i.Name,
		HasOverview: strings.TrimSpace(i.Overview) != "",
		ProviderIDs: i.ProviderIds,
	}
	_, p.HasPrimary = i.ImageTags["Primary"]
	return p
}

// FetchPersons returns persons, optionally scoped to the given libraries.
func (c *EmbyClient) FetchPersons(libraries []string) ([]Person, error) {
	var persons []Person
	if len(libraries) == 0 {
		// Server-wide persons (Emby returns a paged object).
		start := 0
		for {
			var page struct {
				Items            []embyItem `json:"Items"`
				TotalRecordCount int        `json:"TotalRecordCount"`
			}
			q := url.Values{}
			q.Set("Recursive", "true")
			q.Set("Fields", "ProviderIds,Overview")
			q.Set("StartIndex", fmt.Sprint(start))
			q.Set("Limit", "2000")
			if err := c.do(http.MethodGet, "/Persons", q, nil, &page); err != nil {
				return nil, err
			}
			for _, i := range page.Items {
				persons = append(persons, i.toPerson())
			}
			start += len(page.Items)
			if len(page.Items) == 0 || (page.TotalRecordCount > 0 && start >= page.TotalRecordCount) {
				break
			}
		}
		return persons, nil
	}
	seen := map[string]struct{}{}
	for _, lib := range libraries {
		var page struct {
			Items            []embyItem `json:"Items"`
			TotalRecordCount int        `json:"TotalRecordCount"`
		}
		start := 0
		for {
			q := url.Values{}
			q.Set("ParentId", lib)
			q.Set("IncludeItemTypes", "Person")
			q.Set("Recursive", "true")
			q.Set("Fields", "ProviderIds,Overview")
			q.Set("StartIndex", fmt.Sprint(start))
			q.Set("Limit", "1000")
			if err := c.do(http.MethodGet, "/Items", q, nil, &page); err != nil {
				return nil, err
			}
			for _, i := range page.Items {
				if _, ok := seen[i.Id]; ok {
					continue
				}
				seen[i.Id] = struct{}{}
				persons = append(persons, i.toPerson())
			}
			start += len(page.Items)
			if len(page.Items) == 0 || start >= page.TotalRecordCount {
				break
			}
		}
	}
	return persons, nil
}

// CountPersonMovies returns how many movies link to the person.
func (c *EmbyClient) CountPersonMovies(personID string) (int, error) {
	var page struct {
		TotalRecordCount int `json:"TotalRecordCount"`
	}
	q := url.Values{}
	q.Set("PersonIds", personID)
	q.Set("Recursive", "true")
	q.Set("IncludeItemTypes", "Movie")
	q.Set("Limit", "0")
	if err := c.do(http.MethodGet, "/Items", q, nil, &page); err != nil {
		return 0, err
	}
	return page.TotalRecordCount, nil
}

// ListPersonMovies returns the movies linking to the person.
func (c *EmbyClient) ListPersonMovies(personID string) ([]embyItem, error) {
	var page struct {
		Items []embyItem `json:"Items"`
	}
	q := url.Values{}
	q.Set("PersonIds", personID)
	q.Set("Recursive", "true")
	q.Set("IncludeItemTypes", "Movie")
	q.Set("Fields", "ProviderIds,Overview")
	q.Set("Limit", "10000")
	if err := c.do(http.MethodGet, "/Items", q, nil, &page); err != nil {
		return nil, err
	}
	return page.Items, nil
}

// PersonExists checks whether a person item still exists.
func (c *EmbyClient) PersonExists(personID string) (bool, error) {
	var item embyItem
	err := c.do(http.MethodGet, "/Items/"+url.PathEscape(personID), nil, nil, &item)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return false, nil
		}
		return false, err
	}
	return item.Id != "", nil
}

// UploadPersonImage uploads a primary image for the person.
func (c *EmbyClient) UploadPersonImage(personID string, image []byte, contentType string) error {
	q := url.Values{}
	q.Set("api_key", c.apiKey)
	req, err := http.NewRequest(http.MethodPost,
		c.base+"/Items/"+url.PathEscape(personID)+"/Images/Primary?"+q.Encode(),
		bytes.NewReader([]byte(base64.StdEncoding.EncodeToString(image))))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return fmt.Errorf("upload image: http %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return nil
}

// DownloadURL fetches binary content (e.g. an avatar image).
func (c *EmbyClient) DownloadURL(rawURL string) ([]byte, string, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("download %s: http %d", rawURL, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, "", err
	}
	ct := resp.Header.Get("Content-Type")
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	return data, ct, nil
}
