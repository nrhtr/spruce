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
	AdminToken      string
	DigestHour      int
	DigestTZ        string
	EmailFrom       string
	EmailTo         string
	SMTPHost        string
	SMTPPort        string
	SMTPUser        string
	SMTPPass        string
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
		DBPath:          getenv("SPRUCE_DB_PATH", "spruce.db"),
		ListenAddr:      getenv("SPRUCE_LISTEN_ADDR", ":8080"),
		SiteURL:         getenv("SPRUCE_SITE_URL", ""),
		DevMode:         os.Getenv("SPRUCE_DEV_MODE") == "true",
		AdminToken:      getenv("SPRUCE_ADMIN_TOKEN", ""),
		DigestHour:      getenvInt("SPRUCE_DIGEST_HOUR", 18),
		DigestTZ:        getenv("SPRUCE_DIGEST_TZ", "Australia/Sydney"),
		EmailFrom:       getenv("SPRUCE_EMAIL_FROM", "spruce@localhost"),
		EmailTo:         getenv("SPRUCE_EMAIL_TO", ""),
		SMTPHost:        getenv("SPRUCE_SMTP_HOST", ""),
		SMTPPort:        getenv("SPRUCE_SMTP_PORT", "587"),
		SMTPUser:        getenv("SPRUCE_SMTP_USER", ""),
		SMTPPass:        getenv("SPRUCE_SMTP_PASS", ""),
		ScanCron:        getenv("SPRUCE_SCAN_CRON", "0 */3 * * *"),
		UrgentThreshold: getenvDuration("SPRUCE_URGENT_THRESHOLD", 12*time.Hour),

		AnthropicAPIKey: getenv("ANTHROPIC_API_KEY", ""),
		ClaudeModel:     getenv("SPRUCE_CLAUDE_MODEL", "claude-haiku-4-5-20251001"),

		EbayClientID:     getenv("SPRUCE_EBAY_CLIENT_ID", ""),
		EbayClientSecret: getenv("SPRUCE_EBAY_CLIENT_SECRET", ""),
		EbayMarketplace:  getenv("SPRUCE_EBAY_MARKETPLACE", "EBAY_AU"),
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
