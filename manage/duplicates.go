package manage

import (
	"fmt"
	"log"
	"sort"
)

// DuplicateActor is one Emby person inside a duplicate group.
type DuplicateActor struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	MovieCount int    `json:"movie_count"`
	HasAvatar  bool   `json:"has_avatar"`
}

// DuplicateGroup groups Emby persons that the actor mapping resolves to the
// same canonical actor.
type DuplicateGroup struct {
	MappingID      string           `json:"mapping_id"`
	Recommended    string           `json:"recommended_name"`
	TmdbID         string           `json:"tmdb_id,omitempty"`
	Actors         []DuplicateActor `json:"actors"`
	TotalMovies    int              `json:"total_movies"`
}

// DetectDuplicates scans Emby persons and groups duplicates by the actor
// mapping table. This is the same detection model as avdb's
// check_actor_duplicates: mapping index x Emby persons.
func DetectDuplicates() (map[string]any, error) {
	c := getConfig()
	if c == nil {
		return nil, ErrNotConfigured
	}
	am := ActorMapping()
	if am == nil {
		return nil, fmt.Errorf("演员映射表未加载，请先在「映射表」中完成同步")
	}
	client, err := newEmbyClient(c)
	if err != nil {
		return nil, err
	}
	persons, err := client.FetchPersons(c.EmbyLibraryIDs)
	if err != nil {
		return nil, fmt.Errorf("拉取 Emby 演员失败: %w", err)
	}
	log.Printf("[manage] duplicates: fetched %d persons", len(persons))

	// Group persons by mapping entry (match on any alias / canonical name).
	// Entries with the same canonical name are the same actor.
	groups := map[string][]Person{}
	matched := 0
	for _, p := range persons {
		if p.Name == "" {
			continue
		}
		entry, ok := am.Lookup(p.Name)
		if !ok {
			continue
		}
		matched++
		groups[entry.Name] = append(groups[entry.Name], p)
	}

	var out []DuplicateGroup
	for name, members := range groups {
		if len(members) < 2 {
			continue
		}
		entry, _ := am.Lookup(members[0].Name)
		group := DuplicateGroup{
			MappingID:   name,
			Recommended: entry.Name,
			TmdbID:      entry.TmdbID,
		}
		for _, m := range members {
			count, err := client.CountPersonMovies(m.ID)
			if err != nil {
				log.Printf("[manage] duplicates: count movies for %s(%s) failed: %v", m.Name, m.ID, err)
			}
			group.Actors = append(group.Actors, DuplicateActor{
				ID:         m.ID,
				Name:       m.Name,
				MovieCount: count,
				HasAvatar:  m.HasPrimary,
			})
			group.TotalMovies += count
		}
		// Recommendation: keep the person with the most movies; prefer one
		// that already has an avatar on ties (same UX as avdb).
		sort.SliceStable(group.Actors, func(i, j int) bool {
			a, b := group.Actors[i], group.Actors[j]
			if a.MovieCount != b.MovieCount {
				return a.MovieCount > b.MovieCount
			}
			return a.HasAvatar && !b.HasAvatar
		})
		out = append(out, group)
	}
	log.Printf("[manage] duplicates: matched=%d groups=%d duplicate_groups=%d",
		matched, len(groups), len(out))
	return map[string]any{
		"scanned": len(persons),
		"matched": matched,
		"groups":  out,
	}, nil
}

// ResolveDuplicate merges duplicate persons: every movie linked to a source
// person is re-bound to the target person via the AvdbMagicTools plugin, and
// the (now empty) source persons are deleted and verified absent.
func ResolveDuplicate(targetID string, sourceIDs []string) (map[string]any, error) {
	c := getConfig()
	if c == nil {
		return nil, ErrNotConfigured
	}
	if targetID == "" || len(sourceIDs) == 0 {
		return nil, fmt.Errorf("必须选择保留的演员")
	}
	client, err := newEmbyClient(c)
	if err != nil {
		return nil, err
	}
	if ok, err := client.HasMagicToolsPlugin(); err != nil || !ok {
		return nil, fmt.Errorf("Emby 未安装 Avdb Magic Tools 插件，无法安全合并（请先安装插件）")
	}

	// Collect movies of all source persons.
	movieIDs := map[string]struct{}{}
	for _, sid := range sourceIDs {
		items, err := client.ListPersonMovies(sid)
		if err != nil {
			return nil, fmt.Errorf("列出演员 %s 的影片失败: %w", sid, err)
		}
		for _, it := range items {
			movieIDs[it.Id] = struct{}{}
		}
	}

	transferred, failed := 0, 0
	var failures []string
	for movieID := range movieIDs {
		if err := client.TransferPerson(movieID, sourceIDs, targetID); err != nil {
			failed++
			failures = append(failures, fmt.Sprintf("影片 %s: %v", movieID, err))
			log.Printf("[manage] resolve: transfer failed movie=%s: %v", movieID, err)
			continue
		}
		transferred++
	}
	// Delete emptied source persons (even if some transfers failed, avdb
	// reports the failures; deletion only for sources with no remaining
	// links is safer — probe count first).
	deleted := 0
	var deleteFailures []string
	for _, sid := range sourceIDs {
		if count, err := client.CountPersonMovies(sid); err == nil && count > 0 {
			deleteFailures = append(deleteFailures, fmt.Sprintf("演员 %s 仍有 %d 部关联影片，跳过删除", sid, count))
			continue
		}
		if err := client.DeletePerson(sid); err != nil {
			deleteFailures = append(deleteFailures, fmt.Sprintf("删除 %s 失败: %v", sid, err))
			continue
		}
		if ok, err := client.PersonExists(sid); err == nil && ok {
			deleteFailures = append(deleteFailures, fmt.Sprintf("演员 %s 删除后仍存在", sid))
			continue
		}
		deleted++
	}
	log.Printf("[manage] resolve: movies=%d transferred=%d failed=%d deleted=%d",
		len(movieIDs), transferred, failed, deleted)
	result := map[string]any{
		"movies":      len(movieIDs),
		"transferred": transferred,
		"failed":      failed,
		"deleted":     deleted,
	}
	if len(failures) > 0 {
		result["failures"] = failures
	}
	if len(deleteFailures) > 0 {
		result["delete_failures"] = deleteFailures
	}
	return result, nil
}
