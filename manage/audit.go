package manage

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// kanaRe matches real hiragana/katakana letters. It deliberately leaves out
// Japanese punctuation and marks that also show up in Chinese titles:
// ・(U+30FB), ー(U+30FC), ヽヾ(U+30FD-30FE), ゛゜(U+309B-309C) and the
// 、。「」 block — otherwise a title like "续・出道" would be called Japanese.
var kanaRe = regexp.MustCompile(`[\x{3041}-\x{3096}\x{30A1}-\x{30FA}]`)
var hanRe = regexp.MustCompile(`[\x{4e00}-\x{9fff}]`)

// Issue types reported by the audit. Keep them stable: the UI groups by them.
const (
	IssueTitleMissing  = "title_missing"  // no real title, just the number
	IssueTitleJapanese = "title_japanese" // title left in Japanese
	IssueActorJapanese = "actor_japanese" // at least one actor name in Japanese
	IssueNoActors      = "no_actors"      // the provider returned no actors
	IssueGenreJapanese = "genre_japanese" // at least one genre left in Japanese
	IssueNoImage       = "no_image"       // no primary image
	// IssueActorUnmapped is informational: the performer name is Japanese and
	// the mapping table has no Chinese for it, so nothing can fix it
	// automatically. It is reported separately and never counts as a problem.
	IssueActorUnmapped = "actor_unmapped"
)

// AuditRequest scopes one completeness scan.
type AuditRequest struct {
	LibraryID string `json:"library_id"`
	// PathPrefix limits the scan to items whose file path starts with it
	// (e.g. /media/litepan-strm/JAV/JAV2), so one folder can be audited alone.
	PathPrefix string `json:"path_prefix"`
	// Problem limits the returned issue window to one issue type; the
	// counters stay global so the summary pills keep telling the truth.
	Problem string `json:"problem"`
	Offset  int    `json:"offset"`
	Limit   int    `json:"limit"`
}

// AuditIssue is one movie that fails the completeness rules.
type AuditIssue struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Number   string   `json:"number"`
	Path     string   `json:"path"`
	Problems []string `json:"problems"`
	Notes    []string `json:"notes,omitempty"`
}

// AuditResult is the machine-readable health report of a library.
type AuditResult struct {
	Total        int            `json:"total"`
	Checked      int            `json:"checked"`
	OK           int            `json:"ok"`
	ProblemCount int            `json:"problem_count"`
	Groups       map[string]int `json:"groups"`
	// NoteGroups / NoteCount hold informational findings (cannot be fixed by a
	// refresh) and are kept out of ProblemCount.
	NoteGroups map[string]int `json:"note_groups"`
	NoteCount  int            `json:"note_count"`
	IssueCount int            `json:"issue_count"`
	// DuplicateCount counts numbers that exist in more than one library copy.
	DuplicateCount int          `json:"duplicate_count"`
	Issues         []AuditIssue `json:"issues"`
	Offset         int          `json:"offset"`
	Limit          int          `json:"limit"`
	ScannedAt      string       `json:"scanned_at"`
}

const auditPageSize = 500

