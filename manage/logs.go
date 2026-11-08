package manage

import (
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// LogEntry is one parsed log line exposed to the console.
type LogEntry struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Source  string `json:"source"`
	Number  string `json:"number,omitempty"`
	Message string `json:"message"`
}

const logRingCap = 5000

type logRing struct {
	mu   sync.Mutex
	buf  []LogEntry
	next int
	full bool
}

var ring = &logRing{buf: make([]LogEntry, logRingCap)}

func (r *logRing) add(e LogEntry) {
	r.mu.Lock()
	r.buf[r.next] = e
	r.next = (r.next + 1) % logRingCap
	if r.next == 0 {
		r.full = true
	}
	r.mu.Unlock()
}

// QueryLogs returns matching entries, newest first.
func QueryLogs(number, level, source string, limit int) []LogEntry {
	if limit <= 0 || limit > logRingCap {
		limit = 300
	}
	number = strings.ToUpper(strings.TrimSpace(number))
	level = strings.ToUpper(strings.TrimSpace(level))
	source = strings.ToUpper(strings.TrimSpace(source))

	ring.mu.Lock()
	defer ring.mu.Unlock()
	out := make([]LogEntry, 0, 64)
	n := len(ring.buf)
	if !ring.full {
		n = ring.next
	}
	for i := 0; i < n && len(out) < limit; i++ {
		idx := (ring.next - 1 - i + logRingCap) % logRingCap
		e := ring.buf[idx]
		if number != "" && !strings.Contains(strings.ToUpper(e.Number), number) {
			continue
		}
		if level != "" && e.Level != level {
			continue
		}
		if source != "" && !strings.Contains(strings.ToUpper(e.Source), source) {
			continue
		}
		out = append(out, e)
	}
	return out
}

var (
	// Hyphenated movie numbers, e.g. START-015.
	numberRe = regexp.MustCompile(`\b([A-Z]{2,7}-\d{2,6})\b`)
	ginLine  = regexp.MustCompile(`\[GIN\].*\|\s*(\d{3})\s*\|`)
	srcRe    = regexp.MustCompile(`^\[(ENGINE|JavDB|manage|mapping|GIN)\]`)
	stdDate  = regexp.MustCompile(`^\d{4}/\d{2}/\d{2}[ T]`)
	goFileRe = regexp.MustCompile(`^[\w/\\.\-]+\.go:\d+:\s*`)

	// Structured log lines: leading [SCRAPE]/[FALLBACK]/[TRANSLATE] bracket,
	// optional timestamp, then key=value fields.
	srcBracketRe     = regexp.MustCompile(`^\[([A-Za-z]+)\]\s+`)
	kvRe             = regexp.MustCompile(`(\w+)=("([^"]*)"|\S+)`)
	structuredDateRe = regexp.MustCompile(`^\d{4}/\d{2}/\d{2}[ T]\d{2}:\d{2}:\d{2}(\.\d+)?\s+`)
)

// parseLogLine derives level/source/number from a raw log line. Parsing is
// heuristic: unmatched fields default gracefully.
func parseLogLine(line string) LogEntry {
	// Machine-readable scrape/fallback lines get a readable rewrite first.
	if e, ok := parseStructured(line); ok {
		return e
	}
	e := LogEntry{
		Time:    time.Now().Format("15:04:05.000"),
		Level:   "INFO",
		Source:  "APP",
		Message: line,
	}
	rest := line

	// Standard-log date prefix: 2026/10/31 20:47:12 message
	if stdDate.MatchString(rest) {
		parts := strings.SplitN(rest, " ", 3)
		if len(parts) >= 3 {
			if t, err := time.Parse("2006/01/02 15:04:05", parts[0]+" "+parts[1]); err == nil {
				e.Time = t.Format("15:04:05.000")
			}
			rest = parts[2]
		}
	}

	// [ENGINE] 2026/10/31 ... : prefix comes before the date.
	if m := srcRe.FindStringSubmatch(rest); m != nil {
		e.Source = m[1]
		rest = strings.TrimSpace(strings.TrimPrefix(rest, m[0]))
		if stdDate.MatchString(rest) {
			parts := strings.SplitN(rest, " ", 3)
			if len(parts) >= 3 {
				if t, err := time.Parse("2006/01/02 15:04:05", parts[0]+" "+parts[1]); err == nil {
					e.Time = t.Format("15:04:05.000")
				}
				rest = parts[2]
			}
		}
	}

	// GIN request lines: derive level from status code, source=API.
	if m := ginLine.FindStringSubmatch(line); m != nil {
		e.Source = "API"
		code, _ := strconv.Atoi(m[1])
		switch {
		case code >= 500:
			e.Level = "ERROR"
		case code >= 400:
			e.Level = "WARN"
		}
	}

	// GORM per-query SQL lines are tagged DB/DEBUG, not dropped.
	if strings.Contains(rest, "[rows:") && strings.Contains(rest, "ms]") {
		e.Level = "DEBUG"
		e.Source = "DB"
	}

	// Level heuristics.
	switch {
	case strings.Contains(rest, "ERROR"), strings.Contains(line, "ERROR"):
		e.Level = "ERROR"
	case strings.Contains(rest, "WARN"), strings.Contains(rest, "failed"), strings.Contains(rest, "Failed"), strings.Contains(rest, "Forbidden"), strings.Contains(rest, "took over"), strings.Contains(rest, "not loaded"):
		if e.Level == "INFO" {
			e.Level = "WARN"
		}
	}

	// Number extraction (hyphenated numbers only, e.g. START-015).
	if m := numberRe.FindStringSubmatch(strings.ToUpper(rest)); m != nil {
		e.Number = m[1]
	}

	// Strip engine source-file prefixes (e.g. /src/engine/movie.go:145:).
	rest = goFileRe.ReplaceAllString(rest, "")
	e.Message = strings.TrimSpace(rest)
	return e
}

