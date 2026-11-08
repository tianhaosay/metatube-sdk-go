package manage

import (
	"fmt"
	"log"
	"path"
	"regexp"
	"sort"
	"strings"
)

// UncensoredBadgeGenre is the Emby genre added to movies that are a
// mosaic-restored or uncensored release.
const UncensoredBadgeGenre = "无码破解"

// uncensoredTagRe matches the release-suffix markers that indicate an
// uncensored / restored cut. Go's RE2 engine has no lookahead, so the trailing
// boundary is captured instead: group 1 is the tag and group 2 the separator
// that must end the name or be one of "-_.".
//
//	XXX-LTX.mp4      AI mosaic restoration (Jasna/LTX)
//	XXX-U.mp4        uncensored
//	XXX_UC.mkv       underscore separator
var uncensoredTagRe = regexp.MustCompile(`(?i)-(ltx|uc|u)([-_.]|$)`)

// isUncensoredRelease reports whether the file name marks an uncensored cut.
func isUncensoredRelease(filePath string) bool {
	base := path.Base(filePath)
	// Drop the extension so "...-U.strm" is judged on "...-U".
	if ext := path.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	return uncensoredTagRe.MatchString(base)
}

// UncensoredBadgeResult reports what the badge run changed.
type UncensoredBadgeResult struct {
	Scanned  int              `json:"scanned"`
	Matched  int              `json:"matched"`
	Updated  int              `json:"updated"`
	Skipped  int              `json:"skipped"`
	Excluded []string         `json:"excluded,omitempty"`
	Changed  []UncensoredItem `json:"changed"`
}

// UncensoredItem is one movie that received (or already had) the badge.
type UncensoredItem struct {
	ID     string `json:"id"`
	Number string `json:"number"`
	Name   string `json:"name"`
	Path   string `json:"path"`
	Added  bool   `json:"added"`
}

// ApplyUncensoredBadges scans the configured libraries, adds UncensoredBadgeGenre
// to movies whose file name carries an uncensored / restoration suffix, and
// reports what changed. Items that already carry the genre are reported but not
// rewritten, and numbers in exclude are skipped.
func ApplyUncensoredBadges(exclude []string, dryRun bool) (*UncensoredBadgeResult, error) {
	c := getConfig()
	if c == nil {
		return nil, ErrNotConfigured
	}
	client, err := newEmbyClient(c)
	if err != nil {
		return nil, err
	}

	skip := map[string]struct{}{}
	for _, n := range exclude {
		if n = strings.ToUpper(strings.TrimSpace(n)); n != "" {
			skip[n] = struct{}{}
		}
	}

	res := &UncensoredBadgeResult{Changed: []UncensoredItem{}}

	for _, lib := range c.EmbyLibraryIDs {
		start := 0
		for {
			items, total, err := client.FetchMovies(lib, start, 500)
			if err != nil {
				return nil, fmt.Errorf("读取媒体库失败: %w", err)
			}
			for _, it := range items {
				res.Scanned++
				if !isUncensoredRelease(it.Path) {
					continue
				}
				res.Matched++
				number := it.Number()
				if _, isExcluded := skip[number]; isExcluded {
					res.Excluded = append(res.Excluded, number)
					continue
				}
				if hasGenre(it.Genres, UncensoredBadgeGenre) {
					res.Skipped++
					res.Changed = append(res.Changed, UncensoredItem{
						ID: it.Id, Number: number, Name: it.Name, Path: it.Path,
					})
					continue
				}
				if dryRun {
					res.Updated++
					res.Changed = append(res.Changed, UncensoredItem{
						ID: it.Id, Number: number, Name: it.Name, Path: it.Path, Added: true,
					})
					continue
				}
				if err := client.AddGenre(it.Id, UncensoredBadgeGenre); err != nil {
					log.Printf("[manage] uncensored badge: %s (%s) failed: %v", it.Name, it.Id, err)
					continue
				}
				res.Updated++
				res.Changed = append(res.Changed, UncensoredItem{
					ID: it.Id, Number: number, Name: it.Name, Path: it.Path, Added: true,
				})
			}
			start += len(items)
			if len(items) == 0 || (total > 0 && start >= total) {
				break
			}
		}
	}

	sort.Slice(res.Changed, func(i, j int) bool { return res.Changed[i].Number < res.Changed[j].Number })
	log.Printf("[manage] uncensored badge: scanned=%d matched=%d updated=%d already=%d excluded=%d dry=%v",
		res.Scanned, res.Matched, res.Updated, res.Skipped, len(res.Excluded), dryRun)
	return res, nil
}

func hasGenre(genres []string, want string) bool {
	for _, g := range genres {
		if strings.EqualFold(strings.TrimSpace(g), want) {
			return true
		}
	}
	return false
}
