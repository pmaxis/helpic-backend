package mail_test

import (
	"bufio"
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/textproto"
	"strings"
	"testing"

	helpicmail "github.com/pmaxis/helpic/internal/mail"
)

// fakeSMTP accepts one message and hands back the envelope and the data.
func fakeSMTP(t *testing.T) (port int, got chan [3]string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	got = make(chan [3]string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		tp := textproto.NewConn(conn)
		var from, to string
		_ = tp.PrintfLine("220 fake")
		for {
			line, err := tp.ReadLine()
			if err != nil {
				return
			}
			switch cmd := strings.ToUpper(strings.Fields(line)[0]); cmd {
			case "EHLO", "HELO":
				_ = tp.PrintfLine("250 fake")
			case "MAIL":
				from = line
				_ = tp.PrintfLine("250 OK")
			case "RCPT":
				to = line
				_ = tp.PrintfLine("250 OK")
			case "DATA":
				_ = tp.PrintfLine("354 go on")
				data, err := tp.ReadDotBytes()
				if err != nil {
					return
				}
				got <- [3]string{from, to, string(data)}
				_ = tp.PrintfLine("250 OK")
			case "QUIT":
				_ = tp.PrintfLine("221 bye")
				return
			default:
				_ = tp.PrintfLine("502 no")
			}
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, got
}

func TestSMTP_SendsBothParts(t *testing.T) {
	port, got := fakeSMTP(t)
	s, err := helpicmail.NewSMTP(helpicmail.SMTPConfig{Host: "127.0.0.1", Port: port, From: "Helpic <no-reply@helpic.test>"})
	if err != nil {
		t.Fatal(err)
	}
	m, err := helpicmail.Render(helpicmail.Email{
		To: "Олена <olena@example.com>", Subject: "Завтра дедлайн: “Logo”", Heading: "Hi",
		Paragraphs: []string{"A line that is long enough to be wrapped by quoted-printable encoding, with ünïcödé in it too."},
		Button:     &helpicmail.Button{Text: "Open", URL: "https://app.test/tasks/1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	env := <-got
	if env[0] != "MAIL FROM:<no-reply@helpic.test>" || !strings.HasPrefix(env[1], "RCPT TO:<olena@example.com>") {
		t.Fatalf("envelope = %q, %q", env[0], env[1])
	}

	msg, err := mail.ReadMessage(strings.NewReader(env[2]))
	if err != nil {
		t.Fatal(err)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil || subject != "Завтра дедлайн: “Logo”" {
		t.Fatalf("subject = %q, %v", subject, err)
	}
	_, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	parts := map[string]string{}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	for {
		p, err := mr.NextPart() // decodes quoted-printable
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(bufio.NewReader(p))
		parts[strings.Split(p.Header.Get("Content-Type"), ";")[0]] = string(body)
	}
	if !strings.Contains(parts["text/plain"], "ünïcödé in it too.") || !strings.Contains(parts["text/plain"], "Open: https://app.test/tasks/1") {
		t.Fatalf("text part = %q", parts["text/plain"])
	}
	if !strings.Contains(parts["text/html"], `href="https://app.test/tasks/1"`) {
		t.Fatalf("html part = %q", parts["text/html"])
	}
}

func TestSMTP_RefusesHeaderInjection(t *testing.T) {
	s, err := helpicmail.NewSMTP(helpicmail.SMTPConfig{Host: "127.0.0.1", Port: 1, From: "no-reply@helpic.test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []helpicmail.Message{
		{To: "a@example.com", Subject: "Hi\r\nBcc: victim@example.com"},
		{To: "a@example.com\r\nBcc: victim@example.com", Subject: "Hi"},
	} {
		if err := s.Send(context.Background(), m); err == nil || strings.Contains(err.Error(), "dial") {
			t.Fatalf("sent %q / %q: %v", m.To, m.Subject, err)
		}
	}
	if _, err := helpicmail.NewSMTP(helpicmail.SMTPConfig{From: "not an address"}); err == nil {
		t.Fatal("accepted a bad MAIL_FROM")
	}
}
