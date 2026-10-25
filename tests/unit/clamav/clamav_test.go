package clamav_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/pmaxis/helpic/internal/clamav"
)

var eicar = []byte(`X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`)

// clamd answers one connection with reply, keeping the stream it was sent.
func clamd(t *testing.T, reply string, got *bytes.Buffer) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		cmd, _ := r.ReadString(0)
		if cmd == "zINSTREAM\x00" {
			for {
				var size uint32
				if binary.Read(r, binary.BigEndian, &size) != nil || size == 0 {
					break
				}
				io.CopyN(got, r, int64(size)) //nolint:errcheck
			}
		}
		conn.Write([]byte(reply + "\x00")) //nolint:errcheck
	}()
	return ln.Addr().String()
}

func TestScan(t *testing.T) {
	cases := []struct {
		name, reply, threat string
		fails               bool
	}{
		{"clean", "stream: OK", "", false},
		{"infected", "stream: Win.Test.EICAR_HDB-1 FOUND", "Win.Test.EICAR_HDB-1", false},
		{"too large", "INSTREAM size limit exceeded. ERROR", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got bytes.Buffer
			// Bigger than one chunk, to send several.
			file := bytes.Repeat([]byte("helpic "), 30000)
			threat, err := clamav.NewClient(clamd(t, c.reply, &got)).Scan(context.Background(), bytes.NewReader(file))
			if (err != nil) != c.fails {
				t.Fatalf("err = %v, want failure %v", err, c.fails)
			}
			if threat != c.threat {
				t.Fatalf("threat = %q, want %q", threat, c.threat)
			}
			if !bytes.Equal(got.Bytes(), file) {
				t.Fatalf("clamd got %d bytes, want %d", got.Len(), len(file))
			}
		})
	}
}

func TestPing(t *testing.T) {
	if err := clamav.NewClient(clamd(t, "PONG", &bytes.Buffer{})).Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := clamav.NewClient(clamd(t, "UNKNOWN COMMAND", &bytes.Buffer{})).Ping(context.Background()); err == nil {
		t.Fatal("want an error for a wrong answer")
	}
}

func TestScan_Unreachable(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	if _, err := clamav.NewClient(addr).Scan(context.Background(), strings.NewReader("x")); err == nil {
		t.Fatal("want an error when clamd is down")
	}
}

// TestRealClamd runs against a real clamd when CLAMAV_TEST_ADDRESS names one,
// e.g. docker run -p 3310:3310 clamav/clamav-debian:1.4.
func TestRealClamd(t *testing.T) {
	addr := os.Getenv("CLAMAV_TEST_ADDRESS")
	if addr == "" {
		t.Skip("CLAMAV_TEST_ADDRESS is not set")
	}
	c := clamav.NewClient(addr)
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	if threat, err := c.Scan(context.Background(), bytes.NewReader(eicar)); err != nil || threat == "" {
		t.Fatalf("eicar: threat %q, err %v", threat, err)
	}
	if threat, err := c.Scan(context.Background(), bytes.NewReader(bytes.Repeat([]byte("clean "), 50000))); err != nil || threat != "" {
		t.Fatalf("clean: threat %q, err %v", threat, err)
	}
}
