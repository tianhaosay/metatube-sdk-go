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
	// Action: "refresh" (re-scrape given items) or "rename_actors"
	// (canonicalize actor names through the mapping table).
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

const (
	maxFixBatch    = 200
	embyWriteDelay = 60 * time.Millisecond
)

// Fix runs a batch repair. Every action is idempotent and safe to re-run.
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
	if limit <= 0 {
		limit = 50
	}
	if limit > maxFixBatch {
		limit = maxFixBatch
	}

	switch strings.ToLower(strings.TrimSpace(req.Action)) {
	case "refresh":
		return fixRefresh(client, req.IDs, limit, req.ReplaceAll)
	case "retitle":
		return fixRetitle(client, req.IDs, limit)
	case "rename_actors":
		return fixRenameActors(client, req.IDs, limit)
	default:
		return nil, fmt.Errorf("未知操作 %q（可用：refresh、rename_actors）", req.Action)
	}
}

// fixRefresh asks Emby to re-scrape the given items. replaceAll decides whether
// the provider output overwrites existing values; without it Emby keeps every
// field that is already set, so stale Japanese titles stay put.
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

// fixRenameActors renames Emby persons to their canonical Chinese name using
// the mapping table. This needs no Emby plugin and fixes the actor list in
// place, which is what users actually look at.
func fixRenameActors(client *EmbyClient, ids []string, limit int) (*FixResult, error) {
	am := ActorMapping()
	if am == nil {
		return nil, fmt.Errorf("演员映射表未加载，请先同步映射表")
	}

	persons, err := client.FetchPersons(getConfig().EmbyLibraryIDs)
	if err != nil {
		return nil, fmt.Errorf("拉取 Emby 演员失败: %w", err)
	}
	if len(ids) > 0 {
		want := make(map[string]struct{}, len(ids))
		for _, id := range ids {
			want[id] = struct{}{}
		}
		filtered := persons[:0]
		for _, p := range persons {
			if _, ok := want[p.ID]; ok {
				filtered = append(filtered, p)
			}
		}
		persons = filtered
	}

	res := &FixResult{Action: "rename_actors"}
	for _, p := range persons {
		if res.Total >= limit {
			break
		}
		if !kanaRe.MatchString(p.Name) {
			continue // already Chinese (or Latin); nothing to do
		}
		entry, ok := am.Lookup(p.Name)
		if !ok || entry.Name == "" || entry.Name == p.Name {
			continue // not in the mapping table: leave for human review
		}
		if kanaRe.MatchString(entry.Name) {
			// The table's canonical name is itself Japanese (bad/ambiguous
			// source data). Renaming would not improve anything, so skip and
			// leave the person for manual review.
			continue
		}
		res.Total++
		if err := client.RenameItem(p.ID, entry.Name); err != nil {
			res.Failed++
			res.Details = append(res.Details, fmt.Sprintf("%s (%s): %v", p.Name, p.ID, err))
			continue
		}
		res.OK++
		time.Sleep(embyWriteDelay)
	}
	log.Printf("[manage] fix rename_actors: %d/%d ok, %d untouched", res.OK, res.Total, len(persons))
	return res, nil
}

// fixRetitle rewrites the title from the backend whenever Emby refused to take
// it during a refresh. A manually edited name (for example a "（夸克）" prefix)
// is kept as a prefix, everything after it is replaced with the Chinese title.
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
// "（夸克）REAL-885 日本語..." -> ("（夸克）REAL-885", "日本語...").
func splitTitle(name string) (code, marker string) {
	s := strings.TrimSpace(name)
	// A manual marker such as "（夸克）" may sit before or after the code; keep
	// it visible so duplicate copies stay distinguishable.
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
// backendURL is the address of this server, used when a repair needs to read
// freshly scraped metadata (the console runs inside the same process).
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