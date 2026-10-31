package manage

import (
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
		// newest first: walk the ring backwards.
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
	numberRe = regexp.MustCompile(`\b([A-Z]{2,7}-\d{2,6})\b`)
	ginLine  = regexp.MustCompile(`\[GIN\].*\|\s*(\d{3})\s*\|`)
	srcRe    = regexp.MustCompile(`^\[(ENGINE|TRANSLATE|JavDB|manage|mapping|GIN)\]`)
	stdDate  = regexp.MustCompile(`^\d{4}/\d{2}/\d{2}[ T]`)
	goFileRe = regexp.MustCompile(`^[\w/\\.\-]+\.go:\d+:\s*`)
)

// parseLogLine derives level/source/number from a raw log line. Parsing is
// heuristic on purpose: unmatched fields default gracefully.
func parseLogLine(line string) LogEntry {
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

	// [ENGINE] 2026/10/31 ... — swap prefix/date order.
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

	// Noise filter: GORM per-query SQL lines are skipped entirely.
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
// request logger into the in-memory ring. Must be called before the engine
// and router are created. Returns the logger the engine should use.
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
	return log.New(io.MultiWriter(log.Default().Writer(), &logCapture{}), "[ENGINE]\u00a0", log.LstdFlags|log.Llongfile)
}
