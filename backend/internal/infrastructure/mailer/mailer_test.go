package mailer

import (
	"bufio"
	"context"
	"encoding/base64"
	"net"
	"strings"
	"testing"
	"time"
)

func TestBuildMessage_HeadersAndBase64Body(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	msg, from, to, err := BuildMessage("Korzadi Wallet <no-reply@korzadi.com>", "ana@example.com",
		"Verifica tu correo — ñandú", "Hola Ana,\nenlace: https://x/verify-email?token=abc", now)
	if err != nil {
		t.Fatalf("BuildMessage: %v", err)
	}
	if from != "no-reply@korzadi.com" || to != "ana@example.com" {
		t.Fatalf("direcciones de sobre = %q / %q", from, to)
	}
	s := string(msg)
	for _, want := range []string{
		"MIME-Version: 1.0\r\n", "Content-Type: text/plain; charset=UTF-8\r\n",
		"Content-Transfer-Encoding: base64\r\n", "To: <ana@example.com>\r\n", "Subject: =?UTF-8?",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("falta %q en el mensaje:\n%s", want, s)
		}
	}
	// El cuerpo decodifica al texto original.
	parts := strings.SplitN(s, "\r\n\r\n", 2)
	if len(parts) != 2 {
		t.Fatal("mensaje sin separador cabecera/cuerpo")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(parts[1], "\r\n", ""))
	if err != nil || !strings.Contains(string(raw), "verify-email?token=abc") {
		t.Fatalf("cuerpo mal codificado: %v %q", err, raw)
	}
}

func TestBuildMessage_RejectsHeaderInjection(t *testing.T) {
	now := time.Now()
	cases := map[string][3]string{
		"to con CRLF":      {"a@b.com", "x@y.com\r\nBcc: evil@z.com", "hola"},
		"subject con CRLF": {"a@b.com", "x@y.com", "hola\r\nBcc: evil@z.com"},
		"from con CRLF":    {"a@b.com\nBcc: e@z.com", "x@y.com", "hola"},
		"to inválido":      {"a@b.com", "no-es-un-correo", "hola"},
	}
	for name, c := range cases {
		if _, _, _, err := BuildMessage(c[0], c[1], c[2], "cuerpo", now); err == nil {
			t.Errorf("%s: debía rechazarse", name)
		}
	}
}

func TestNewSMTPSender_Validation(t *testing.T) {
	if _, err := NewSMTPSender(SMTPConfig{From: "a@b.com"}); err == nil {
		t.Error("host vacío debía fallar")
	}
	if _, err := NewSMTPSender(SMTPConfig{Host: "smtp.x.com", From: "no-es-correo"}); err == nil {
		t.Error("From inválido debía fallar")
	}
}

func TestLogSender_DoesNotFailAndMasksEmail(t *testing.T) {
	if err := NewLogSender(false).Send(context.Background(), "ana@example.com", "asunto", "cuerpo con enlace secreto"); err != nil {
		t.Fatalf("LogSender: %v", err)
	}
	if got := maskEmail("ana@example.com"); got != "a***@example.com" {
		t.Fatalf("maskEmail = %q", got)
	}
}

// fakeSMTP es un servidor SMTP mínimo (sin TLS) para probar el flujo completo.
func fakeSMTP(t *testing.T) (addr string, got chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	got = make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		w := func(s string) { conn.Write([]byte(s + "\r\n")) }
		w("220 fake ESMTP")
		var data strings.Builder
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if inData {
				if line == "." {
					inData = false
					w("250 OK queued")
					got <- data.String()
					continue
				}
				data.WriteString(line + "\n")
				continue
			}
			switch {
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
				w("250 fake")
			case strings.HasPrefix(line, "MAIL FROM"):
				w("250 OK")
			case strings.HasPrefix(line, "RCPT TO"):
				w("250 OK")
			case line == "DATA":
				w("354 go ahead")
				inData = true
			case line == "QUIT":
				w("221 bye")
				return
			default:
				w("250 OK")
			}
		}
	}()
	return ln.Addr().String(), got
}

func TestSMTPSender_SendsMessage_WhenTLSNotRequired(t *testing.T) {
	addr, got := fakeSMTP(t)
	host, portStr, _ := net.SplitHostPort(addr)
	port := 0
	for _, ch := range portStr {
		port = port*10 + int(ch-'0')
	}
	s, err := NewSMTPSender(SMTPConfig{Host: host, Port: port, From: "no-reply@korzadi.com", RequireTLS: false, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("NewSMTPSender: %v", err)
	}
	if err := s.Send(context.Background(), "ana@example.com", "Hola", "cuerpo de prueba"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case data := <-got:
		if !strings.Contains(data, "To: <ana@example.com>") {
			t.Fatalf("mensaje recibido inesperado:\n%s", data)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("el servidor no recibió el mensaje")
	}
}

// Con RequireTLS=true y un servidor sin STARTTLS, NO se envía nada en claro.
func TestSMTPSender_RefusesPlaintext_WhenTLSRequired(t *testing.T) {
	addr, _ := fakeSMTP(t)
	host, portStr, _ := net.SplitHostPort(addr)
	port := 0
	for _, ch := range portStr {
		port = port*10 + int(ch-'0')
	}
	s, _ := NewSMTPSender(SMTPConfig{Host: host, Port: port, From: "no-reply@korzadi.com", RequireTLS: true, Timeout: 5 * time.Second})
	if err := s.Send(context.Background(), "ana@example.com", "Hola", "cuerpo"); err == nil {
		t.Fatal("con RequireTLS=true y sin STARTTLS debía fallar")
	}
}
