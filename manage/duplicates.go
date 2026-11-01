package manage

import (
	"fmt"
	"log"
	"sort"
	"time"
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

// ResolveDuplicate merges duplicate persons natively, without any Emby plugin:
//
//  1. re-point every movie of the source persons at the surviving person
//     (read the item DTO, rewrite its People list, write it back);
//  2. try to delete the now-empty source items (Emby only prunes virtual
//     items during a scan, so this is best-effort);
//  3. kick off a library scan, which is what actually removes the orphans.
//
// Everything is idempotent: re-running reports "skipped" for movies that are
// already pointing at the target.
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
	targetName, err := client.ItemName(targetID)
	if err != nil {
		return nil, fmt.Errorf("读取保留演员失败: %w", err)
	}

	// Collect every movie linked to any source person.
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

	transferred, skipped, failed := 0, 0, 0
	var failures []string
	for movieID := range movieIDs {
		changed, err := client.MovePeopleLinks(movieID, sourceIDs, targetID, targetName)
		if err != nil {
			failed++
			failures = append(failures, fmt.Sprintf("影片 %s: %v", movieID, err))
			log.Printf("[manage] resolve: move links failed movie=%s: %v", movieID, err)
			continue
		}
		if changed {
			transferred++
		} else {
			skipped++
		}
		time.Sleep(embyWriteDelay)
	}

	// Best-effort delete of the emptied source items.
	deleted := 0
	var notes []string
	for _, sid := range sourceIDs {
		if count, err := client.CountPersonMovies(sid); err == nil && count > 0 {
			notes = append(notes, fmt.Sprintf("演员 %s 仍有 %d 部关联影片，保留", sid, count))
			continue
		}
		if err := client.DeleteItem(sid); err != nil {
			notes = append(notes, fmt.Sprintf("删除 %s 失败: %v", sid, err))
			continue
		}
		deleted++
	}

	// The scan is what removes the orphan person entries.
	scanStarted, scanErr := client.TriggerLibraryScan()
	if scanErr != nil {
		notes = append(notes, "触发媒体库扫描失败: "+scanErr.Error())
	}

	log.Printf("[manage] resolve: target=%s movies=%d transferred=%d skipped=%d failed=%d deleted=%d scan=%v",
		targetName, len(movieIDs), transferred, skipped, failed, deleted, scanStarted)

	result := map[string]any{
		"target":      targetName,
		"movies":      len(movieIDs),
		"transferred": transferred,
		"skipped":     skipped,
		"failed":      failed,
		"deleted":     deleted,
		"scan_started": scanStarted,
	}
	if len(failures) > 0 {
		result["failures"] = failures
	}
	if len(notes) > 0 {
		result["notes"] = notes
	}
	return result, nil
}