package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// Sender delivers a rendered message.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// SMTPConfig is where and as whom to send.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	// From is the sender, e.g. "Helpic <no-reply@helpic.app>".
	From string
}

// smtpTimeout bounds one delivery, from dialing to QUIT.
const smtpTimeout = 30 * time.Second

// SMTP sends through a mail server: implicit TLS on port 465, STARTTLS
// wherever the server offers it, and a login when a username is set.
type SMTP struct {
	cfg  SMTPConfig
	from *mail.Address
}

// NewSMTP checks the sender address and returns an SMTP Sender.
func NewSMTP(cfg SMTPConfig) (*SMTP, error) {
	from, err := mail.ParseAddress(cfg.From)
	if err != nil {
		return nil, fmt.Errorf("invalid MAIL_FROM: %w", err)
	}
	return &SMTP{cfg: cfg, from: from}, nil
}

// Send delivers m.
func (s *SMTP) Send(ctx context.Context, m Message) error {
	to, err := mail.ParseAddress(m.To)
	if err != nil {
		return fmt.Errorf("invalid recipient: %w", err)
	}
	body, err := s.compose(to, m)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, smtpTimeout)
	defer cancel()
	c, err := s.dial(ctx)
	if err != nil {
		return err
	}
	defer c.Close()

	if err := c.Mail(s.from.Address); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	if err := c.Rcpt(to.Address); err != nil {
		return fmt.Errorf("smtp rcpt to: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp end data: %w", err)
	}
	return c.Quit()
}

// Check signs in to the mail server and leaves without sending anything, to
// tell whether email can go out.
func (s *SMTP) Check(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, smtpTimeout)
	defer cancel()
	c, err := s.dial(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.Quit()
}

// dial connects, says hello, switches to TLS and signs in. The connection
// gives up at ctx's deadline.
func (s *SMTP) dial(ctx context.Context) (*smtp.Client, error) {
	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial smtp: %w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if s.cfg.Port == 465 {
		conn = tls.Client(conn, &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12})
	}
	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("smtp hello: %w", err)
	}
	if ok, _ := c.Extension("STARTTLS"); ok && s.cfg.Port != 465 {
		if err := c.StartTLS(&tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			_ = c.Close()
			return nil, fmt.Errorf("smtp starttls: %w", err)
		}
	}
	if s.cfg.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			_ = c.Close()
			return nil, fmt.Errorf("smtp auth: %w", err)
		}
	}
	return c, nil
}

// compose builds the MIME message: plain text and HTML as alternatives.
func (s *SMTP) compose(to *mail.Address, m Message) ([]byte, error) {
	if strings.ContainsAny(m.Subject, "\r\n") {
		return nil, errors.New("subject has a line break")
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	header := []string{
		"From: " + s.from.String(),
		"To: " + to.String(),
		"Subject: " + mime.QEncoding.Encode("utf-8", m.Subject),
		"Date: " + time.Now().Format(time.RFC1123Z),
		"Message-ID: " + messageID(s.from.Address),
		"MIME-Version: 1.0",
		`Content-Type: multipart/alternative; boundary="` + mw.Boundary() + `"`,
	}
	head := strings.Join(header, "\r\n") + "\r\n\r\n"

	for _, part := range []struct{ kind, body string }{{"text/plain", m.Text}, {"text/html", m.HTML}} {
		pw, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {part.kind + "; charset=UTF-8"},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return nil, err
		}
		qp := quotedprintable.NewWriter(pw)
		if _, err := qp.Write([]byte(part.body)); err != nil {
			return nil, err
		}
		if err := qp.Close(); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	return append([]byte(head), buf.Bytes()...), nil
}

func messageID(from string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	domain := "helpic.local"
	if at := strings.LastIndex(from, "@"); at >= 0 {
		domain = from[at+1:]
	}
	return "<" + hex.EncodeToString(b) + "@" + domain + ">"
}

// Log is the Sender without a mail server: it logs who would have got what,
// never the body (it can hold one-time links).
type Log struct{}

// Send logs the message instead of sending it.
func (Log) Send(_ context.Context, m Message) error {
	slog.Info("email not sent: no SMTP_HOST", "to", m.To, "subject", m.Subject)
	return nil
}