type logCapture struct{}

func (c *logCapture) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			ring.add(parseLogLine(line))
		}
	}
	return len(p), nil
}

var logCaptureOnce sync.Once

// InstallLogCapture mirrors the standard logger, the engine logger and gin's
// request logger into the in-memory ring. Must be called before the engine and
// router are created. Returns the logger the engine should use.
func InstallLogCapture() *log.Logger {
	logCaptureOnce.Do(func() {
		capture := &logCapture{}
		base := log.Default().Writer()
		if base == nil {
			base = os.Stderr
		}
		log.SetOutput(io.MultiWriter(base, capture))
		gin.DefaultWriter = io.MultiWriter(gin.DefaultWriter, capture)
	})
	// log.Default().Writer() already tees into the ring, so the engine logger
	// only adds its prefix and file flags; adding a second capture writer here
	// would record every engine line twice.
	return log.New(log.Default().Writer(), "[ENGINE] ", log.LstdFlags|log.Llongfile)
}

// parseStructured turns a key=value log line into a readable Chinese message.
// The raw line may arrive as "[ENGINE] 2026/11/01 13:44:16 [FALLBACK] number=...",
// so bracket prefixes and the date are stripped first.
func parseStructured(line string) (LogEntry, bool) {
	s := strings.TrimSpace(line)
	kind := ""
	// The date may precede or follow the source bracket, so strip both
	// repeatedly.
	for i := 0; i < 4; i++ {
		if m := srcBracketRe.FindStringSubmatch(s); m != nil {
			if m[1] == "SCRAPE" || m[1] == "FALLBACK" || m[1] == "TRANSLATE" {
				kind = m[1]
			}
			s = strings.TrimSpace(s[len(m[0]):])
			continue
		}
		if structuredDateRe.MatchString(s) {
			s = strings.TrimSpace(structuredDateRe.ReplaceAllString(s, ""))
			continue
		}
		break
	}
	if kind == "" {
		return LogEntry{}, false
	}
	fields := map[string]string{}
	for _, kv := range kvRe.FindAllStringSubmatch(s, -1) {
		val := kv[2]
		if kv[3] != "" {
			val = kv[3]
		}
		fields[strings.ToLower(kv[1])] = strings.Trim(val, `"`)
	}
	if kind == "TRANSLATE" && (fields["number"] == "" || fields["field"] == "") {
		return LogEntry{}, false
	}
	e := LogEntry{
		Time:   time.Now().Format("15:04:05.000"),
		Level:  "INFO",
		Source: kind,
		Number: fields["number"],
	}
	provider := fields["provider"]
	result := fields["result"]
	ms := fields["ms"]
	switch kind {
	case "SCRAPE":
		switch result {
		case "ok":
			e.Message = fmt.Sprintf("%s · %s · 抓取成功 · %sms · 演员 %s · 流派 %s · %s",
				fields["number"], provider, ms, fields["actors"], fields["genres"], fields["title"])
		default:
			e.Level = "WARN"
			e.Message = fmt.Sprintf("%s · %s · 抓取失败 · %sms", fields["number"], provider, ms)
		}
	case "TRANSLATE":
		field := fields["field"]
		engine := fields["engine"]
		if engine == "" {
			engine = "unknown"
		}
		switch field {
		case "title":
			field = "标题"
		case "summary":
			field = "简介"
		case "genre":
			field = "流派"
		}
		if from, to := fields["from"], fields["to"]; from != "" {
			e.Message = fmt.Sprintf("%s · 翻译%s · %s · %s → %s", fields["number"], field, engine, from, to)
		} else {
			e.Message = fmt.Sprintf("%s · 翻译%s · %s · %s", fields["number"], field, engine, fields["text"])
		}
	case "FALLBACK":
		switch result {
		case "ok":
			e.Message = fmt.Sprintf("%s · %s · 兜底成功 · 补到 %s 位演员",
				fields["number"], provider, fields["actors"])
		case "none":
			e.Message = fmt.Sprintf("%s · %s · 兜底无演员 · %sms", fields["number"], provider, ms)
		case "miss":
			e.Level = "WARN"
			e.Message = fmt.Sprintf("%s · 兜底无结果（12 小时内不再尝试）", fields["number"])
		default:
			e.Level = "WARN"
			e.Message = fmt.Sprintf("%s · %s · 兜底失败 · %sms", fields["number"], provider, ms)
		}
	}
	return e, true
}
