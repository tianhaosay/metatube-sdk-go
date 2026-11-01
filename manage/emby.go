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
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	HasPrimary   bool              `json:"has_primary"`
	HasOverview  bool              `json:"has_overview"`
	MovieCount   int               `json:"movie_count"`
	ProviderIDs  map[string]string `json:"provider_ids,omitempty"`
}

type embyItem struct {
	Id          string            `json:"Id"`
	Name        string            `json:"Name"`
	Type        string            `json:"Type"`
	ImageTags   map[string]string `json:"ImageTags"`
	ProviderIds map[string]string `json:"ProviderIds"`
	Genres      []string          `json:"Genres"`
	Overview    string            `json:"Overview"`
	Path        string            `json:"Path"`
	People      []struct {
		Name string `json:"Name"`
		Type string `json:"Type"`
	} `json:"People"`
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

// HasMagicToolsPlugin reports whether the AvdbMagicTools plugin is installed.
func (c *EmbyClient) HasMagicToolsPlugin() (bool, error) {
	var plugins []struct {
		Name string `json:"Name"`
		Id   string `json:"Id"`
	}
	if err := c.do(http.MethodGet, "/Plugins", nil, nil, &plugins); err != nil {
		return false, err
	}
	for _, p := range plugins {
		n := strings.ToLower(strings.ReplaceAll(p.Name, " ", ""))
		id := strings.ToLower(p.Id)
		if strings.Contains(n, "magictools") ||
			strings.ReplaceAll(id, "-", "") == "28abc9396af744d199d37bc5e2d52610" {
			return true, nil
		}
	}
	return false, nil
}

// TransferPerson moves an item's person links from sources to target via the
// AvdbMagicTools plugin (ID-preserving, like avdb).
func (c *EmbyClient) TransferPerson(itemID string, sourceIDs []string, targetID string) error {
	sourceNums := make([]int, 0, len(sourceIDs))
	for _, id := range sourceIDs {
		n := 0
		if _, err := fmt.Sscanf(id, "%d", &n); err != nil || n <= 0 {
			return fmt.Errorf("invalid numeric person id: %q", id)
		}
		sourceNums = append(sourceNums, n)
	}
	targetNum := 0
	if _, err := fmt.Sscanf(targetID, "%d", &targetNum); err != nil || targetNum <= 0 {
		return fmt.Errorf("invalid numeric target person id: %q", targetID)
	}
	q := url.Values{}
	if c.userID != "" {
		q.Set("UserId", c.userID)
	}
	return c.do(http.MethodPost,
		fmt.Sprintf("/Plugins/AvdbMagicTools/Items/%s/TransferPerson", url.PathEscape(itemID)),
		q,
		map[string]any{"SourcePersonIds": sourceNums, "TargetPersonId": targetNum},
		nil)
}

// DeletePerson removes a person via the AvdbMagicTools plugin.
func (c *EmbyClient) DeletePerson(personID string) error {
	q := url.Values{}
	if c.userID != "" {
		q.Set("UserId", c.userID)
	}
	return c.do(http.MethodPost,
		fmt.Sprintf("/Plugins/AvdbMagicTools/Items/%s/DeletePerson", url.PathEscape(personID)),
		q, nil, nil)
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