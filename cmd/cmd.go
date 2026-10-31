package cmd

import (
	goflag "flag"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/peterbourgon/ff/v3"

	"github.com/metatube-community/metatube-sdk-go/common/enrich"
	"github.com/metatube-community/metatube-sdk-go/database"
	"github.com/metatube-community/metatube-sdk-go/engine"
	"github.com/metatube-community/metatube-sdk-go/internal/envconfig"
	"github.com/metatube-community/metatube-sdk-go/manage"
	"github.com/metatube-community/metatube-sdk-go/route"
	"github.com/metatube-community/metatube-sdk-go/route/auth"
	"github.com/metatube-community/metatube-sdk-go/translate"
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
	flag.StringVar(&Config.ManageEmbyURL, "manage-emby-url", "", "Emby server base URL for the manage module")
	flag.StringVar(&Config.ManageEmbyAPIKey, "manage-emby-api-key", "", "Emby API key for the manage module")
	flag.StringVar(&Config.ManageEmbyUserID, "manage-emby-user-id", "", "Emby user id for the manage module")
	flag.StringVar(&Config.ManageEmbyLibraries, "manage-emby-libraries", "", "CSV of Emby library ids to scope actor scans")

	// Mapping tables (applied to every provider by the enrich pipeline).
	flag.StringVar(&Config.MappingActorFile, "mapping-actor-file", "", "Local actor mapping table path (synced from the online source when enabled)")
	flag.StringVar(&Config.MappingInfoFile, "mapping-info-file", "", "Local genre/tag mapping table path (mdc mapping_info.xml format)")
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

	// The manage console mutates the same pipeline at runtime.
	manage.SetPipeline(pipe)
	if err = app.DBAutoMigrate(Config.DBAutoMigrate); err != nil {
		log.Fatal(err)
	}

	var token auth.Validator
	if Config.Token != "" {
		token = auth.Token(Config.Token)
	}

	ginApp := route.New(app, token)

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
			MappingOnline:       Config.MappingOnline && Config.MappingActorFile != "",
			MappingSyncInterval: Config.MappingSyncInterval,
		})
		manage.RegisterRoutes(ginApp, route.RequireToken(token))
	} else if Config.MappingOnline && Config.MappingActorFile != "" {
		// Mapping sync without the manage UI still runs in the background.
		manage.Setup(&manage.Config{
			ActorMappingPath:    Config.MappingActorFile,
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
	}
	if Config.MappingActorFile != "" {
		cfg.Actors = enrich.NewLazyActor(Config.MappingActorFile)
	}
	if Config.MappingInfoFile != "" {
		cfg.Info = enrich.NewLazyInfo(Config.MappingInfoFile)
	}
	if Config.Translate && Config.TranslateEngines != "" {
		cfg.Translator = buildTranslator()
	}
	return enrich.New(cfg)
}

// buildTranslator creates a failover-capable metadata translator from the
// translate* config. Engine parameters come from environment variables in
// the form MT_TRANSLATE_<ENGINE>__<PARAM>, where PARAM matches the engine's
// JSON-tagged fields case-insensitively ignoring '-'/'_'
// (e.g. MT_TRANSLATE_DEEPL__DEEPL_API_URL=...).
func buildTranslator() translate.Translator {
	engineNames := strings.Split(Config.TranslateEngines, ",")
	for i := range engineNames {
		engineNames[i] = strings.TrimSpace(engineNames[i])
	}
	if engineNames[0] == "" {
		return nil
	}
	params := parseTranslateEngineParams()
	build := func(name string) (translate.Translator, string) {
		if name == "" {
			return nil, ""
		}
		if !translate.Exists(name) {
			log.Printf("[TRANSLATE] unknown engine: %s", name)
			return nil, ""
		}
		return translate.Build(name, params[strings.ToUpper(name)]), name
	}
	primary, primaryName := build(engineNames[0])
	if primary == nil {
		return nil
	}
	var secondary translate.Translator
	secondaryName := ""
	if len(engineNames) > 1 && engineNames[1] != "" {
		secondary, secondaryName = build(engineNames[1])
		if secondary == nil {
			secondaryName = ""
		}
	}
	log.Printf("[TRANSLATE] enabled: primary=%s secondary=%s lang=%s timeout=%s switch-threshold=%d",
		primaryName, secondaryName, Config.TranslateLang, Config.TranslateTimeout, Config.TranslateSwitchThreshold)
	return translate.NewFailover(primary, primaryName, secondary, secondaryName, Config.TranslateTimeout, Config.TranslateSwitchThreshold)
}

// parseTranslateEngineParams collects MT_TRANSLATE_<ENGINE>__<PARAM> envs.
func parseTranslateEngineParams() map[string]map[string]string {
	result := map[string]map[string]string{}
	const prefix = "MT_TRANSLATE_"
	for _, env := range os.Environ() {
		key, value, found := strings.Cut(env, "=")
		if !found || !strings.HasPrefix(key, prefix) {
			continue
		}
		rest := strings.TrimPrefix(key, prefix)
		engine, param, found := strings.Cut(rest, "__")
		if !found || engine == "" || param == "" {
			continue
		}
		if result[engine] == nil {
			result[engine] = map[string]string{}
		}
		result[engine][param] = value
	}
	return result
}
