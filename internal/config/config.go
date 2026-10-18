// Package config loads runtime configuration from environment variables.
//
// Every option has a sensible default so that `docker run` with a single
// volume works out of the box. See docs/configuration.md for the full list.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime settings.
type Config struct {
	Host           string
	Port           int
	BaseURL        string
	DataDir        string
	DBPath         string
	FilesDir       string
	LogLevel       string
	LogFormat      string // text | json
	TrustProxy     bool
	MetricsEnabled bool

	MaxUploadBytes int64

	AuthMode             string // local | proxy | none
	AuthProxyHeader      string
	AuthProxyEmailHeader string
	SessionTTL           time.Duration

	// Sign-in with an OpenID Connect provider (PocketID, Authelia, Keycloak…) next to or instead of passwords.
	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCName         string // button label
	OIDCAutoCreate   bool   // unknown emails get a member account
	PasswordLogin    bool   // false = only the provider (tokens keep working)
	SecureCookies    bool
	AdminEmail       string
	AdminPassword    string
	AdminName        string

	DefaultCurrency string
	FXProvider      string // auto | frankfurter | erapi | none

	// Integration defaults; the values saved under Settings win.
	FireflyURL      string
	FireflyToken    string
	PicoURL         string
	FireflyImport   bool // import everything once on the first start (empty database)
	FireflyMirror   bool // keep pulling new Firefly transactions every 30 minutes
	NotifyURL       string
	NotifyFormat    string // ntfy | apprise | json
	NotifyHour      int
	OpenAIKey       string
	OpenAIModel     string
	OpenAIBaseURL   string
	TravelURL       string
	TravelToken     string
	TravelPublicURL string

	// Outbound lookups identify as this.
	UserAgent string

	DemoData bool
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func envBool(key string, def bool) bool {
	switch strings.ToLower(env(key, "")) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func envInt(key string, def int) int {
	if v := env(key, ""); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envInt64(key string, def int64) int64 {
	if v := env(key, ""); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := env(key, ""); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

// Load reads configuration from the environment.
func Load() (*Config, error) {
	dataDir := env("DATA_DIR", "./data")
	c := &Config{
		Host:           env("HOST", "0.0.0.0"),
		Port:           envInt("PORT", 8080),
		BaseURL:        strings.TrimRight(env("BASE_URL", ""), "/"),
		DataDir:        dataDir,
		DBPath:         env("DB_PATH", dataDir+"/pocket-money.db"),
		FilesDir:       env("FILES_DIR", dataDir+"/files"),
		LogLevel:       strings.ToLower(env("LOG_LEVEL", "info")),
		LogFormat:      strings.ToLower(env("LOG_FORMAT", "text")),
		TrustProxy:     envBool("TRUST_PROXY", true),
		MetricsEnabled: envBool("METRICS_ENABLED", true),

		MaxUploadBytes: envInt64("MAX_UPLOAD_BYTES", 50<<20),

		AuthMode:             strings.ToLower(env("AUTH_MODE", "local")),
		AuthProxyHeader:      env("AUTH_PROXY_HEADER", "Remote-User"),
		AuthProxyEmailHeader: env("AUTH_PROXY_EMAIL_HEADER", "Remote-Email"),
		SessionTTL:           envDuration("SESSION_TTL", 720*time.Hour),
		OIDCIssuer:           strings.TrimRight(env("OIDC_ISSUER", ""), "/"),
		OIDCClientID:         env("OIDC_CLIENT_ID", ""),
		OIDCClientSecret:     env("OIDC_CLIENT_SECRET", ""),
		OIDCName:             env("OIDC_NAME", "single sign-on"),
		OIDCAutoCreate:       envBool("OIDC_AUTO_CREATE", false),
		PasswordLogin:        envBool("PASSWORD_LOGIN", true),
		SecureCookies:        envBool("SECURE_COOKIES", false),
		AdminEmail:           strings.ToLower(env("ADMIN_EMAIL", "")),
		AdminPassword:        env("ADMIN_PASSWORD", ""),
		AdminName:            env("ADMIN_NAME", "Admin"),

		DefaultCurrency: strings.ToUpper(env("DEFAULT_CURRENCY", "EUR")),
		FXProvider:      strings.ToLower(env("FX_PROVIDER", "auto")),

		FireflyURL:      strings.TrimRight(env("FIREFLY_URL", ""), "/"),
		FireflyToken:    env("FIREFLY_TOKEN", ""),
		PicoURL:         strings.TrimRight(env("PICO_URL", ""), "/"),
		FireflyImport:   envBool("FIREFLY_IMPORT_ON_START", true),
		FireflyMirror:   envBool("FIREFLY_MIRROR", false),
		NotifyURL:       env("NOTIFY_URL", ""),
		NotifyFormat:    strings.ToLower(env("NOTIFY_FORMAT", "")),
		NotifyHour:      envInt("NOTIFY_HOUR", 9),
		OpenAIKey:       env("OPENAI_API_KEY", ""),
		OpenAIModel:     env("OPENAI_MODEL", ""),
		OpenAIBaseURL:   strings.TrimRight(env("OPENAI_BASE_URL", ""), "/"),
		TravelURL:       strings.TrimRight(env("TRAVEL_URL", ""), "/"),
		TravelToken:     env("TRAVEL_TOKEN", ""),
		TravelPublicURL: strings.TrimRight(env("TRAVEL_PUBLIC_URL", ""), "/"),

		UserAgent: env("USER_AGENT", "PocketMoney/1.0 (+https://github.com/abjelosevic88/pocket-homelab-money)"),

		DemoData: envBool("DEMO_DATA", false),
	}
	switch c.AuthMode {
	case "local", "proxy", "none":
	default:
		return nil, fmt.Errorf("AUTH_MODE must be one of local, proxy, none (got %q)", c.AuthMode)
	}
	if c.OIDCIssuer != "" && (c.OIDCClientID == "" || c.BaseURL == "") {
		return nil, fmt.Errorf("OIDC_ISSUER needs OIDC_CLIENT_ID and BASE_URL (the callback is BASE_URL/api/v1/auth/oidc/callback)")
	}
	if !c.PasswordLogin && c.OIDCIssuer == "" {
		c.PasswordLogin = true // never lock everyone out
	}
	switch c.FXProvider {
	case "auto", "frankfurter", "erapi", "none":
	default:
		return nil, fmt.Errorf("FX_PROVIDER must be one of auto, frankfurter, erapi, none (got %q)", c.FXProvider)
	}
	if c.Port <= 0 || c.Port > 65535 {
		return nil, fmt.Errorf("PORT out of range: %d", c.Port)
	}
	if c.NotifyHour < 0 || c.NotifyHour > 23 {
		c.NotifyHour = 9
	}
	return c, nil
}

// Addr returns host:port.
func (c *Config) Addr() string { return fmt.Sprintf("%s:%d", c.Host, c.Port) }
