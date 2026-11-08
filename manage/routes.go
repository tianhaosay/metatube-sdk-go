package manage

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// uiBasePath is where the console is mounted. It is a variable (not a constant)
// so a future release can change the mount point in one place.
const uiBasePath = "/ui"

// RegisterRoutes mounts the console UI and its JSON API on the given router.
//
// The UI lives at /ui (and /ui/), the JSON API at /ui/api/*. The previous
// /manage paths are gone on purpose: one canonical URL keeps bookmarks and
// documentation from drifting.
//
// apiMiddleware is applied to the JSON API only (the HTML shell stays public
// so the UI can prompt for a token); pass route.RequireToken(token) to make
// the console respect the server token.
func RegisterRoutes(r gin.IRouter, apiMiddleware ...gin.HandlerFunc) {
	r.GET(uiBasePath, uiHandler)
	r.GET(uiBasePath+"/", uiHandler)

	api := r.Group(uiBasePath + "/api")
	api.Use(corsMiddleware)
	if len(apiMiddleware) > 0 {
		api.Use(apiMiddleware...)
	}
	{
		api.GET("/status", getStatus)
		api.GET("/audit", getAudit)
		api.POST("/fix", postFix)
		api.POST("/refresh/start", postRefreshStart)
		api.POST("/refresh/stop", postRefreshStop)
		api.GET("/refresh/status", getRefreshStatus)
		api.POST("/refresh/reset", postRefreshReset)
		api.GET("/paths", getPaths)
		api.POST("/mapping/sync", postSyncMapping)
		api.GET("/logs", getLogs)
		api.GET("/settings", getSettings)
		api.POST("/settings", postSettings)
		api.GET("/consistency", getConsistency)
		api.GET("/plugin-translation", getPluginTranslation)
		api.POST("/translate-test", postTranslateTest)
	}
}

// corsMiddleware allows cross-origin GETs so that the Emby/Jellyfin plugin
// configuration page (served from the media server origin) can query the
// backend's consistency status. Preflight requests are answered directly.
func corsMiddleware(c *gin.Context) {
	c.Header("Access-Control-Allow-Origin", "*")
	if c.Request.Method == http.MethodOptions {
		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
		c.AbortWithStatus(http.StatusNoContent)
		return
	}
	c.Next()
}

// getConsistency reports backend state that overlaps plugin-side settings,
// used by the plugin config page to warn about conflicts.
func getConsistency(c *gin.Context) {
	s := GetSettings()
	ti := GetPluginTranslationInfo()
	jsonOK(c, gin.H{
		"translation":          ti,
		"disable_summary":      s.DisableSummary,
		"actor_fallback":       s.ActorFallback,
		"fanza_rate_limit":     s.FanzaRateLimit,
		"javdb_rate_limit":     s.JavdbRateLimit,
		"javdb_mapping_active": isJavdbMappingActive(),
		"mapping_entries":      MappingStatus()["entries"],
	})
}

// getAudit scans the library and reports which movies are incomplete.
// Query: library_id (optional), offset, limit (issues window).
func getAudit(c *gin.Context) {
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "200"))
	libID := c.Query("library_id")
	prefix := c.Query("path_prefix")
	if libID == "" && prefix == "" {
		libID = "*"
	}
	if libID == "" && prefix != "" {
		if cl, cerr := newEmbyClient(getConfig()); cerr == nil {
			libID = ResolveLibraryForPath(cl, getConfig(), prefix)
		}
	}
	res, err := Audit(AuditRequest{
		LibraryID:  libID,
		PathPrefix: prefix,
		Problem:    c.Query("problem"),
		Offset:     offset,
		Limit:      limit,
	})
	if err != nil {
		jsonErr(c, err)
		return
	}
	jsonOK(c, res)
}

// postFix runs a batch repair (refresh | rename_actors).
func postFix(c *gin.Context) {
	var req FixRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonErr(c, err)
		return
	}
	res, err := Fix(req)
	if err != nil {
		jsonErr(c, err)
		return
	}
	jsonOK(c, res)
}

func getLogs(c *gin.Context) {
	number := c.Query("number")
	level := c.Query("level")
	source := c.Query("source")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "300"))
	jsonOK(c, gin.H{
		"logs":  QueryLogs(number, level, source, limit),
		"total": len(QueryLogs(number, level, source, 5000)),
	})
}

func getSettings(c *gin.Context) {
	jsonOK(c, GetSettings())
}

// postSettings is a read-only guard: the runtime toggles live in the native
// MetaTube plugin settings page, so the console cannot change them. It replies
// with the current (plugin-mirrored) values and explains where to edit.
func postSettings(c *gin.Context) {
	jsonOK(c, gin.H{
		"read_only": true,
		"message":   "这些设置只能在 Emby → 插件 → MetaTube 里修改，后端会自动跟随",
		"settings":  GetSettings(),
	})
}

func uiHandler(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "text/html; charset=utf-8", webIndex)
}

func jsonOK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"data": data})
}

func jsonErr(c *gin.Context, err error) {
	c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": http.StatusBadRequest, "message": err.Error()}})
}

