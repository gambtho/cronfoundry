package publish

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"mime"
	"mime/multipart"
	"net"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"

	"github.com/gambtho/cronfoundry/internal/config"
	"github.com/gambtho/cronfoundry/internal/template"
)

type emailPub struct{}

// NewEmailPublisher returns a Publisher that sends email via SMTP.
func NewEmailPublisher() Publisher { return &emailPub{} }

func (p *emailPub) Type() string { return "email" }

func (p *emailPub) Publish(ctx context.Context, dest config.Destination, output string, tctx template.Context, secrets SecretGetter) Result {
	d := dest.Email
	if d == nil {
		return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("email: destination config is nil")}
	}

	username, err := secrets.Get(d.UsernameSecret)
	if err != nil {
		return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("email: resolve username secret: %w", err)}
	}

	password, err := secrets.Get(d.PasswordSecret)
	if err != nil {
		return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("email: resolve password secret: %w", err)}
	}

	subj := d.Subject
	if subj == "" {
		subj = "{{ skill.name }}: run {{ run.date }}"
	}
	renderedSubj, warns := template.Render(subj, tctx)
	var detail string
	if len(warns) > 0 {
		detail = "template warnings: " + strings.Join(warns, ", ")
	}

	format := d.EffectiveFormat()
	port := d.EffectiveSMTPPort()

	body, err := buildEmail(d.From, d.To, renderedSubj, output, format, tctx)
	if err != nil {
		return Result{Type: p.Type(), OK: false, Err: err}
	}
	addr := net.JoinHostPort(d.SMTPHost, strconv.Itoa(port))
	auth := smtp.PlainAuth("", username, password, d.SMTPHost)

	// net/smtp does not support context cancellation; the run deadline covers this via the process.
	if err := smtp.SendMail(addr, auth, d.From, d.To, body); err != nil {
		return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("email: send: %w", err)}
	}

	return Result{Type: p.Type(), OK: true, Detail: detail}
}

func buildEmail(from string, to []string, subject, output, format string, tctx template.Context) ([]byte, error) {
	var buf bytes.Buffer

	// Write common headers
	fmt.Fprintf(&buf, "From: %s\r\n", from)
	fmt.Fprintf(&buf, "To: %s\r\n", strings.Join(to, ", "))
	fmt.Fprintf(&buf, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", subject))
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")

	if format == "html" {
		var bodyBuf bytes.Buffer
		mw := multipart.NewWriter(&bodyBuf)
		boundary := mw.Boundary()

		fmt.Fprintf(&buf, "Content-Type: multipart/alternative; boundary=\"%s\"\r\n", boundary)
		fmt.Fprintf(&buf, "\r\n")

		// text/plain part
		th := make(textproto.MIMEHeader)
		th.Set("Content-Type", "text/plain; charset=utf-8")
		th.Set("Content-Transfer-Encoding", "8bit")
		pw, err := mw.CreatePart(th)
		if err != nil {
			return nil, fmt.Errorf("email: build plain part: %w", err)
		}
		if _, err := pw.Write([]byte(output)); err != nil {
			return nil, fmt.Errorf("email: write plain part: %w", err)
		}

		// text/html part
		hh := make(textproto.MIMEHeader)
		hh.Set("Content-Type", "text/html; charset=utf-8")
		hh.Set("Content-Transfer-Encoding", "8bit")
		hw, err := mw.CreatePart(hh)
		if err != nil {
			return nil, fmt.Errorf("email: build html part: %w", err)
		}
		if _, err := fmt.Fprintf(hw, "<html><body style=\"font-family:sans-serif;\"><h2>%s — %s</h2><pre style=\"white-space:pre-wrap;\">%s</pre></body></html>",
			html.EscapeString(tctx.Skill.Name),
			html.EscapeString(tctx.RunDate),
			html.EscapeString(output)); err != nil {
			return nil, fmt.Errorf("email: write html part: %w", err)
		}

		if err := mw.Close(); err != nil {
			return nil, fmt.Errorf("email: close multipart writer: %w", err)
		}
		buf.Write(bodyBuf.Bytes())
	} else {
		fmt.Fprintf(&buf, "Content-Type: text/plain; charset=utf-8\r\n")
		fmt.Fprintf(&buf, "Content-Transfer-Encoding: 8bit\r\n")
		fmt.Fprintf(&buf, "\r\n")
		_, _ = buf.WriteString(output)
	}

	return buf.Bytes(), nil
}
