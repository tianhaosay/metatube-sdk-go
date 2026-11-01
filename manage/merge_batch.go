package manage

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// mergeRecord is the rollback log of one merge batch: for every movie it
// stores the original People list, so a wrong merge can be undone exactly.
type mergeRecord struct {
	Time   string        `json:"time"`
	Groups []mergeGroup  `json:"groups"`
}

type mergeGroup struct {
	Target     string        `json:"target"`
	TargetName string        `json:"target_name"`
	Sources    []string      `json:"sources"`
	Movies     []movieBackup `json:"movies"`
	// Unverified lists movies whose link could not be confirmed after the
	// update (Emby ignored or re-resolved the People entry).
	Unverified []string `json:"unverified,omitempty"`
}

type movieBackup struct {
	ID     string `json:"id"`
	People any    `json:"people"`
}

func mergeLogPath() string {
	c := getConfig()
	if c == nil || c.ActorMappingPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(c.ActorMappingPath), "merge_log.json")
}

func saveMergeRecord(rec *mergeRecord) (string, error) {
	path := mergeLogPath()
	if path == "" {
		return "", nil
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, data, 0o644)
}

// mergeOneGroup re-points all movies of the source persons at the target and
// records the original People list for rollback. It never scans (the batch
// caller triggers one scan at the end).
func mergeOneGroup(client *EmbyClient, targetID, targetName string, sourceIDs []string) (mergeGroup, int, int, []string) {
	group := mergeGroup{Target: targetID, TargetName: targetName, Sources: sourceIDs}

	movieIDs := map[string]struct{}{}
	for _, sid := range sourceIDs {
		items, err := client.ListPersonMovies(sid)
		if err != nil {
			return group, 0, 0, []string{fmt.Sprintf("列出演员 %s 的影片失败: %v", sid, err)}
		}
		for _, it := range items {
			movieIDs[it.Id] = struct{}{}
		}
	}

	ids := make([]string, 0, len(movieIDs))
	for id := range movieIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	ok, failed := 0, 0
	var notes []string
	for _, movieID := range ids {
		before, err := client.PeopleOf(movieID)
		if err != nil {
			failed++
			notes = append(notes, fmt.Sprintf("影片 %s 读取失败: %v", movieID, err))
			continue
		}
		changed, err := client.MovePeopleLinks(movieID, sourceIDs, targetID, targetName)
		if err != nil {
			failed++
			notes = append(notes, fmt.Sprintf("影片 %s 转移失败: %v", movieID, err))
			continue
		}
		if changed {
			group.Movies = append(group.Movies, movieBackup{ID: movieID, People: before})
			ok++
			// Verify the write actually stuck: Emby resolves People by name and
			// can silently re-attach the old person.
			if verified, err := verifyPeopleLink(client, movieID, sourceIDs, targetID); err != nil || !verified {
				group.Unverified = append(group.Unverified,
					fmt.Sprintf("%s: 关联未生效 (%v)", movieID, err))
			}
		}
		time.Sleep(embyWriteDelay)
	}
	return group, ok, failed, notes
}

// ResolveAllDuplicates merges every detected duplicate group, keeping the
// recommended (most movies / has avatar) entry. One library scan is triggered
// at the end, and the original person links are written to merge_log.json.
func ResolveAllDuplicates() (map[string]any, error) {
	c := getConfig()
	if c == nil {
		return nil, ErrNotConfigured
	}
	client, err := newEmbyClient(c)
	if err != nil {
		return nil, err
	}
	detected, err := DetectDuplicates(false)
	if err != nil {
		return nil, err
	}
	groups, _ := detected["groups"].([]DuplicateGroup)
	if len(groups) == 0 {
		return map[string]any{"groups": 0, "message": "没有需要合并的重复演员"}, nil
	}

	rec := &mergeRecord{Time: time.Now().Format(time.RFC3339)}
	totalMovies, totalOK, totalFail, merged := 0, 0, 0, 0
	var notes []string

	for _, g := range groups {
		if len(g.Actors) < 2 {
			continue
		}
		target := g.Actors[0]
		sources := make([]string, 0, len(g.Actors)-1)
		for _, a := range g.Actors[1:] {
			sources = append(sources, a.ID)
		}
		mg, ok, fail, groupNotes := mergeOneGroup(client, target.ID, target.Name, sources)
		if len(mg.Movies) > 0 {
			rec.Groups = append(rec.Groups, mg)
		}
		totalMovies += len(mg.Movies)
		totalOK += ok
		totalFail += fail
		merged++
		for _, n := range groupNotes {
			notes = append(notes, target.Name+": "+n)
		}
		for _, sid := range sources {
			if count, err := client.CountPersonMovies(sid); err == nil && count == 0 {
				_ = client.DeleteItem(sid)
			}
		}
	}

	logPath, saveErr := saveMergeRecord(rec)
	scanStarted, scanErr := client.TriggerLibraryScan()
	if scanErr != nil {
		notes = append(notes, "触发媒体库扫描失败: "+scanErr.Error())
	}

	log.Printf("[manage] resolve-all: groups=%d movies=%d ok=%d failed=%d scan=%v log=%s",
		merged, totalMovies, totalOK, totalFail, scanStarted, logPath)

	res := map[string]any{
		"groups":       merged,
		"movies":       totalMovies,
		"transferred":  totalOK,
		"failed":       totalFail,
		"scan_started": scanStarted,
		"rollback_log": logPath,
	}
	if saveErr != nil {
		res["log_error"] = saveErr.Error()
	}
	if len(notes) > 0 {
		res["notes"] = notes
	}
	return res, nil
}

// RollbackLastMerge restores the original People arrays recorded by the last
// merge batch.
func RollbackLastMerge() (map[string]any, error) {
	c := getConfig()
	if c == nil {
		return nil, ErrNotConfigured
	}
	path := mergeLogPath()
	if path == "" {
		return nil, fmt.Errorf("未配置映射表目录，无法定位回滚日志")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("没有可用的回滚日志: %w", err)
	}
	var rec mergeRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("回滚日志损坏: %w", err)
	}
	client, err := newEmbyClient(c)
	if err != nil {
		return nil, err
	}
	restored, failed := 0, 0
	var notes []string
	for _, g := range rec.Groups {
		for _, m := range g.Movies {
			if err := client.SetPeople(m.ID, m.People); err != nil {
				failed++
				notes = append(notes, fmt.Sprintf("影片 %s 还原失败: %v", m.ID, err))
				continue
			}
			restored++
			time.Sleep(embyWriteDelay)
		}
	}
	_, scanErr := client.TriggerLibraryScan()
	if scanErr != nil {
		notes = append(notes, "触发媒体库扫描失败: "+scanErr.Error())
	}
	res := map[string]any{
		"restored": restored,
		"failed":   failed,
		"time":     rec.Time,
	}
	if len(notes) > 0 {
		res["notes"] = notes
	}
	return res, nil
}