// maxAuditIssues caps the in-memory issue list of one scan.
const maxAuditIssues = 20000

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
		Groups:     map[string]int{},
		NoteGroups: map[string]int{},
		Issues:     []AuditIssue{},
		Offset:     req.Offset,
		Limit:      req.Limit,
		ScannedAt:  time.Now().Format(time.RFC3339),
	}

	libraries := []string{libraryID}
	if libraryID == "*" {
		libraries = c.EmbyLibraryIDs
	}
	// Count how many copies of each number exist (cross-library duplicates).
	numberCount := map[string]int{}
	for _, lib := range libraries {
		start := 0
		for {
			items, total, err := client.FetchMovies(lib, start, auditPageSize)
			if err != nil {
				return nil, fmt.Errorf("读取媒体库失败: %w", err)
			}
			if start == 0 {
				res.Total += total
			}
			for _, it := range items {
				if req.PathPrefix != "" && !pathInScope(it.Path, req.PathPrefix) {
					continue
				}
				res.Checked++
				// The same movie often exists twice (JAV library + quark
				// library). Count those copies so the summary can explain why
				// one entry looks Japanese while the other looks translated.
				if num := it.Number(); num != "" {
					numberCount[num]++
				}
				problems, notes := auditItem(it)
				if len(problems) == 0 && len(notes) == 0 {
					res.OK++
					continue
				}
				if len(problems) > 0 {
					res.ProblemCount++
					for _, pr := range problems {
						res.Groups[pr]++
					}
				}
				if len(notes) > 0 {
					res.NoteCount++
					for _, n := range notes {
						res.NoteGroups[n]++
					}
				}
				if matchesProblem(append(append([]string{}, problems...), notes...), req.Problem) && len(res.Issues) < maxAuditIssues {
					res.Issues = append(res.Issues, AuditIssue{ID: it.Id, Name: it.Name, Number: it.Number(), Path: it.Path, Problems: problems, Notes: notes})
				}
			}
			start += len(items)
			if len(items) == 0 || (total > 0 && start >= total) {
				break
			}
		}
	}
	if req.PathPrefix != "" {
		res.Total = res.Checked
	}
	// Numbers that appear more than once are the same film stored twice (one
	// copy per library); each copy is audited on its own.
	for _, n := range numberCount {
		if n > 1 {
			res.DuplicateCount++
		}
	}
	// Paginate the issue list itself: the UI asks for "issues 50..100", not
	// "items 50..100". Slicing here keeps offsets stable across requests.
	res.IssueCount = len(res.Issues)
	if req.Offset >= len(res.Issues) {
		res.Issues = []AuditIssue{}
		return res, nil
	}
	end := req.Offset + req.Limit
	if end > len(res.Issues) {
		end = len(res.Issues)
	}
	res.Issues = res.Issues[req.Offset:end]
	return res, nil
}

// looksJapanese reports whether the text is really Japanese rather than a
// translated title with a few leftover kana (typically an actress name).
// A title counts as Japanese when there is no Han at all, or when kana make up
// a meaningful share of the characters.
func looksJapanese(s string) bool {
	if !kanaRe.MatchString(s) {
		return false
	}
	if !hanRe.MatchString(s) {
		return true
	}
	runes := []rune(s)
	if len(runes) == 0 {
		return false
	}
	kana := len(kanaRe.FindAllString(s, -1))
	return float64(kana)/float64(len(runes)) >= 0.2
}

// matchesProblem reports whether an issue belongs to the requested filter.
func matchesProblem(problems []string, filter string) bool {
	if filter == "" {
		return true
	}
	for _, p := range problems {
		if p == filter {
			return true
		}
	}
	return false
}

