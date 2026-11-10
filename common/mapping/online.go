package mapping

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Online actor-mapping source: actor-mapping.xml on the main branch of the
// GitHub repository li-peifeng/Jav-Actors-Mapping. Sync pins the download to the
// latest commit and verifies the content against the Git blob SHA1 before
// replacing the local file; the commit is recorded in a <path>.version sidecar
// so repeated syncs are cheap no-ops.

const (
	onlineRepoOwner = "li-peifeng"
	onlineRepoName  = "Jav-Actors-Mapping"
	onlineBranch    = "main"
	onlineFilePath  = "actor-mapping.xml"

	onlineMinRecords = 1000 // sanity check: refuse tiny/corrupt tables.
)

var (
	onlineCommitSHARe = regexp.MustCompile(`^[0-9a-f]{40}$`)

	// ErrSyncUnchanged reports that the remote commit already matches the
	// synced one, so nothing was downloaded.
	ErrSyncUnchanged = errors.New("mapping: online source unchanged")
)

// SyncResult describes the outcome of one online sync attempt.
type SyncResult struct {
	Updated bool   // whether the local file was replaced.
	Version string // synced commit SHA ("" when nothing done).
	Records int    // entry count of the (new) table.
	Message string // human-readable outcome shown in the console (Chinese).
}

func onlineCommitsAPIURL() string {
	return fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/commits?path=%s&sha=%s&per_page=1&page=1",
		onlineRepoOwner, onlineRepoName, onlineFilePath, onlineBranch)
}

func onlineContentsAPIURL(ref string) string {
	return fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/contents/%s?ref=%s",
		onlineRepoOwner, onlineRepoName, onlineFilePath, ref)
}

func onlineDownloadCandidates(ref string) []string {
	return []string{
		fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s",
			onlineRepoOwner, onlineRepoName, ref, onlineFilePath),
		fmt.Sprintf("https://cdn.jsdelivr.net/gh/%s/%s@%s/%s",
			onlineRepoOwner, onlineRepoName, ref, onlineFilePath),
		fmt.Sprintf("https://fastly.jsdelivr.net/gh/%s/%s@%s/%s",
			onlineRepoOwner, onlineRepoName, ref, onlineFilePath),
		fmt.Sprintf("https://gcore.jsdelivr.net/gh/%s/%s@%s/%s",
			onlineRepoOwner, onlineRepoName, ref, onlineFilePath),
	}
}

func onlineHTTPGet(url string, timeout time.Duration) ([]byte, error) {
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "metatube-plus-mapping-sync")
	req.Header.Set("Cache-Control", "no-cache")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d from %s", resp.StatusCode, url)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20)) // 64MiB cap.
}

// gitBlobSHA1 computes the Git blob object SHA1 of content, the integrity check
// applied to downloaded mapping tables.
func gitBlobSHA1(content []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(content))
	h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}

// readStoredVersion returns the commit SHA recorded in the version sidecar
// file, or "" when absent/malformed.
func readStoredVersion(localPath string) string {
	data, err := os.ReadFile(localPath + ".version")
	if err != nil {
		return ""
	}
	v := strings.ToLower(strings.TrimSpace(string(data)))
	if !onlineCommitSHARe.MatchString(v) {
		return ""
	}
	return v
}

// SyncOnlineActorMapping downloads the latest actor-mapping.xml from the online
// source and atomically replaces localPath when the remote commit differs from
// the synced one. The loader hot-reloads on mtime change, so consumers pick up
// the new table without a restart.
func SyncOnlineActorMapping(localPath string) (*SyncResult, error) {
	stored := readStoredVersion(localPath)

	// Latest commit touching the mapping file.
	commitBody, err := onlineHTTPGet(onlineCommitsAPIURL(), 20*time.Second)
	if err != nil {
		return nil, fmt.Errorf("query latest commit: %w", err)
	}
	version := firstSHA(commitBody)
	if version == "" {
		return nil, errors.New("mapping: no valid commit SHA in commits response")
	}
	if version == stored {
		return &SyncResult{
			Updated: false,
			Version: version,
			Message: "在线映射源无更新",
		}, nil
	}

	// Blob SHA of the file at that commit, used as the integrity reference.
	var blobSHA string
	if body, err := onlineHTTPGet(onlineContentsAPIURL(version), 20*time.Second); err == nil {
		blobSHA = firstSHA(body)
	} else {
		log.Printf("[mapping-sync] contents API failed (continuing without blob check): %v", err)
	}

	// Download from CDN candidates pinned to the commit.
	var (
		content []byte
		lastErr error
	)
	for _, url := range onlineDownloadCandidates(version) {
		content, lastErr = onlineHTTPGet(url, 60*time.Second)
		if lastErr != nil {
			log.Printf("[mapping-sync] download failed, trying next: %v", lastErr)
			continue
		}
		if blobSHA != "" && gitBlobSHA1(content) != blobSHA {
			lastErr = errors.New("mapping: downloaded content does not match git blob sha")
			log.Printf("[mapping-sync] %v", lastErr)
			continue
		}
		break
	}
	if lastErr != nil {
		return nil, fmt.Errorf("download mapping: %w", lastErr)
	}

	// Sanity checks before anything replaces the local file.
	records := strings.Count(string(content), "<a ")
	if records < onlineMinRecords {
		return nil, fmt.Errorf("mapping: downloaded table too small (%d records)", records)
	}
	sum := sha256.Sum256(content)

	// Atomic replace, then record the synced commit in the version sidecar.
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return nil, err
	}
	tmp := localPath + ".tmp"
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, localPath); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	_ = os.WriteFile(localPath+".version", []byte(version), 0o644)

	log.Printf("[mapping-sync] updated %s: commit=%s records=%d sha256=%s",
		localPath, version[:12], records, hex.EncodeToString(sum[:12]))
	return &SyncResult{
		Updated: true,
		Version: version,
		Records: records,
		Message: fmt.Sprintf("已更新：%d 条映射（commit %s）", records, version[:12]),
	}, nil
}

// commitSHARe extracts the first "sha" field from a GitHub API JSON response.
var commitSHARe = regexp.MustCompile(`"sha"\s*:\s*"([0-9a-f]{40})"`)

// firstSHA returns the first 40-hex "sha" field of a GitHub API response. Both
// endpoints used here report the relevant SHA first: the commits API lists the
// latest commit, and the contents API describes the single requested file.
func firstSHA(body []byte) string {
	m := commitSHARe.FindSubmatch(body)
	if m == nil {
		return ""
	}
	return strings.ToLower(string(m[1]))
}
