package manage

import (
	"strings"
	"testing"
)

func TestParseStructured_EngineLineWithCallerPrefix(t *testing.T) {
	line := "[ENGINE] 2026/11/09 01:08:53 /src/engine/movie.go:260: " +
		"[SCRAPE] number=SSIS-001 provider=FANZA result=ok ms=812 actors=2 genres=6 title=Demo"

	entry, ok := parseStructured(line)
	if !ok {
		t.Fatalf("engine scrape line not recognised: %q", line)
	}
	if entry.Source != "SCRAPE" {
		t.Fatalf("source = %q, want SCRAPE", entry.Source)
	}
	if entry.Number != "SSIS-001" {
		t.Fatalf("number = %q, want SSIS-001", entry.Number)
	}
	if entry.Level != "INFO" {
		t.Fatalf("level = %q, want INFO", entry.Level)
	}
	if !strings.Contains(entry.Message, "抓取成功") {
		t.Fatalf("message = %q, want a readable scrape line", entry.Message)
	}
}

func TestParseStructured_FallbackMissIsWarning(t *testing.T) {
	entry, ok := parseStructured("[FALLBACK] number=ABC-001 provider=JavDB result=miss")
	if !ok {
		t.Fatal("fallback line not recognised")
	}
	if entry.Source != "FALLBACK" {
		t.Fatalf("source = %q, want FALLBACK", entry.Source)
	}
	if entry.Level != "WARN" {
		t.Fatalf("level = %q, want WARN", entry.Level)
	}
	if !strings.Contains(entry.Message, "兜底无结果") {
		t.Fatalf("message = %q", entry.Message)
	}
}

func TestParseStructured_TranslateNeedsFields(t *testing.T) {
	if _, ok := parseStructured("[TRANSLATE] number=ABC-001"); ok {
		t.Fatal("translate line without field must be rejected")
	}
	entry, ok := parseStructured("[TRANSLATE] number=ABC-001 field=genre engine=deepl from=ドラマ to=剧情")
	if !ok {
		t.Fatal("complete translate line must be accepted")
	}
	if entry.Source != "TRANSLATE" || !strings.Contains(entry.Message, "翻译流派") {
		t.Fatalf("entry = %+v", entry)
	}
}

func TestParseLogLine_GinRequestIsAPI(t *testing.T) {
	entry := parseLogLine("[GIN] 2026/11/09 - 01:08:53 | 200 | 1.2ms | 127.0.0.1 | GET /healthz")
	if entry.Source != "API" {
		t.Fatalf("source = %q, want API", entry.Source)
	}
	if entry.Level != "INFO" {
		t.Fatalf("level = %q, want INFO", entry.Level)
	}
}

func TestParseLogLine_PlainLineKeepsText(t *testing.T) {
	entry := parseLogLine("[manage] initialized: emby=http://emby:8096")
	if entry.Source != "manage" {
		t.Fatalf("source = %q, want manage", entry.Source)
	}
	if !strings.Contains(entry.Message, "initialized") {
		t.Fatalf("message = %q", entry.Message)
	}
}
