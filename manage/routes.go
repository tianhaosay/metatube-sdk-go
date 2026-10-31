package manage

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes mounts the manage UI and its JSON API on the given router.
// apiMiddleware is applied to the JSON API only (the HTML shell stays public
// so the UI can prompt for a token); pass route.RequireToken(token) to make
// the console respect the server token.
func RegisterRoutes(r gin.IRouter, apiMiddleware ...gin.HandlerFunc) {
	r.GET("/manage/", uiHandler)
	r.GET("/manage", uiHandler)

	api := r.Group("/manage/api")
	api.Use(corsMiddleware)
	if len(apiMiddleware) > 0 {
		api.Use(apiMiddleware...)
	}
	{
		api.GET("/status", getStatus)
		api.POST("/mapping/sync", postSyncMapping)
		api.GET("/duplicates", getDuplicates)
		api.POST("/duplicates/resolve", postResolveDuplicate)
		api.GET("/avatars/missing", getMissingAvatars)
		api.POST("/avatars/complete", postCompleteAvatars)
		api.GET("/avatars/progress", getAvatarProgress)
		api.GET("/logs", getLogs)
		api.GET("/settings", getSettings)
		api.POST("/settings", postSettings)
		api.GET("/consistency", getConsistency)
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
	jsonOK(c, gin.H{
		"translate":             s.Translate,
		"translate_engines":     s.TranslateEngines,
		"disable_summary":       s.DisableSummary,
		"fanza_rate_limit":      s.FanzaRateLimit,
		"javdb_rate_limit":      s.JavdbRateLimit,
		"javdb_mapping_active":  isJavdbMappingActive(),
		"mapping_entries":       MappingStatus()["entries"],
	})
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

func postSettings(c *gin.Context) {
	var s Settings
	if err := c.ShouldBindJSON(&s); err != nil {
		jsonErr(c, err)
		return
	}
	jsonOK(c, UpdateSettings(s))
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
		"enabled":  c2 != nil,
		"mapping":  MappingStatus(),
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
	emby := map[string]any{"connected": true, "url": c2.EmbyURL}
	if ok, err := client.HasMagicToolsPlugin(); err == nil {
		emby["magic_tools"] = ok
	} else {
		emby["magic_tools_error"] = err.Error()
	}
	// Quick connectivity + person count probe.
	if persons, err := client.FetchPersons(c2.EmbyLibraryIDs); err == nil {
		emby["person_count"] = len(persons)
	} else {
		emby["connected"] = false
		emby["error"] = err.Error()
	}
	status["emby"] = emby
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

func getDuplicates(c *gin.Context) {
	res, err := DetectDuplicates()
	if err != nil {
		jsonErr(c, err)
		return
	}
	jsonOK(c, res)
}

type resolveRequest struct {
	TargetID  string   `json:"target_id"`
	SourceIDs []string `json:"source_ids"`
}

func postResolveDuplicate(c *gin.Context) {
	var req resolveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonErr(c, err)
		return
	}
	res, err := ResolveDuplicate(req.TargetID, req.SourceIDs)
	if err != nil {
		jsonErr(c, err)
		return
	}
	jsonOK(c, res)
}

func getMissingAvatars(c *gin.Context) {
	res, err := ScanMissingAvatars()
	if err != nil {
		jsonErr(c, err)
		return
	}
	jsonOK(c, res)
}

type completeRequest struct {
	Persons []Person `json:"persons"`
}

func postCompleteAvatars(c *gin.Context) {
	var req completeRequest
	_ = c.ShouldBindJSON(&req) // empty body = complete all missing.
	res, err := CompleteMissingAvatars(req.Persons)
	if err != nil {
		jsonErr(c, err)
		return
	}
	jsonOK(c, res)
}

func getAvatarProgress(c *gin.Context) {
	jsonOK(c, GetAvatarProgress())
}
