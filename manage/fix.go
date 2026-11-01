package manage

import (
	"fmt"
	"log"
	"strings"
	"time"
)

// FixRequest is one batch repair.
type FixRequest struct {
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
		return fixRefresh(client, req.IDs, limit)
	case "rename_actors":
		return fixRenameActors(client, req.IDs, limit)
	default:
		return nil, fmt.Errorf("未知操作 %q（可用：refresh、rename_actors）", req.Action)
	}
}

// fixRefresh asks Emby to re-scrape the given items. ReplaceAllMetadata stays
// false on purpose: "replace all" is what previously overwrote local metadata
// with Japanese values.
func fixRefresh(client *EmbyClient, ids []string, limit int) (*FixResult, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("refresh 需要 ids")
	}
	if len(ids) > limit {
		ids = ids[:limit]
	}
	res := &FixResult{Action: "refresh", Total: len(ids)}
	for _, id := range ids {
		if err := client.RefreshMetadata(id); err != nil {
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
