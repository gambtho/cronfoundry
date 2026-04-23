package publish

import (
	"bufio"
	"context"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/gambtho/cronfoundry/internal/config"
	"github.com/gambtho/cronfoundry/internal/template"
)

// fakeSMTP starts a minimal SMTP server on a random port.
// It returns the listener address and a channel that receives the DATA body.
func fakeSMTP(t *testing.T) (string, <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("fakeSMTP listen: %v", err)
	}
	ch := make(chan string, 1)
	go func() {
		defer func() { _ = ln.Close() }()
		defer close(ch) // unblock receivers if no DATA command is reached
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		r := bufio.NewReader(conn)
		_, _ = conn.Write([]byte("220 fake ESMTP\r\n"))
		var dataBody strings.Builder
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				break
			}
			upper := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(upper, "EHLO"):
				_, _ = conn.Write([]byte("250-fake\r\n250 AUTH PLAIN\r\n"))
			case strings.HasPrefix(upper, "AUTH PLAIN"):
				_, _ = conn.Write([]byte("235 ok\r\n"))
			case strings.HasPrefix(upper, "MAIL FROM"):
				_, _ = conn.Write([]byte("250 ok\r\n"))
			case strings.HasPrefix(upper, "RCPT TO"):
				_, _ = conn.Write([]byte("250 ok\r\n"))
			case upper == "DATA":
				_, _ = conn.Write([]byte("354 go\r\n"))
				// read until \r\n.\r\n
				for {
					dline, err := r.ReadString('\n')
					if err != nil {
						break
					}
					if dline == ".\r\n" {
						break
					}
					dataBody.WriteString(dline)
				}
				_, _ = conn.Write([]byte("250 ok\r\n"))
				ch <- dataBody.String()
			case upper == "QUIT":
				_, _ = conn.Write([]byte("221 bye\r\n"))
				return
			}
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String(), ch
}

// fakeSMTPAuthFail is like fakeSMTP but responds 535 to AUTH PLAIN.
func fakeSMTPAuthFail(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("fakeSMTPAuthFail listen: %v", err)
	}
	go func() {
		defer func() { _ = ln.Close() }()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		r := bufio.NewReader(conn)
		_, _ = conn.Write([]byte("220 fake ESMTP\r\n"))
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				break
			}
			upper := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(upper, "EHLO"):
				_, _ = conn.Write([]byte("250-fake\r\n250 AUTH PLAIN\r\n"))
			case strings.HasPrefix(upper, "AUTH PLAIN"):
				_, _ = conn.Write([]byte("535 auth failed\r\n"))
				_ = conn.Close()
				return
			case upper == "QUIT":
				_, _ = conn.Write([]byte("221 bye\r\n"))
				return
			}
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String()
}

func destFromAddr(t *testing.T, addr, format, subject string) config.Destination {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("destFromAddr: split host/port %q: %v", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("destFromAddr: parse port %q: %v", portStr, err)
	}
	return config.Destination{
		Email: &config.EmailDest{
			SMTPHost:       host,
			SMTPPort:       port,
			UsernameSecret: "user_secret",
			PasswordSecret: "pass_secret",
			From:           "from@example.com",
			To:             []string{"to@example.com"},
			Subject:        subject,
			Format:         format,
		},
	}
}

func TestEmailPublisher_PlainText(t *testing.T) {
	addr, dataCh := fakeSMTP(t)
	pub := NewEmailPublisher()
	dest := destFromAddr(t, addr, "text", "Test Subject")
	tctx := template.Context{Skill: template.Meta{Name: "MySkill"}, RunDate: "2026-04-23"}
	secrets := mapSecrets{"user_secret": "user@example.com", "pass_secret": "secret"}
	res := pub.Publish(context.Background(), dest, "hello world", tctx, secrets)
	if !res.OK {
		t.Fatalf("expected OK, got err: %v", res.Err)
	}
	data := <-dataCh
	if !strings.Contains(data, "Content-Type: text/plain") {
		t.Errorf("expected text/plain in data, got:\n%s", data)
	}
}

func TestEmailPublisher_HTMLFormat(t *testing.T) {
	addr, dataCh := fakeSMTP(t)
	pub := NewEmailPublisher()
	dest := destFromAddr(t, addr, "html", "Test Subject")
	tctx := template.Context{Skill: template.Meta{Name: "MySkill"}, RunDate: "2026-04-23"}
	secrets := mapSecrets{"user_secret": "user@example.com", "pass_secret": "secret"}
	res := pub.Publish(context.Background(), dest, "hello world", tctx, secrets)
	if !res.OK {
		t.Fatalf("expected OK, got err: %v", res.Err)
	}
	data := <-dataCh
	if !strings.Contains(data, "Content-Type: multipart/alternative") {
		t.Errorf("expected multipart/alternative in data, got:\n%s", data)
	}
	if !strings.Contains(data, "text/html") {
		t.Errorf("expected text/html in data, got:\n%s", data)
	}
	if !strings.Contains(data, "<h2>MySkill</h2>") {
		t.Errorf("expected <h2>MySkill</h2> in HTML part, got:\n%s", data)
	}
}