// getStatus returns everything the UI needs for its header cards.
func getStatus(c *gin.Context) {
	c2 := getConfig()
	status := map[string]any{
		"enabled": c2 != nil,
		"mapping": MappingStatus(),
	}
	if c2 == nil {
		jsonOK(c, status)
		return
	}
	client, err := newEmbyClient(c2)
	if err != nil {
		status["emby"] = map[string]any{"connected": false, "error": err.Error()}
		jsonOK(c, status)
		return
	}
	// Lightweight connectivity probe (does not pull a full person list).
	var probe struct {
		TotalRecordCount int `json:"TotalRecordCount"`
	}
	if err := client.do(http.MethodGet, "/Items", nil, nil, &probe); err != nil {
		status["emby"] = map[string]any{"connected": false, "error": err.Error()}
		jsonOK(c, status)
		return
	}
	status["emby"] = map[string]any{"connected": true, "url": c2.EmbyURL}
	jsonOK(c, status)
}

func postSyncMapping(c *gin.Context) {
	res, err := SyncMappingNow()
	if err != nil {
		if errors.Is(err, ErrNotConfigured) {
			jsonErr(c, err)
			return
		}
		jsonErr(c, err)
		return
	}
	jsonOK(c, res)
}

func HealthHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"mapping": MappingStatus()["entries"],
	})
}

// getPluginTranslation exposes the read-only translation linkage view: the
// engine/key come from the native plugin settings page.
func getPluginTranslation(c *gin.Context) {
	jsonOK(c, GetPluginTranslationInfo())
}

// postTranslateTest runs the current translator on a sample text so the
// operator can verify credentials without waiting for a real scrape.
func postTranslateTest(c *gin.Context) {
	var req struct {
		Text string `json:"text"`
	}
	_ = c.ShouldBindJSON(&req)
	if req.Text == "" {
		req.Text = "高圧系女上司にパワハラされたので、快楽の虜にしてやった"
	}
	p := getPipeline()
	if p == nil {
		jsonErr(c, fmt.Errorf("翻译未初始化"))
		return
	}
	cfg := p.Snapshot()
	if cfg.Translator == nil {
		jsonErr(c, fmt.Errorf("翻译未启用"))
		return
	}
	lang := cfg.TranslateLang
	if lang == "" {
		lang = "zh-CN"
	}
	out, err := cfg.Translator.Translate(req.Text, "auto", lang)
	if err != nil {
		jsonErr(c, err)
		return
	}
	jsonOK(c, gin.H{"source": req.Text, "translated": out, "lang": lang})
}

// postRefreshStart begins a batched library refresh with progress tracking.
func postRefreshStart(c *gin.Context) {
	var opts RefreshOptions
	_ = c.ShouldBindJSON(&opts)
	res, err := StartRefresh(opts)
	if err != nil {
		jsonErr(c, err)
		return
	}
	jsonOK(c, res)
}

func postRefreshStop(c *gin.Context) {
	jsonOK(c, StopRefresh())
}

func getRefreshStatus(c *gin.Context) {
	jsonOK(c, RefreshStatus())
}

// getPaths lists the first-level folders of the configured library so the UI
// can offer "refresh only this folder" without asking for an item id.
func getPaths(c *gin.Context) {
	cfg := getConfig()
	if cfg == nil {
		return
	}
	client, err := newEmbyClient(cfg)
	if err != nil {
		jsonErr(c, err)
		return
	}
	locations := client.LibraryLocations()
	res := make([]map[string]any, 0, 8)
	sortLib := func(items []FolderRef) {
		sort.SliceStable(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	}
	for _, lib := range cfg.EmbyLibraryIDs {
		roots := locations[lib]
		items := make([]FolderRef, 0, 8)
		sample, _, _ := client.FetchMovies(lib, 0, 400)
		for _, root := range roots {
			root = strings.TrimRight(root, "/")
			children := map[string]struct{}{}
			deepCount, sampled := 0, 0
			for _, it := range sample {
				if it.Path == "" || !pathInScope(it.Path, root) {
					continue
				}
				rel := strings.TrimPrefix(strings.TrimPrefix(it.Path, root), "/")
				segs := strings.Split(rel, "/")
				sampled++
				if len(segs) >= 3 {
					deepCount++
					children[segs[0]] = struct{}{}
				}
			}
			// Categories only make sense when most movies sit one level deeper
			// (root/category/actor/file). A flat layout (root/actor/file) offers
			// the root itself, and a huge child list means we guessed wrong.
			if sampled > 0 && deepCount*2 > sampled && len(children) > 0 && len(children) <= 20 {
				for ch := range children {
					items = append(items, FolderRef{Name: ch, Path: root + "/" + ch})
				}
			} else {
				items = append(items, FolderRef{Name: root, Path: root})
			}
		}
		sortLib(items)
		res = append(res, map[string]any{"library_id": lib, "items": items})
	}
	jsonOK(c, res)
}

// postRefreshReset forgets which items a scope already refreshed.
func postRefreshReset(c *gin.Context) {
	var body struct {
		Scope      string `json:"scope"`
		PathPrefix string `json:"path_prefix"`
	}
	_ = c.ShouldBindJSON(&body)
	jsonOK(c, ResetRefreshProgress(body.Scope, body.PathPrefix))
}
