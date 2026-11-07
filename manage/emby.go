package manage

import (
	"bytes"
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