func TestEmailPublisher_SubjectTemplate(t *testing.T) {
	addr, dataCh := fakeSMTP(t)
	pub := NewEmailPublisher()
	dest := destFromAddr(t, addr, "text", "{{ skill.name }} digest")
	tctx := template.Context{Skill: template.Meta{Name: "MySkill"}, RunDate: "2026-04-23"}
	secrets := mapSecrets{"user_secret": "user@example.com", "pass_secret": "secret"}
	res := pub.Publish(context.Background(), dest, "hello", tctx, secrets)
	if !res.OK {
		t.Fatalf("expected OK, got err: %v", res.Err)
	}
	data := <-dataCh
	if !strings.Contains(data, "Subject: MySkill digest") {
		t.Errorf("expected 'Subject: MySkill digest' in data, got:\n%s", data)
	}
}

func TestEmailPublisher_AuthFailure(t *testing.T) {
	addr := fakeSMTPAuthFail(t)
	pub := NewEmailPublisher()
	dest := destFromAddr(t, addr, "text", "Test")
	tctx := template.Context{Skill: template.Meta{Name: "MySkill"}, RunDate: "2026-04-23"}
	secrets := mapSecrets{"user_secret": "user@example.com", "pass_secret": "secret"}
	res := pub.Publish(context.Background(), dest, "hello", tctx, secrets)
	if res.OK {
		t.Fatal("expected OK==false for auth failure")
	}
	if res.Err == nil {
		t.Fatal("expected non-nil Err for auth failure")
	}
}

func TestEmailPublisher_SubjectNonASCII(t *testing.T) {
	addr, dataCh := fakeSMTP(t)
	pub := NewEmailPublisher()
	dest := destFromAddr(t, addr, "text", "Alert — système")
	tctx := template.Context{Skill: template.Meta{Name: "MySkill"}, RunDate: "2026-04-23"}
	secrets := mapSecrets{"user_secret": "user@example.com", "pass_secret": "secret"}
	res := pub.Publish(context.Background(), dest, "body", tctx, secrets)
	if !res.OK {
		t.Fatalf("expected OK, got err: %v", res.Err)
	}
	data := <-dataCh
	if !strings.Contains(data, "=?utf-8?") {
		t.Errorf("expected Q-encoded subject for non-ASCII, got:\n%s", data)
	}
}

func TestEmailPublisher_DefaultSubject(t *testing.T) {
	addr, dataCh := fakeSMTP(t)
	pub := NewEmailPublisher()
	// Subject="" triggers the default "{{ skill.name }}: run {{ run.date }}" template
	dest := destFromAddr(t, addr, "text", "")
	tctx := template.Context{Skill: template.Meta{Name: "MySkill"}, RunDate: "2026-04-23"}
	secrets := mapSecrets{"user_secret": "user@example.com", "pass_secret": "secret"}
	res := pub.Publish(context.Background(), dest, "body", tctx, secrets)
	if !res.OK {
		t.Fatalf("expected OK, got err: %v", res.Err)
	}
	data := <-dataCh
	if !strings.Contains(data, "MySkill") {
		t.Errorf("expected skill name in default subject, got:\n%s", data)
	}
	if !strings.Contains(data, "2026-04-23") {
		t.Errorf("expected run date in default subject, got:\n%s", data)
	}
}

func TestEmailPublisher_DefaultFormat(t *testing.T) {
	addr, dataCh := fakeSMTP(t)
	pub := NewEmailPublisher()
	// Format="" should default to html → multipart/alternative
	dest := destFromAddr(t, addr, "", "Test")
	tctx := template.Context{Skill: template.Meta{Name: "MySkill"}, RunDate: "2026-04-23"}
	secrets := mapSecrets{"user_secret": "user@example.com", "pass_secret": "secret"}
	res := pub.Publish(context.Background(), dest, "body", tctx, secrets)
	if !res.OK {
		t.Fatalf("expected OK, got err: %v", res.Err)
	}
	data := <-dataCh
	if !strings.Contains(data, "multipart/alternative") {
		t.Errorf("expected multipart/alternative for default format, got:\n%s", data)
	}
}
