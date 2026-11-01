package manage

import (
	"github.com/metatube-community/metatube-sdk-go/common/progress"
	"fmt"
	"log"
	"sync"
	"time"
)

// RefreshJob triggers Emby metadata refreshes in the background with progress
// tracking and batching. It exists because Emby's own "refresh library" gives
// no progress at all, and because a 3600-item refresh needs to be batched and
// resumable.
type RefreshJob struct {
	mu        sync.Mutex
	running   bool
	stopFlag  bool
	total     int
	done      int
	sent      int
	ok        int
	failed    int
	current   string
	startedAt   time.Time
	baseAnswers int64
	finished  bool
	err       string
}

var refreshJob = &RefreshJob{}

// RefreshOptions controls one batch run.
type RefreshOptions struct {
	// Scope: "all" (every movie in the library) or "problems" (only items the
	// audit flagged).
	Scope string `json:"scope"`
	// Limit caps how many items this run touches (0 = no cap).
	Limit int `json:"limit"`
	// ItemDelayMs is the pause between two refresh calls (default 200).
	ItemDelayMs int `json:"item_delay_ms"`
	// BatchSize / BatchPauseSec insert a longer pause every N items.
	BatchSize     int `json:"batch_size"`
	BatchPauseSec int `json:"batch_pause_sec"`
	// Mode: "" / "refresh" re-scrapes, "wipe" clears metadata but keeps images.
	Mode string `json:"mode"`
	// ReplaceAll lets provider output overwrite existing metadata (needed to
	// refresh a stale Japanese title or clear a summary).
	ReplaceAll bool `json:"replace_all"`
	// PathPrefix limits the run to one folder subtree (e.g. .../JAV/JAV2).
	PathPrefix string `json:"path_prefix"`
}

// StartRefresh validates the request and starts the background job.
func StartRefresh(opts RefreshOptions) (map[string]any, error) {
	c := getConfig()
	if c == nil {
		return nil, ErrNotConfigured
	}
	client, err := newEmbyClient(c)
	if err != nil {
		return nil, err
	}
	if opts.ItemDelayMs <= 0 {
		opts.ItemDelayMs = 200
	}
	if opts.ItemDelayMs > 5000 {
		opts.ItemDelayMs = 5000
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 200
	}
	if opts.BatchPauseSec < 0 {
		opts.BatchPauseSec = 0
	}

	refreshJob.mu.Lock()
	if refreshJob.running {
		refreshJob.mu.Unlock()
		return nil, fmt.Errorf("已有刷新任务在运行")
	}
	refreshJob.mu.Unlock()

	ids, err := collectRefreshIDs(client, c, opts)
	if err != nil {
		return nil, err
	}
	// Skip items this scope already refreshed in an earlier run, so a batch job
	// can be stopped and resumed (or run day by day) without redoing work.
	key := opts.Mode + "|" + opts.Scope + "|" + opts.PathPrefix
	prog := loadRefreshProgress()
	alreadyDone := 0
	if prog.Key == key && len(prog.Done) > 0 {
		done := make(map[string]struct{}, len(prog.Done))
		for _, id := range prog.Done {
			done[id] = struct{}{}
		}
		pending := make([]string, 0, len(ids))
		for _, id := range ids {
			if _, ok := done[id]; ok {
				alreadyDone++
				continue
			}
			pending = append(pending, id)
		}
		ids = pending
	}
	if opts.Limit > 0 && len(ids) > opts.Limit {
		ids = ids[:opts.Limit]
	}
	if len(ids) == 0 {
		return map[string]any{"started": false, "total": 0, "skipped": alreadyDone,
			"message": "该范围已全部刷新完成（如需重刷请点「重置进度」）"}, nil
	}

	refreshJob.mu.Lock()
	// Reset fields individually: replacing the whole struct would copy the
	// mutex that is currently held.
	refreshJob.running = true
	refreshJob.stopFlag = false
	refreshJob.total = len(ids)
	refreshJob.done = 0
	refreshJob.ok = 0
	refreshJob.failed = 0
	refreshJob.current = ""
	refreshJob.err = ""
	refreshJob.finished = false
	refreshJob.startedAt = time.Now()
	refreshJob.baseAnswers = progress.Answers()
	refreshJob.mu.Unlock()

	go runRefresh(client, ids, opts, key)
	log.Printf("[manage] refresh job started: %d items (skipped %d already done), scope=%s delay=%dms batch=%d/%ds",
		len(ids), alreadyDone, opts.Scope, opts.ItemDelayMs, opts.BatchSize, opts.BatchPauseSec)

	return map[string]any{"started": true, "total": len(ids), "skipped": alreadyDone}, nil
}

