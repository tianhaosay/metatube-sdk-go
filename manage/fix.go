package manage

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// FixRequest is one batch repair.
type FixRequest struct {
	// ReplaceAll lets provider output overwrite existing values (needed to
	// clear a summary or replace a stale Japanese title).
	ReplaceAll bool `json:"replace_all"`
	// Action is "refresh" (re-scrape the given items) or "retitle" (rewrite the
	// title from the backend).
	Action string   `json:"action"`
	IDs    []string `json:"ids"`
	// Limit caps how many items this call touches (default 50, max 200).
	Limit int `json:"limit"`
}

// FixResult reports what a batch repair did.
type FixResult struct {
	Action  string   `json:"action"`
	Total   int      `json:"total"`
	OK      int      `json:"ok"`
	Failed  int      `json:"failed"`
	Details []string `json:"details,omitempty"`
}

// Fix runs a batch repair.
func Fix(req FixRequest) (*FixResult, error) {
	c := getConfig()
	if c == nil {
		return nil, ErrNotConfigured
	}
	client, err := newEmbyClient(c)
	if err != nil {
		return nil, err
	}
	limit := req.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	switch strings.ToLower(strings.TrimSpace(req.Action)) {
	case "refresh":
		return fixRefresh(client, req.IDs, limit, req.ReplaceAll)
	case "retitle":
		return fixRetitle(client, req.IDs, limit)
	default:
		return nil, fmt.Errorf("未知操作 %q（可用：refresh、retitle）", req.Action)
	}
}

// fixRefresh asks Emby to re-scrape the given items; replaceAll decides whether
// provider output overwrites the fields Emby already has.
func fixRefresh(client *EmbyClient, ids []string, limit int, replaceAll bool) (*FixResult, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("refresh 需要 ids")
	}
	if len(ids) > limit {
		ids = ids[:limit]
	}
	res := &FixResult{Action: "refresh", Total: len(ids)}
	for _, id := range ids {
		if err := client.RefreshMetadata(id, replaceAll); err != nil {
			res.Failed++
			res.Details = append(res.Details, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		res.OK++
		time.Sleep(embyWriteDelay)
	}
	log.Printf("[manage] fix refresh: %d/%d ok", res.OK, res.Total)
	return res, nil
}

// fixRetitle rewrites the title of the given items from the backend; a manually
// edited marker prefix is kept.
func fixRetitle(client *EmbyClient, ids []string, limit int) (*FixResult, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("retitle 需要 ids")
	}
	if len(ids) > limit {
		ids = ids[:limit]
	}
	res := &FixResult{Action: "retitle", Total: len(ids)}
	for _, id := range ids {
		name, err := retitleOne(client, id)
		if err != nil {
			res.Failed++
			res.Details = append(res.Details, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		res.OK++
		res.Details = append(res.Details, name)
		time.Sleep(embyWriteDelay)
	}
	log.Printf("[manage] fix retitle: %d/%d ok", res.OK, res.Total)
	return res, nil
}

// retitleOne resolves the Chinese title for one item and writes it back.
func retitleOne(client *EmbyClient, id string) (string, error) {
	dto, err := client.itemDTO(id)
	if err != nil {
		return "", err
	}
	name, _ := dto["Name"].(string)
	code, marker := splitTitle(name)
	providerID := ""
	if ids, ok := dto["ProviderIds"].(map[string]any); ok {
		for k, v := range ids {
			if strings.EqualFold(k, "MetaTube") {
				providerID, _ = v.(string)
			}
		}
	}
	if providerID == "" {
		return "", fmt.Errorf("条目没有 MetaTube 番号，无法定位")
	}
	parts := strings.SplitN(providerID, ":", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("番号格式异常: %s", providerID)
	}
	fresh, err := fetchBackendMovie(parts[0], parts[1])
	if err != nil {
		return "", err
	}
	newTitle := strings.TrimSpace(fresh.Title)
	if newTitle == "" {
		return "", fmt.Errorf("后端没有返回标题")
	}
	newName := strings.TrimSpace(strings.Join(strings.Fields(code+" "+marker+" "+newTitle), " "))
	if newName == strings.TrimSpace(name) {
		return newName, nil
	}
	if err := client.RenameItem(id, newName); err != nil {
		return "", err
	}
	return newName, nil
}

// splitTitle separates a manual prefix / code from the descriptive title:
// "REAL-885 （夸克）日本語..." -> ("REAL-885", "（夸克）").
func splitTitle(name string) (code, marker string) {
	s := strings.TrimSpace(name)
	// Keep a manual marker such as "（夸克）" visible so copies stay
	// distinguishable, whether it sits before or after the code.
	if idx := strings.IndexAny(s, "（("); idx >= 0 {
		rest := s[idx:]
		if end := strings.IndexAny(rest, "）)"); end > 0 && end <= 8 {
			marker = rest[:end+1]
			s = strings.TrimSpace(strings.Replace(s, marker, "", 1))
		}
	}
	if idx := strings.Index(s, " "); idx > 0 {
		return s[:idx], marker
	}
	return s, marker
}

// embyWriteDelay spaces out consecutive writes to Emby.
const embyWriteDelay = 200 * time.Millisecond

// backendURL is the address of this server, used to read freshly scraped
// metadata from inside the same process.
var backendURL = "http://127.0.0.1:8080"

// SetBackendURL lets the command package publish the real listen address.
func SetBackendURL(u string) {
	if strings.TrimSpace(u) != "" {
		backendURL = strings.TrimRight(u, "/")
	}
}

type backendMovie struct {
	Title string `json:"title"`
}

// fetchBackendMovie asks this server for the current metadata of one provider id.
func fetchBackendMovie(provider, id string) (*backendMovie, error) {
	endpoint := backendURL + "/v1/movies/" + url.PathEscape(provider) + "/" + url.PathEscape(id)
	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Get(endpoint)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("后端返回 %d", resp.StatusCode)
	}
	var payload struct {
		Data backendMovie `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return &payload.Data, nil
}
