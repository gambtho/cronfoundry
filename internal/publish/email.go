package publish

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"mime"
	"mime/multipart"
	"net/smtp"
	"net/textproto"
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
		return Result{Type: "email", OK: false, Err: fmt.Errorf("email: destination config is nil")}
	}

	username, err := secrets.Get(d.UsernameSecret)
	if err != nil {
		return Result{Type: "email", OK: false, Err: fmt.Errorf("email: resolve username secret: %w", err)}
	}

	password, err := secrets.Get(d.PasswordSecret)
	if err != nil {
		return Result{Type: "email", OK: false, Err: fmt.Errorf("email: resolve password secret: %w", err)}
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

	format := d.Format
	if format == "" {
		format = "html"
	}

	port := d.SMTPPort
	if port == 0 {
		port = 587
	}

	body := buildEmail(d.From, d.To, renderedSubj, output, format, tctx)
	addr := fmt.Sprintf("%s:%d", d.SMTPHost, port)
	auth := smtp.PlainAuth("", username, password, d.SMTPHost)

	if err := smtp.SendMail(addr, auth, d.From, d.To, body); err != nil {
		return Result{Type: "email", OK: false, Err: err}
	}

	return Result{Type: "email", OK: true, Detail: detail}
}

func buildEmail(from string, to []string, subject, output, format string, tctx template.Context) []byte {
	var buf bytes.Buffer
	var bodyBuf bytes.Buffer

	mw := multipart.NewWriter(&bodyBuf)
	boundary := mw.Boundary()

	// Write headers
	fmt.Fprintf(&buf, "From: %s\r\n", from)
	fmt.Fprintf(&buf, "To: %s\r\n", strings.Join(to, ", "))
	fmt.Fprintf(&buf, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", subject))
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&buf, "Content-Type: multipart/alternative; boundary=%q\r\n", boundary)
	fmt.Fprintf(&buf, "\r\n")

	// text/plain part (always)
	th := make(textproto.MIMEHeader)
	th.Set("Content-Type", "text/plain; charset=utf-8")
	pw, _ := mw.CreatePart(th)
	pw.Write([]byte(output))

	// text/html part (only when format == "html")
	if format == "html" {
		hh := make(textproto.MIMEHeader)
		hh.Set("Content-Type", "text/html; charset=utf-8")
		hw, _ := mw.CreatePart(hh)
		fmt.Fprintf(hw, "<html><body style=\"font-family:sans-serif;\"><h2>%s</h2><p>%s</p><pre style=\"white-space:pre-wrap;\">%s</pre></body></html>",
			html.EscapeString(tctx.Skill.Name),
			html.EscapeString(tctx.RunDate),
			html.EscapeString(output))
	}

	mw.Close()

	buf.Write(bodyBuf.Bytes())
	return buf.Bytes()
}
