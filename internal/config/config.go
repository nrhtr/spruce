package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	DBPath          string
	ListenAddr      string
	SiteURL         string
	DevMode         bool
	DigestHour      int
	DigestTZ        string
	EmailFrom       string
	EmailTo         string
	ScanCron        string
	UrgentThreshold time.Duration

	AnthropicAPIKey string
	ClaudeModel     string

	EbayClientID     string
	EbayClientSecret string
	EbayMarketplace  string
}

func Load() *Config {
	cfg := &Config{
		DBPath:          getenv("DARKLY_DB_PATH", "darkly.db"),
		ListenAddr:      getenv("DARKLY_LISTEN_ADDR", ":8080"),
		SiteURL:         getenv("DARKLY_SITE_URL", ""),
		DevMode:         os.Getenv("DARKLY_DEV_MODE") == "true",
		DigestHour:      getenvInt("DARKLY_DIGEST_HOUR", 18),
		DigestTZ:        getenv("DARKLY_DIGEST_TZ", "Australia/Sydney"),
		EmailFrom:       getenv("DARKLY_EMAIL_FROM", "darkly@localhost"),
		EmailTo:         getenv("DARKLY_EMAIL_TO", ""),
		ScanCron:        getenv("DARKLY_SCAN_CRON", "*/30 * * * *"),
		UrgentThreshold: getenvDuration("DARKLY_URGENT_THRESHOLD", 12*time.Hour),

		AnthropicAPIKey: getenv("ANTHROPIC_API_KEY", ""),
		ClaudeModel:     getenv("DARKLY_CLAUDE_MODEL", "claude-haiku-4-5-20251001"),

		EbayClientID:     getenv("DARKLY_EBAY_CLIENT_ID", ""),
		EbayClientSecret: getenv("DARKLY_EBAY_CLIENT_SECRET", ""),
		EbayMarketplace:  getenv("DARKLY_EBAY_MARKETPLACE", "EBAY_AU"),
	}
	return cfg
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getenvDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
