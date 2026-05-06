package email

import (
	"crypto/tls"
	"fmt"
	"net/smtp"
	"strings"

	"github.com/nrhtr/spruce/internal/config"
)

// Send sends an HTML email via SMTP.
// Port 465 uses implicit TLS (SMTPS); all other ports use STARTTLS via smtp.SendMail.
func Send(cfg *config.Config, to, subject, htmlBody string) error {
	if cfg.SMTPHost == "" {
		return fmt.Errorf("SPRUCE_SMTP_HOST is not configured")
	}

	addr := cfg.SMTPHost + ":" + cfg.SMTPPort
	msg := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n%s",
		cfg.EmailFrom, to, subject, htmlBody,
	)

	if cfg.SMTPPort == "465" {
		return sendImplicitTLS(cfg.SMTPHost, addr, cfg.SMTPUser, cfg.SMTPPass, cfg.EmailFrom, to, msg)
	}

	auth := smtp.PlainAuth("", cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPHost)
	return smtp.SendMail(addr, auth, cfg.EmailFrom, []string{to}, []byte(msg))
}

// sendImplicitTLS connects with TLS from the start (port 465 / SMTPS).
func sendImplicitTLS(host, addr, user, pass, from, to, msg string) error {
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: host})
	if err != nil {
		return fmt.Errorf("tls dial: %w", err)
	}

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}
	defer client.Close()

	if err := client.Auth(smtp.PlainAuth("", user, pass, host)); err != nil {
		return fmt.Errorf("smtp auth: %w", err)
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("smtp mail: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("smtp rcpt: %w", err)
	}
	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := strings.NewReader(msg).WriteTo(wc); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("smtp close data: %w", err)
	}
	return client.Quit()
}
