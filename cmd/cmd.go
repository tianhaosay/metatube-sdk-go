package cmd

import (
	"encoding/json"
	goflag "flag"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/peterbourgon/ff/v3"

	"github.com/metatube-community/metatube-sdk-go/common/enrich"
	"github.com/metatube-community/metatube-sdk-go/database"
	"github.com/metatube-community/metatube-sdk-go/engine"
	"github.com/metatube-community/metatube-sdk-go/internal/envconfig"
	"github.com/metatube-community/metatube-sdk-go/manage"
	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/route"
	"github.com/metatube-community/metatube-sdk-go/route/auth"
)

var Config = &struct {
	// main config
	Bind  string
	Port  string
	Token string
	DSN   string

	// engine config
	RequestTimeout time.Duration

	// database config
	DBMaxIdleConns int
	DBMaxOpenConns int
	DBAutoMigrate  bool
	DBPreparedStmt bool

	// metadata translation config
	Translate                bool
	TranslateEngines         string // CSV: primary,secondary
	TranslateLang            string
	TranslateTimeout         time.Duration
	TranslateSwitchThreshold int

	// summary switch
	DisableSummary bool

	// manage module (actor management web UI)
	Manage           bool
	ManageEmbyURL    string
	ManageEmbyAPIKey string
	ManageEmbyUserID string
	// ManageEmbyLibraries is a CSV of Emby library ids to scope scanning.
	ManageEmbyLibraries string

	// mapping tables (applied to every provider by the enrich pipeline)
	MappingActorFile    string
	MappingInfoFile     string
	MappingOnline       bool
	MappingSyncInterval time.Duration

	// GenreDictFile is the persistent Japanese->Chinese tag dictionary.
	// Empty means "<dir of mapping files>/genre_dict.json".
	GenreDictFile string

	// TMDB (optional) - used by the actor completion job for avatars/gender.
	EmbyPluginDir string


	// ActorFallback is a CSV of provider names consulted when a scrape
	// returned no actors (e.g. "JavBus"). Empty disables the fallback.
	ActorFallback string

	// version flag
	VersionFlag bool
}{}

func init() {
	// gin init
	gin.DisableConsoleColor()

	// flag init
	flag := goflag.NewFlagSet("", goflag.ExitOnError)

	// flag parse
	flag.StringVar(&Config.Bind, "bind", "", "Bind address of server")
	flag.StringVar(&Config.Port, "port", "8080", "Port number of server")
	flag.StringVar(&Config.Token, "token", "", "Token to access server")
	flag.StringVar(&Config.DSN, "dsn", "", "Database Service Name")
	flag.DurationVar(&Config.RequestTimeout, "request-timeout", engine.DefaultRequestTimeout, "Timeout per request")
	flag.IntVar(&Config.DBMaxIdleConns, "db-max-idle-conns", 0, "Database max idle connections")
	flag.IntVar(&Config.DBMaxOpenConns, "db-max-open-conns", 0, "Database max open connections")
	flag.BoolVar(&Config.DBAutoMigrate, "db-auto-migrate", false, "Database auto migration")
	flag.BoolVar(&Config.DBPreparedStmt, "db-prepared-stmt", false, "Database prepared statement")

	// Metadata translation (engine-level, applies to all providers).
	flag.BoolVar(&Config.Translate, "translate", false, "Enable metadata translation (title/summary)")
	flag.StringVar(&Config.TranslateEngines, "translate-engines", "", "Translation engines: primary,secondary (e.g. deepl,googlefree)")
	flag.StringVar(&Config.TranslateLang, "translate-lang", "zh-CN", "Translation target language")
	flag.DurationVar(&Config.TranslateTimeout, "translate-timeout", 30*time.Second, "Per-engine translation timeout")
	flag.IntVar(&Config.TranslateSwitchThreshold, "translate-switch-threshold", 5, "Consecutive takeovers before secondary is promoted to primary")

	// Summary switch.
	flag.BoolVar(&Config.DisableSummary, "disable-summary", false, "Disable movie summaries (no scrape output, no translation)")

	// Manage module (actor management web UI).
	flag.BoolVar(&Config.Manage, "manage", false, "Enable the actor management web UI (/manage/)")
	flag.StringVar(&Config.EmbyPluginDir, "emby-plugin-dir", "/emby-plugins", "Read-only mount of Emby plugin configurations (for conflict checks)")
	flag.StringVar(&Config.ManageEmbyURL, "manage-emby-url", "", "Emby server base URL for the manage module")
	flag.StringVar(&Config.ManageEmbyAPIKey, "manage-emby-api-key", "", "Emby API key for the manage module")
	flag.StringVar(&Config.ManageEmbyUserID, "manage-emby-user-id", "", "Emby user id for the manage module")
	flag.StringVar(&Config.ManageEmbyLibraries, "manage-emby-libraries", "", "CSV of Emby library ids to scope actor scans")

	// Mapping tables (applied to every provider by the enrich pipeline).
	flag.StringVar(&Config.MappingActorFile, "mapping-actor-file", "", "Local actor mapping table path (synced from the online source when enabled)")
	flag.StringVar(&Config.MappingInfoFile, "mapping-info-file", "", "Local genre/tag mapping table path (mdc mapping_info.xml format)")
	flag.StringVar(&Config.GenreDictFile, "genre-dict", "", "Persistent genre dictionary path (default: next to the mapping files)")
	flag.StringVar(&Config.ActorFallback, "actor-fallback", "JavDB", "CSV of providers used to fill missing actors (JavBus mirrors DMM, so JavDB is the only useful source)")
	flag.BoolVar(&Config.MappingOnline, "mapping-online", true, "Enable online actor-mapping source sync")
	flag.DurationVar(&Config.MappingSyncInterval, "mapping-sync-interval", 24*time.Hour, "Online mapping sync interval")

	flag.BoolVar(&Config.VersionFlag, "version", false, "Show version")
	ff.Parse(flag, os.Args[1:], ff.WithEnvVars())
	applyMetaTubeEnvOverrides()
}

