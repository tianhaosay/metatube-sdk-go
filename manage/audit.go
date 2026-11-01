package manage

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var kanaRe = regexp.MustCompile(`[\x{3040}-\x{30ff}]`)
var hanRe = regexp.MustCompile(`[\x{4e00}-\x{9fff}]`)

// Issue types reported by the audit. Keep them stable: the UI groups by them.
const (
	IssueTitleMissing  = "title_missing"  // no real title, just the number
	IssueTitleJapanese = "title_japanese" // title left in Japanese
	IssueActorJapanese = "actor_japanese" // at least one actor name in Japanese
	IssueNoActors      = "no_actors"      // the provider returned no actors
	IssueGenreJapanese = "genre_japanese" // at least one genre left in Japanese
	IssueNoImage       = "no_image"       // no primary image
)

// AuditRequest scopes one completeness scan.
type AuditRequest struct {
	LibraryID string `json:"library_id"`
	Offset    int    `json:"offset"`
	Limit     int    `json:"limit"`
}

// AuditIssue is one movie that fails the completeness rules.
type AuditIssue struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Number   string   `json:"number"`
	Problems []string `json:"problems"`
}

// AuditResult is the machine-readable health report of a library.
type AuditResult struct {
	Total        int            `json:"total"`
	Checked      int            `json:"checked"`
	OK           int            `json:"ok"`
	ProblemCount int            `json:"problem_count"`
	Groups       map[string]int `json:"groups"`
	Issues       []AuditIssue   `json:"issues"`
	Offset       int            `json:"offset"`
	Limit        int            `json:"limit"`
	ScannedAt    string         `json:"scanned_at"`
}

const auditPageSize = 500

// Audit scans a library and reports which movies are not "good enough" to
// browse: missing/Japanese title, missing/Japanese actors, Japanese genres,
// missing cover.
//
// The whole library is scanned so the counters are truthful; only the
// requested window of items is returned (the UI pages through it).
func Audit(req AuditRequest) (*AuditResult, error) {
	c := getConfig()
	if c == nil {
		return nil, ErrNotConfigured
	}
	client, err := newEmbyClient(c)
	if err != nil {
		return nil, err
	}
	libraryID := req.LibraryID
	if libraryID == "" && len(c.EmbyLibraryIDs) > 0 {
		libraryID = c.EmbyLibraryIDs[0]
	}
	if req.Limit <= 0 || req.Limit > 500 {
		req.Limit = 200
	}
	if req.Offset < 0 {
		req.Offset = 0
	}

	res := &AuditResult{
		Groups:    map[string]int{},
		Issues:    []AuditIssue{},
		Offset:    req.Offset,
		Limit:     req.Limit,
		ScannedAt: time.Now().Format(time.RFC3339),
	}

	start := 0
	for {
		items, total, err := client.FetchMovies(libraryID, start, auditPageSize)
		if err != nil {
			return nil, fmt.Errorf("读取媒体库失败: %w", err)
		}
		if start == 0 {
			res.Total = total
		}
		for _, it := range items {
			res.Checked++
			problems := auditItem(it)
			if len(problems) == 0 {
				res.OK++
				continue
			}
			res.ProblemCount++
			for _, p := range problems {
				res.Groups[p]++
			}
			if res.Checked > req.Offset && len(res.Issues) < req.Limit {
				res.Issues = append(res.Issues, AuditIssue{
					ID:       it.Id,
					Name:     it.Name,
					Number:   it.Number(),
					Problems: problems,
				})
			}
		}
		start += len(items)
		if len(items) == 0 || (total > 0 && start >= total) {
			break
		}
	}
	return res, nil
}

// auditItem applies the completeness rules to one movie.
func auditItem(it embyItem) []string {
	problems := make([]string, 0, 4)
	name := strings.TrimSpace(it.Name)
	number := it.Number()

	if name == "" || (number != "" && strings.EqualFold(name, number)) || !hasText(name) {
		problems = append(problems, IssueTitleMissing)
	} else if kanaRe.MatchString(name) {
		problems = append(problems, IssueTitleJapanese)
	}

	actors := it.Actors()
	if len(actors) == 0 {
		problems = append(problems, IssueNoActors)
	} else {
		for _, a := range actors {
			if kanaRe.MatchString(a) {
				problems = append(problems, IssueActorJapanese)
				break
			}
		}
	}

	for _, g := range it.Genres {
		if kanaRe.MatchString(g) {
			problems = append(problems, IssueGenreJapanese)
			break
		}
	}

	if _, ok := it.ImageTags["Primary"]; !ok {
		problems = append(problems, IssueNoImage)
	}
	return problems
}

// hasText reports whether the name carries any letters (Latin/CJK), i.e. is
// not just a number or punctuation.
func hasText(s string) bool {
	if kanaRe.MatchString(s) || hanRe.MatchString(s) {
		return true
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return true
		}
	}
	return false
}
