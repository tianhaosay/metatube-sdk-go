package manage

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/metatube-community/metatube-sdk-go/common/mapping"
	"github.com/metatube-community/metatube-sdk-go/provider"
	_ "github.com/metatube-community/metatube-sdk-go/provider/gfriends" // avatar fallback source
)

var avatarStatus = jobStatus{}

// ScanMissingAvatars returns Emby persons without a primary image.
func ScanMissingAvatars() (map[string]any, error) {
	c := getConfig()
	if c == nil {
		return nil, ErrNotConfigured
	}
	client, err := newEmbyClient(c)
	if err != nil {
		return nil, err
	}
	persons, err := client.FetchPersons(c.EmbyLibraryIDs)
	if err != nil {
		return nil, fmt.Errorf("拉取 Emby 演员失败: %w", err)
	}
	missing := make([]Person, 0, 64)
	for _, p := range persons {
		if !p.HasPrimary {
			missing = append(missing, p)
		}
	}
	log.Printf("[manage] avatars: scanned=%d missing=%d", len(persons), len(missing))
	return map[string]any{
		"scanned": len(persons),
		"missing": missing,
	}, nil
}

// AvatarProgress is the per-run progress snapshot for the UI.
type AvatarProgress struct {
	Total   int      `json:"total"`
	Done    int      `json:"done"`
	Updated int      `json:"updated"`
	Failed  int      `json:"failed"`
	Current string   `json:"current"`
	Fails   []string `json:"fails,omitempty"`
}

var (
	avatarProgressMu sync.Mutex
	avatarProgress   *AvatarProgress
)

func currentAvatarProgress() map[string]any {
	avatarProgressMu.Lock()
	defer avatarProgressMu.Unlock()
	s := avatarStatus.get()
	if avatarProgress != nil {
		s["total"] = avatarProgress.Total
		s["done"] = avatarProgress.Done
		s["updated"] = avatarProgress.Updated
		s["failed"] = avatarProgress.Failed
		s["current"] = avatarProgress.Current
	}
	return s
}

// CompleteMissingAvatars fills missing avatars for the given persons
// (nil/empty = all currently-missing persons). Name resolution goes through
// the actor mapping table (aliases -> canonical zh_cn / jp), then the
// Gfriends provider fetches the avatar image — the same fallback order as
// avdb's gfriends-direct path. Runs in the background; poll
// GetAvatarProgress for progress.
func CompleteMissingAvatars(persons []Person) (map[string]any, error) {
	c := getConfig()
	if c == nil {
		return nil, ErrNotConfigured
	}
	if !avatarStatus.start() {
		return nil, fmt.Errorf("补全任务正在进行中，请稍候")
	}
	client, err := newEmbyClient(c)
	if err != nil {
		avatarStatus.fail(err.Error())
		return nil, err
	}
	if len(persons) == 0 {
		scan, err := ScanMissingAvatars()
		if err != nil {
			avatarStatus.fail(err.Error())
			return nil, err
		}
		persons = scan["missing"].([]Person)
	}
	if len(persons) == 0 {
		avatarStatus.finish(map[string]any{
			"total": 0, "updated": 0, "failed": 0,
			"message": "没有缺头像的演员",
		})
		return map[string]any{"started": true, "total": 0}, nil
	}

	am := ActorMapping()
	gf, err := provider.GetActorProviderByName("Gfriends")
	if err != nil {
		avatarStatus.fail("Gfriends provider 不可用: " + err.Error())
		return nil, fmt.Errorf("Gfriends provider 不可用: %w", err)
	}

	avatarProgressMu.Lock()
	avatarProgress = &AvatarProgress{Total: len(persons)}
	avatarProgressMu.Unlock()

	go func() {
		start := time.Now()
		for _, p := range persons {
			avatarProgressMu.Lock()
			avatarProgress.Done++
			avatarProgress.Current = p.Name
			avatarProgressMu.Unlock()

			imageURL, err := resolveAvatarURL(am, gf, p.Name)
			if err != nil || imageURL == "" {
				avatarProgressMu.Lock()
				avatarProgress.Failed++
				avatarProgress.Fails = append(avatarProgress.Fails, fmt.Sprintf("%s: 未找到头像", p.Name))
				avatarProgressMu.Unlock()
				continue
			}
			data, contentType, err := client.DownloadURL(imageURL)
			if err != nil || len(data) == 0 {
				avatarProgressMu.Lock()
				avatarProgress.Failed++
				avatarProgress.Fails = append(avatarProgress.Fails, fmt.Sprintf("%s: 下载失败 %v", p.Name, err))
				avatarProgressMu.Unlock()
				continue
			}
			if contentType == "" || !strings.HasPrefix(contentType, "image/") {
				contentType = detectImageContentType(data)
			}
			if err := client.UploadPersonImage(p.ID, data, contentType); err != nil {
				avatarProgressMu.Lock()
				avatarProgress.Failed++
				avatarProgress.Fails = append(avatarProgress.Fails, fmt.Sprintf("%s: 上传失败 %v", p.Name, err))
				avatarProgressMu.Unlock()
				continue
			}
			avatarProgressMu.Lock()
			avatarProgress.Updated++
			avatarProgressMu.Unlock()
			// Be gentle with Emby and image sources.
			time.Sleep(300 * time.Millisecond)
		}
		avatarProgressMu.Lock()
		result := map[string]any{
			"total":   avatarProgress.Total,
			"updated": avatarProgress.Updated,
			"failed":  avatarProgress.Failed,
			"fails":   avatarProgress.Fails,
			"elapsed": time.Since(start).Round(time.Second).String(),
		}
		avatarProgressMu.Unlock()
		log.Printf("[manage] avatars: completion done total=%d updated=%d failed=%d elapsed=%s",
			result["total"], result["updated"], result["failed"], result["elapsed"])
		avatarStatus.finish(result)
	}()

	return map[string]any{"started": true, "total": len(persons)}, nil
}

// GetAvatarProgress returns the current completion progress.
func GetAvatarProgress() map[string]any {
	return currentAvatarProgress()
}

// resolveAvatarURL maps a display name to an avatar image URL via the
// mapping table and the Gfriends provider.
func resolveAvatarURL(am *mapping.ActorMap, gf provider.ActorProvider, name string) (string, error) {
	candidates := []string{name}
	if am != nil {
		if entry, ok := am.Lookup(name); ok {
			for _, n := range []string{entry.Name, entry.Jp, entry.ZhTw} {
				if n != "" && n != name {
					candidates = append(candidates, n)
				}
			}
			candidates = append(candidates, entry.Aliases...)
		}
	}
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, dup := seen[candidate]; dup {
			continue
		}
		seen[candidate] = struct{}{}
		info, err := gf.GetActorInfoByID(candidate)
		if err != nil || info == nil || len(info.Images) == 0 {
			continue
		}
		return info.Images[0], nil
	}
	return "", nil
}

func detectImageContentType(data []byte) string {
	switch {
	case len(data) > 3 && data[0] == 0xFF && data[1] == 0xD8:
		return "image/jpeg"
	case len(data) > 8 && string(data[1:4]) == "PNG":
		return "image/png"
	case len(data) > 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a"):
		return "image/gif"
	case len(data) > 12 && string(data[8:12]) == "WEBP":
		return "image/webp"
	default:
		return "image/jpeg"
	}
}