// applyMetaTubeEnvOverrides applies MT_ prefixed environment variables on
// top of ff's plain-name env parsing, so that translate settings follow the
// same MT_* convention as the provider configs.
func applyMetaTubeEnvOverrides() {
	if v := os.Getenv("MT_TRANSLATE"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			Config.Translate = b
		}
	}
	if v := os.Getenv("MT_TRANSLATE_ENGINES"); v != "" {
		Config.TranslateEngines = v
	}
	if v := os.Getenv("MT_TRANSLATE_LANG"); v != "" {
		Config.TranslateLang = v
	}
	if v := os.Getenv("MT_TRANSLATE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			Config.TranslateTimeout = d
		}
	}
	if v := os.Getenv("MT_TRANSLATE_SWITCH_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			Config.TranslateSwitchThreshold = n
		}
	}
	if v := os.Getenv("MT_DISABLE_SUMMARY"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			Config.DisableSummary = b
		}
	}
	if v := os.Getenv("MT_MANAGE"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			Config.Manage = b
		}
	}
	if v := os.Getenv("MT_MANAGE_EMBY_URL"); v != "" {
		Config.ManageEmbyURL = v
	}
	if v := os.Getenv("MT_MANAGE_EMBY_API_KEY"); v != "" {
		Config.ManageEmbyAPIKey = v
	}
	if v := os.Getenv("MT_MANAGE_EMBY_USER_ID"); v != "" {
		Config.ManageEmbyUserID = v
	}
	if v := os.Getenv("MT_MANAGE_EMBY_LIBRARIES"); v != "" {
		Config.ManageEmbyLibraries = v
	}
	if v := os.Getenv("MT_MAPPING_ACTOR_FILE"); v != "" {
		Config.MappingActorFile = v
	}
	if v := os.Getenv("MT_MAPPING_INFO_FILE"); v != "" {
		Config.MappingInfoFile = v
	}
	if v := os.Getenv("MT_GENRE_DICT"); v != "" {
		Config.GenreDictFile = v
	}
	if v := os.Getenv("MT_EMBY_PLUGIN_DIR"); v != "" {
		Config.EmbyPluginDir = v
	}
	if v := os.Getenv("MT_ACTOR_FALLBACK"); v != "" {
		Config.ActorFallback = v
	}
	if v := os.Getenv("MT_MAPPING_ONLINE"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			Config.MappingOnline = b
		}
	}
	if v := os.Getenv("MT_MAPPING_SYNC_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			Config.MappingSyncInterval = d
		}
	}
}

