// Package mailer envía correos transaccionales (verificación, restablecimiento).
//
// SMTPSender habla SMTP real con TLS: puerto 465 usa TLS implícito; en los
// demás puertos exige STARTTLS (por defecto) para que ni las credenciales ni
// los enlaces viajen en claro. LogSender es solo para desarrollo/sin SMTP.
package mailer

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"

	"korzadi-wallet/backend/internal/domain"
)

type SMTPConfig struct {
	Host       string
	Port       int
	Username   string
	Password   string
	From       string // "Korzadi Wallet <no-reply@dominio.com>" o solo la dirección
	RequireTLS bool   // si el servidor no ofrece STARTTLS (y no es 465), falla
	Timeout    time.Duration
}

type SMTPSender struct {
	cfg SMTPConfig
}

func NewSMTPSender(cfg SMTPConfig) (*SMTPSender, error) {
	if strings.TrimSpace(cfg.Host) == "" {
		return nil, errors.New("SMTP_HOST vacío")
	}
	if cfg.Port == 0 {
		cfg.Port = 587
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if _, err := mail.ParseAddress(cfg.From); err != nil {
		return nil, fmt.Errorf("SMTP_FROM inválido: %w", err)
	}
	return &SMTPSender{cfg: cfg}, nil
}

// NewFromEnv construye el sender desde variables de entorno:
//
//	SMTP_HOST, SMTP_PORT (587), SMTP_USERNAME, SMTP_PASSWORD, SMTP_FROM,
//	SMTP_REQUIRE_TLS (true)
//
// Sin SMTP_HOST devuelve un LogSender y configured=false.
func NewFromEnv() (sender domain.EmailSender, configured bool, err error) {
	host := strings.TrimSpace(os.Getenv("SMTP_HOST"))
	if host == "" {
		devLinks := strings.EqualFold(os.Getenv("EMAIL_DEV_LOG_LINKS"), "true")
		return NewLogSender(devLinks), false, nil
	}
	port := 587
	if v := strings.TrimSpace(os.Getenv("SMTP_PORT")); v != "" {
		p, perr := strconv.Atoi(v)
		if perr != nil || p <= 0 || p > 65535 {
			return nil, false, fmt.Errorf("SMTP_PORT inválido: %q", v)
		}
		port = p
	}
	requireTLS := !strings.EqualFold(os.Getenv("SMTP_REQUIRE_TLS"), "false")
	s, err := NewSMTPSender(SMTPConfig{
		Host: host, Port: port,
		Username: os.Getenv("SMTP_USERNAME"), Password: os.Getenv("SMTP_PASSWORD"),
		From: os.Getenv("SMTP_FROM"), RequireTLS: requireTLS,
	})
	if err != nil {
		return nil, false, err
	}
	return s, true, nil
}

func (s *SMTPSender) Send(ctx context.Context, to, subject, body string) error {
	msg, fromAddr, toAddr, err := BuildMessage(s.cfg.From, to, subject, body, time.Now())
	if err != nil {
		return err
	}

	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	implicitTLS := s.cfg.Port == 465

	var conn net.Conn
	if implicitTLS {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp: conectar a %s: %w", addr, err)
	}
	_ = conn.SetDeadline(time.Now().Add(s.cfg.Timeout))

	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp: saludo: %w", err)
	}
	defer c.Close()

	if !implicitTLS {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
				return fmt.Errorf("smtp: STARTTLS: %w", err)
			}
		} else if s.cfg.RequireTLS {
			return errors.New("smtp: el servidor no ofrece STARTTLS y SMTP_REQUIRE_TLS=true")
		}
	}
	if s.cfg.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			return fmt.Errorf("smtp: autenticación: %w", err)
		}
	}
	if err := c.Mail(fromAddr); err != nil {
		return fmt.Errorf("smtp: MAIL FROM: %w", err)
	}
	if err := c.Rcpt(toAddr); err != nil {
		return fmt.Errorf("smtp: RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp: DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("smtp: escribir mensaje: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp: cerrar DATA: %w", err)
	}
	return c.Quit()
}

// BuildMessage arma el mensaje RFC 5322 en UTF-8 (cuerpo en base64). Rechaza
// saltos de línea en destinatario/asunto para impedir inyección de cabeceras.
func BuildMessage(from, to, subject, body string, now time.Time) (msg []byte, fromAddr, toAddr string, err error) {
	for name, v := range map[string]string{"from": from, "to": to, "subject": subject} {
		if strings.ContainsAny(v, "\r\n") {
			return nil, "", "", fmt.Errorf("mailer: salto de línea no permitido en %s", name)
		}
	}
	f, err := mail.ParseAddress(from)
	if err != nil {
		return nil, "", "", fmt.Errorf("mailer: remitente inválido: %w", err)
	}
	t, err := mail.ParseAddress(to)
	if err != nil {
		return nil, "", "", fmt.Errorf("mailer: destinatario inválido: %w", err)
	}

	var b strings.Builder
	b.WriteString("From: " + f.String() + "\r\n")
	b.WriteString("To: " + t.String() + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("UTF-8", subject) + "\r\n")
	b.WriteString("Date: " + now.UTC().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n")
	b.WriteString("\r\n")

	enc := base64.StdEncoding.EncodeToString([]byte(body))
	for len(enc) > 76 {
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc + "\r\n")
	return []byte(b.String()), f.Address, t.Address, nil
}

// LogSender no envía nada: sirve cuando no hay SMTP configurado. Por defecto
// NO registra el cuerpo (contiene enlaces de un solo uso que equivalen a
// credenciales); con devLinks=true sí, SOLO para desarrollo local.
type LogSender struct {
	devLinks bool
}

func NewLogSender(devLinks bool) *LogSender { return &LogSender{devLinks: devLinks} }

func (l *LogSender) Send(ctx context.Context, to, subject, body string) error {
	if l.devLinks {
		log.Printf("mailer[DEV]: correo NO enviado (SMTP sin configurar) to=%s asunto=%q\n%s", maskEmail(to), subject, body)
		return nil
	}
	log.Printf("mailer: correo NO enviado (SMTP sin configurar) to=%s asunto=%q", maskEmail(to), subject)
	return nil
}

func maskEmail(e string) string {
	at := strings.LastIndex(e, "@")
	if at <= 1 {
		return "***"
	}
	return e[:1] + "***" + e[at:]
}