// canonicalCode keeps letters and digits, upper-cased, and drops the leading
// zeros of every digit run so "MIAB-585", "miab00585" and "MIAB585" all match.
func canonicalCode(s string) string {
	var out, digits strings.Builder
	flush := func() {
		if digits.Len() == 0 {
			return
		}
		d := strings.TrimLeft(digits.String(), "0")
		if d == "" {
			d = "0"
		}
		out.WriteString(d)
		digits.Reset()
	}
	for _, r := range strings.ToUpper(s) {
		switch {
		case r >= '0' && r <= '9':
			digits.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			flush()
			out.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return out.String()
}

// titleMissing reports whether the name carries no real title, only the code
// (optionally with a short suffix such as "-C" or "[4K]").
func titleMissing(name, number string) bool {
	if strings.TrimSpace(name) == "" {
		return true
	}
	// Any Han means a title exists ("中文标题"), so it cannot be missing.
	if hanRe.MatchString(name) {
		return false
	}
	if !hasText(name) {
		return true
	}
	base, num := canonicalCode(name), canonicalCode(number)
	if num == "" {
		return false
	}
	if base == num {
		return true
	}
	// "MIAB585C", "MIAB5854K" — the code plus a 1–2 char marker.
	if strings.HasPrefix(base, num) && len(base)-len(num) <= 2 {
		return true
	}
	return false
}

// auditItem applies the completeness rules to one movie.
func auditItem(it embyItem) (problems, notes []string) {
	problems = make([]string, 0, 4)
	name := strings.TrimSpace(it.Name)
	number := it.Number()

	actors := it.Actors()
	// Judge the title after dropping known actor names: many translated titles
	// keep the original performer name at the end ("… 佐藤ののか"), which is
	// useful context but not a sign of an untranslated title.
	titleForTest := name
	for _, a := range actors {
		if a != "" && len(a) > 1 {
			titleForTest = strings.ReplaceAll(titleForTest, a, "")
		}
	}
	if titleMissing(name, number) {
		problems = append(problems, IssueTitleMissing)
	} else if looksJapanese(titleForTest) {
		problems = append(problems, IssueTitleJapanese)
	}
	if len(actors) == 0 {
		problems = append(problems, IssueNoActors)
	} else {
		// Only flag names the mapping table can turn into a real Chinese name.
		// When the table has no Chinese for a performer, no refresh can fix it,
		// so it becomes an informational note instead of a problem.
		am := ActorMapping()
		flagged, unmapped := false, false
		for _, a := range actors {
			if !kanaRe.MatchString(a) {
				continue
			}
			if am == nil {
				unmapped = true
				continue
			}
			entry, ok := am.Lookup(a)
			if !ok || entry == nil || entry.Name == "" || kanaRe.MatchString(entry.Name) || !hanRe.MatchString(entry.Name) {
				unmapped = true
				continue
			}
			flagged = true
		}
		if flagged {
			problems = append(problems, IssueActorJapanese)
		} else if unmapped {
			notes = append(notes, IssueActorUnmapped)
		}
	}

	for _, g := range it.GenresForAudit() {
		if !kanaRe.MatchString(g) {
			continue
		}
		// Providers sometimes pack performer names into the genre list. Those
		// belong to the actor rule (already reported there), not to 类型.
		if am := ActorMapping(); am != nil {
			if _, isPerson := am.Lookup(g); isPerson {
				continue
			}
		}
		problems = append(problems, IssueGenreJapanese)
		break
	}

	if _, ok := it.ImageTags["Primary"]; !ok {
		problems = append(problems, IssueNoImage)
	}
	return problems, notes
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

// pathInScope reports whether path is the folder itself or inside it. A plain
// prefix match would make ".../JAV" also swallow ".../JAV2".
func pathInScope(path, prefix string) bool {
	prefix = strings.TrimRight(prefix, "/")
	if path == prefix {
		return true
	}
	return strings.HasPrefix(path, prefix+"/")
}

// CollectProblemIDs returns every item id in scope that fails the audit rules.
// Audit() itself caps the returned issue window, so a full run cannot rely on
// it; this walks the library directly.
func CollectProblemIDs(client *EmbyClient, libraryID, pathPrefix string) ([]string, int, error) {
	ids := make([]string, 0, 256)
	scanned := 0
	start := 0
	for {
		items, total, err := client.FetchMovies(libraryID, start, 500)
		if err != nil {
			return nil, scanned, err
		}
		for _, it := range items {
			if pathPrefix != "" && !pathInScope(it.Path, pathPrefix) {
				continue
			}
			scanned++
			if p, n := auditItem(it); len(p) > 0 || len(n) > 0 {
				ids = append(ids, it.Id)
			}
		}
		start += len(items)
		if len(items) == 0 || (total > 0 && start >= total) {
			break
		}
	}
	return ids, scanned, nil
}

// ResolveLibraryForPath picks the library whose root contains the given path
// prefix, so a folder scope works without the caller naming its library.
func ResolveLibraryForPath(client *EmbyClient, cfg *Config, prefix string) string {
	if cfg == nil || len(cfg.EmbyLibraryIDs) == 0 {
		return ""
	}
	if prefix == "" {
		return cfg.EmbyLibraryIDs[0]
	}
	locs := client.LibraryLocations()
	for _, lib := range cfg.EmbyLibraryIDs {
		for _, root := range locs[lib] {
			if root != "" && pathInScope(prefix, strings.TrimRight(root, "/")) {
				return lib
			}
		}
	}
	return cfg.EmbyLibraryIDs[0]
}
