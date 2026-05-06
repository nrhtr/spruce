package email

import (
	"fmt"
	"net/smtp"

	"github.com/nrhtr/spruce/internal/config"
)

// Send sends an HTML email via SMTP. Returns an error if SMTP is not configured.
func Send(cfg *config.Config, to, subject, htmlBody string) error {
	if cfg.SMTPHost == "" {
		return fmt.Errorf("SPRUCE_SMTP_HOST is not configured")
	}

	addr := cfg.SMTPHost + ":" + cfg.SMTPPort
	auth := smtp.PlainAuth("", cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPHost)

	msg := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n%s",
		cfg.EmailFrom, to, subject, htmlBody,
	)

	return smtp.SendMail(addr, auth, cfg.EmailFrom, []string{to}, []byte(msg))
}
