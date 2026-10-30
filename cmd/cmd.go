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

	"github.com/metatube-community/metatube-sdk-go/database"
	"github.com/metatube-community/metatube-sdk-go/engine"
	"github.com/metatube-community/metatube-sdk-go/internal/envconfig"
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

	// timeout must >= 1 second
	if Config.RequestTimeout >= time.Second {
		opts = append(opts, engine.WithRequestTimeout(Config.RequestTimeout))
	}

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

	// metadata translation with primary/secondary failover.
	if Config.Translate && Config.TranslateEngines != "" {
		if tr := buildTranslator(); tr != nil {
			opts = append(opts, engine.WithTranslator(tr))
		}
	}
	if Config.TranslateLang != "" {
		opts = append(opts, engine.WithTranslateLang(Config.TranslateLang))
	}
	if Config.DisableSummary {
		opts = append(opts, engine.WithDisableSummary(true))
	}

	app := engine.New(db, opts...)

	// always enable auto migrate for sqlite DB
	if app.DBDriver() == database.Sqlite {
		Config.DBAutoMigrate = true
	}
	if err = app.DBAutoMigrate(Config.DBAutoMigrate); err != nil {
		log.Fatal(err)
	}

	var token auth.Validator
	if Config.Token != "" {
		token = auth.Token(Config.Token)
	}

	return route.New(app, token)
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