func Router(names ...string) *gin.Engine {
	db, err := database.Open(&database.Config{
		DSN:                  Config.DSN,
		PreparedStmt:         Config.DBPreparedStmt,
		MaxIdleConns:         Config.DBMaxIdleConns,
		MaxOpenConns:         Config.DBMaxOpenConns,
		DisableAutomaticPing: true,
	})
	if err != nil {
		log.Fatal(err)
	}

	// engine options
	var opts []engine.Option

	// Mirror standard/engine/gin logs into the manage console ring buffer.
	// Must run before engine.New and route.New.
	engineLogger := manage.InstallLogCapture()

	// timeout must >= 1 second
	if Config.RequestTimeout >= time.Second {
		opts = append(opts, engine.WithRequestTimeout(Config.RequestTimeout))
	}
	opts = append(opts, engine.WithLogger(engineLogger))

	// specify engine name
	for _, name := range names {
		opts = append(opts, engine.WithEngineName(name))
	}

	// // set actor provider configs if any
	for provider, config := range envconfig.ActorProviderConfigs.Iterator() {
		opts = append(opts, engine.WithActorProviderConfig(provider, config))
	}

	// set movie provider configs if any
	for provider, config := range envconfig.MovieProviderConfigs.Iterator() {
		opts = append(opts, engine.WithMovieProviderConfig(provider, config))
	}

	// One metadata pipeline for every provider: actor/genre mapping, summary
	// handling and translation are applied centrally, before caching.
	pipe := buildEnrichPipeline()
	opts = append(opts, engine.WithEnrich(pipe))

	app := engine.New(db, opts...)

	// always enable auto migrate for sqlite DB
	if app.DBDriver() == database.Sqlite {
		Config.DBAutoMigrate = true
	}

	// The manage console mutates the same pipeline at runtime. Translation is
	// built from the native Emby plugin settings after manage.Setup() because
	// that is when the plugin-config directory becomes available.
	manage.SetPipeline(pipe)
	if err = app.DBAutoMigrate(Config.DBAutoMigrate); err != nil {
		log.Fatal(err)
	}

	var token auth.Validator
	if Config.Token != "" {
		token = auth.Token(Config.Token)
	}

	ginApp := route.New(app, token)
	// Liveness probe is always mounted, independent of the console.
	ginApp.GET("/healthz", manage.HealthHandler)

	// Manage module (actor management web UI).
	if Config.Manage && Config.ManageEmbyURL != "" {
		var libraries []string
		for _, lib := range strings.Split(Config.ManageEmbyLibraries, ",") {
			if lib = strings.TrimSpace(lib); lib != "" {
				libraries = append(libraries, lib)
			}
		}
		manage.Setup(&manage.Config{
			EmbyURL:             Config.ManageEmbyURL,
			EmbyAPIKey:          Config.ManageEmbyAPIKey,
			EmbyUserID:          Config.ManageEmbyUserID,
			EmbyLibraryIDs:      libraries,
			ActorMappingPath:    Config.MappingActorFile,
			EmbyPluginDir:       Config.EmbyPluginDir,
			MappingOnline:       Config.MappingOnline && Config.MappingActorFile != "",
			MappingSyncInterval: Config.MappingSyncInterval,
		})
		manage.SetBackendURL("http://127.0.0.1:" + Config.Port)
		manage.StartPluginTranslationWatch()
		manage.RegisterRoutes(ginApp, route.RequireToken(token))
	} else if Config.MappingOnline && Config.MappingActorFile != "" {
		// Mapping sync without the manage UI still runs in the background.
		manage.Setup(&manage.Config{
			ActorMappingPath:    Config.MappingActorFile,
			EmbyPluginDir:       Config.EmbyPluginDir,
			MappingOnline:       true,
			MappingSyncInterval: Config.MappingSyncInterval,
		})
	}

	return ginApp
}

// buildEnrichPipeline assembles the single post-processing pipeline used by
// every provider. Mapping tables load lazily: a missing file (e.g. before the
// first online sync) only disables that step until the table appears, instead
// of staying off until the next restart.
func buildEnrichPipeline() *enrich.Pipeline {
	cfg := enrich.Config{
		TranslateLang:  Config.TranslateLang,
		DisableSummary: Config.DisableSummary,
		CleanSummary:   true,
		// ActorFallbackDisabled keeps its zero value (fallback on); the plugin
		// settings page can turn it off and the backend follows at runtime.
	}
	if Config.MappingActorFile != "" {
		cfg.Actors = enrich.NewLazyActor(Config.MappingActorFile)
	}
	if Config.MappingInfoFile != "" {
		cfg.Info = enrich.NewLazyInfo(Config.MappingInfoFile)
	}
	cfg.Genres = enrich.LoadGenreDict(genreDictPath())
	cfg.ActorFallback = buildActorFallback()
	return enrich.New(cfg)
}

