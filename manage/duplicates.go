package manage

import (
	"fmt"
	"log"
	"sort"
	"time"
)

// MovieRef is one movie linked to an actor, shown as merge evidence so a
// human can tell whether two entries really are the same person.
type MovieRef struct {
	ID     string `json:"id"`
	Number string `json:"number"`
	Title  string `json:"title"`
}

// DuplicateActor is one Emby person inside a duplicate group.
type DuplicateActor struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	MovieCount int        `json:"movie_count"`
	HasAvatar  bool       `json:"has_avatar"`
	Movies     []MovieRef `json:"movies,omitempty"`
}

// DuplicateGroup groups Emby persons that the actor mapping resolves to the
// same canonical actor.
type DuplicateGroup struct {
	MappingID   string           `json:"mapping_id"`
	Recommended string           `json:"recommended_name"`
	TmdbID      string           `json:"tmdb_id,omitempty"`
	Actors      []DuplicateActor `json:"actors"`
	TotalMovies int              `json:"total_movies"`
	Ignored     bool             `json:"ignored,omitempty"`
}

const maxEvidenceMovies = 100

// DetectDuplicates scans Emby persons and groups duplicates by the actor
// mapping table. When includeMovies is true each entry also carries its movie
// list (number + title) so the operator can verify before merging.
func DetectDuplicates(includeMovies bool) (map[string]any, error) {
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

	groups := map[string][]Person{}
	// Only people credited as Actor are actresses; directors and crew have
	// no reason to be merged here.
	actorCache := map[string]bool{}
	matched := 0
	for _, p := range persons {
		if p.Name == "" {
			continue
		}
		entry, ok := am.Lookup(p.Name)
		if !ok {
			continue
		}
		if _, seen := actorCache[p.ID]; !seen {
			actorCache[p.ID] = client.PersonActed(p.ID)
		}
		if !actorCache[p.ID] {
			continue
		}
		matched++
		groups[entry.Name] = append(groups[entry.Name], p)
	}

	out := make([]DuplicateGroup, 0, len(groups))
	ignored := 0
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
			actor := DuplicateActor{ID: m.ID, Name: m.Name, HasAvatar: m.HasPrimary}
			if includeMovies {
				items, err := client.ListPersonMovies(m.ID)
				if err != nil {
					log.Printf("[manage] duplicates: list movies for %s(%s) failed: %v", m.Name, m.ID, err)
				}
				actor.MovieCount = len(items)
				for i, it := range items {
					if i >= maxEvidenceMovies {
						break
					}
					actor.Movies = append(actor.Movies, MovieRef{
						ID:     it.Id,
						Number: it.Number(),
						Title:  it.Name,
					})
				}
			} else {
				if count, err := client.CountPersonMovies(m.ID); err == nil {
					actor.MovieCount = count
				}
			}
			group.Actors = append(group.Actors, actor)
			group.TotalMovies += actor.MovieCount
		}
		// Keep the person with the most movies; prefer one with an avatar.
		sort.SliceStable(group.Actors, func(i, j int) bool {
			a, b := group.Actors[i], group.Actors[j]
			if a.MovieCount != b.MovieCount {
				return a.MovieCount > b.MovieCount
			}
			return a.HasAvatar && !b.HasAvatar
		})
		if isIgnored(name) {
			group.Ignored = true
			ignored++
			continue
		}
		out = append(out, group)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].TotalMovies > out[j].TotalMovies
	})
	log.Printf("[manage] duplicates: matched=%d groups=%d ignored=%d", matched, len(out), ignored)
	return map[string]any{
		"scanned": len(persons),
		"matched": matched,
		"groups":  out,
		"ignored": ignored,
	}, nil
}

// ResolveDuplicate merges duplicate persons natively, without any Emby plugin:
//
//  1. re-point every movie of the source persons at the surviving person;
//  2. verify each movie really lost the source and gained the target;
//  3. try to delete the emptied source items (a library scan is what actually
//     prunes them);
//  4. record the original People lists so the merge can be rolled back.
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

	rec := &mergeRecord{Time: time.Now().Format(time.RFC3339)}
	group, ok, failed, notes := mergeOneGroup(client, targetID, targetName, sourceIDs)
	if len(group.Movies) > 0 {
		rec.Groups = append(rec.Groups, group)
	}
	logPath, _ := saveMergeRecord(rec)

	deleted := 0
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
	scanStarted, scanErr := client.TriggerLibraryScan()
	if scanErr != nil {
		notes = append(notes, "触发媒体库扫描失败: "+scanErr.Error())
	}

	res := map[string]any{
		"target":       targetName,
		"movies":       len(group.Movies),
		"transferred":  ok,
		"failed":       failed,
		"deleted":      deleted,
		"scan_started": scanStarted,
		"rollback_log": logPath,
	}
	if len(group.Unverified) > 0 {
		res["unverified"] = group.Unverified
	}
	if len(notes) > 0 {
		res["notes"] = notes
	}
	return res, nil
}