// StopRefresh asks the running job to stop after the current item.
func StopRefresh() map[string]any {
	refreshJob.mu.Lock()
	defer refreshJob.mu.Unlock()
	if !refreshJob.running {
		return map[string]any{"running": false}
	}
	refreshJob.stopFlag = true
	return map[string]any{"stopping": true, "done": refreshJob.done, "total": refreshJob.total}
}

// RefreshStatus reports live progress for the UI.
func RefreshStatus() map[string]any {
	refreshJob.mu.Lock()
	defer refreshJob.mu.Unlock()
	percent := 0
	if refreshJob.total > 0 {
		percent = refreshJob.done * 100 / refreshJob.total
	}
	elapsed := ""
	if !refreshJob.startedAt.IsZero() {
		elapsed = time.Since(refreshJob.startedAt).Round(time.Second).String()
	}
	answered := int(progress.Answers() - refreshJob.baseAnswers)
	if answered < 0 {
		answered = 0
	}
	if answered > refreshJob.total {
		answered = refreshJob.total
	}
	return map[string]any{
		"answered": answered,
		"running":  refreshJob.running,
		"total":    refreshJob.total,
		"done":     refreshJob.done,
		"sent":     refreshJob.sent,
		"ok":       refreshJob.ok,
		"failed":   refreshJob.failed,
		"percent":  percent,
		"current":  refreshJob.current,
		"elapsed":  elapsed,
		"finished": refreshJob.finished,
		"error":    refreshJob.err,
	}
}

func runRefresh(client *EmbyClient, ids []string, opts RefreshOptions, key string) {
	prog := loadRefreshProgress()
	if prog.Key != key {
		prog = refreshProgress{Key: key}
	}
	doneSet := make(map[string]struct{}, len(prog.Done)+len(ids))
	for _, id := range prog.Done {
		doneSet[id] = struct{}{}
	}
	persist := func() {
		list := make([]string, 0, len(doneSet))
		for id := range doneSet {
			list = append(list, id)
		}
		prog.Done = list
		saveRefreshProgress(prog)
	}
	defer func() {
		persist()
		refreshJob.mu.Lock()
		refreshJob.running = false
		refreshJob.finished = true
		refreshJob.current = ""
		done, ok, failed := refreshJob.done, refreshJob.ok, refreshJob.failed
		refreshJob.mu.Unlock()
		log.Printf("[manage] refresh job stopped/finished: done=%d ok=%d failed=%d", done, ok, failed)
	}()

	for i, id := range ids {
		refreshJob.mu.Lock()
		if refreshJob.stopFlag {
			refreshJob.mu.Unlock()
			return
		}
		refreshJob.current = id
		refreshJob.mu.Unlock()

		var err error
		if opts.Mode == "wipe" {
			err = client.WipeMetadata(id)
		} else {
			err = client.RefreshMetadata(id, opts.ReplaceAll)
		}
		refreshJob.mu.Lock()
		refreshJob.done++
		if err != nil {
			refreshJob.failed++
		} else {
			refreshJob.ok++
		}
		finished := refreshJob.done
		refreshJob.mu.Unlock()
		doneSet[id] = struct{}{}
		if finished%20 == 0 {
			persist()
		}

		time.Sleep(time.Duration(opts.ItemDelayMs) * time.Millisecond)

		if opts.BatchPauseSec > 0 && (i+1)%opts.BatchSize == 0 {
			time.Sleep(time.Duration(opts.BatchPauseSec) * time.Second)
		}
	}
}

// collectRefreshIDs returns the item ids this run should touch.
func collectRefreshIDs(client *EmbyClient, c *Config, opts RefreshOptions) ([]string, error) {
	libraryID := ResolveLibraryForPath(client, c, opts.PathPrefix)

	if opts.Scope == "problems" {
		ids, _, err := CollectProblemIDs(client, libraryID, opts.PathPrefix)
		if err != nil {
			return nil, fmt.Errorf("体检扫描失败: %w", err)
		}
		return ids, nil
	}

	var ids []string
	start := 0
	for {
		items, total, err := client.FetchMovies(libraryID, start, 500)
		if err != nil {
			return nil, fmt.Errorf("读取媒体库失败: %w", err)
		}
		for _, it := range items {
			if opts.PathPrefix != "" && !pathInScope(it.Path, opts.PathPrefix) {
				continue
			}
			ids = append(ids, it.Id)
		}
		start += len(items)
		if len(items) == 0 || (total > 0 && start >= total) {
			break
		}
	}
	return ids, nil
}