// javNumberRe matches a standard JAV number (FLAV-414, BLOR-211, START-015,
// T-38027). It deliberately rejects FC2-PPV-xxx and other multi-segment
// labels, where an actor lookup would never pay off.
var javNumberRe = regexp.MustCompile(`(?i)^[a-z]{1,10}-?\d{2,6}$`)

// negativeActors remembers numbers whose fallback already failed, so a
// library refresh does not hammer JavDB for the same unfixable titles.
var (
	negativeMu    sync.Mutex
	negativeUntil = map[string]time.Time{}
)

const negativeTTL = 12 * time.Hour

// negativeFilePath stores the negative cache next to the mapping tables so a
// restart does not re-query JavDB for titles that have no actors anywhere.
func negativeFilePath() string {
	if Config.MappingActorFile == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(Config.MappingActorFile), "negative_actors.json")
}

var negativeLoaded bool

func loadNegativeCache() {
	negativeMu.Lock()
	defer negativeMu.Unlock()
	if negativeLoaded {
		return
	}
	negativeLoaded = true
	path := negativeFilePath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var raw map[string]int64
	if json.Unmarshal(data, &raw) != nil {
		return
	}
	now := time.Now()
	for k, v := range raw {
		if t := time.Unix(v, 0); t.After(now) {
			negativeUntil[k] = t
		}
	}
	if len(negativeUntil) > 0 {
		log.Printf("[enrich] negative actor cache loaded: %d entries", len(negativeUntil))
	}
}

func saveNegativeCacheLocked() {
	path := negativeFilePath()
	if path == "" {
		return
	}
	raw := make(map[string]int64, len(negativeUntil))
	for k, v := range negativeUntil {
		raw[k] = v.Unix()
	}
	if data, err := json.Marshal(raw); err == nil {
		_ = os.WriteFile(path, data, 0o644)
	}
}

func negativeHit(number string) bool {
	loadNegativeCache()
	negativeMu.Lock()
	defer negativeMu.Unlock()
	until, ok := negativeUntil[number]
	if !ok {
		return false
	}
	if time.Now().After(until) {
		delete(negativeUntil, number)
		return false
	}
	return true
}

func negativeMark(number string) {
	loadNegativeCache()
	negativeMu.Lock()
	negativeUntil[number] = time.Now().Add(negativeTTL)
	saveNegativeCacheLocked()
	negativeMu.Unlock()
	log.Printf("[FALLBACK] number=%s result=miss (12h 内不再尝试)", number)
}

// genreDictPath resolves the tag dictionary location: explicit config wins,
// otherwise it lives next to the mapping tables.
func genreDictPath() string {
	if Config.GenreDictFile != "" {
		return Config.GenreDictFile
	}
	if Config.MappingActorFile != "" {
		return filepath.Join(filepath.Dir(Config.MappingActorFile), "genre_dict.json")
	}
	return ""
}

// buildActorFallback fills in actors when the primary provider returned none.
//
// Why JavDB and not JavBus: JavBus mirrors DMM, so every title DMM has no
// actors for is equally empty there (confirmed on FLAV-414 / BLOR-211). JavDB
// maintains its own cast list and is the only source that can fill the gap.
//
// The lookup runs only for movies that came back with zero actors, so the
// common path never pays for it. JavDB's global 10s cooldown (common/throttle)
// still applies; failures are cached for 12h to keep full-library refreshes
// from retrying the same dead title.
func buildActorFallback() func(number string) []string {
	providers := make([]provider.MovieProvider, 0, 2)
	for _, name := range strings.Split(Config.ActorFallback, ",") {
		if name = strings.TrimSpace(name); name == "" {
			continue
		}
		p, err := provider.GetMovieProviderByName(name)
		if err != nil {
			log.Printf("[enrich] actor fallback %q unavailable: %v", name, err)
			continue
		}
		providers = append(providers, p)
	}
	if len(providers) == 0 {
		return nil
	}
	return func(number string) []string {
		n := strings.TrimSpace(number)
		if n == "" || !javNumberRe.MatchString(n) || negativeHit(n) {
			return nil
		}
		for _, p := range providers {
			info, err := p.GetMovieInfoByID(n)
			if err != nil || info == nil {
				continue
			}
			if actors := []string(info.Actors); len(actors) > 0 {
				log.Printf("[FALLBACK] number=%s provider=%s result=ok actors=%d", n, p.Name(), len(actors))
				return actors
			}
		}
		negativeMark(n)
		return nil
	}
}

