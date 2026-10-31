// Package clamav asks a ClamAV daemon (clamd) whether a file carries
// malware. It speaks clamd's own protocol over TCP: the file goes as a
// stream of length-prefixed chunks (INSTREAM), so clamd needs no access to
// the uploads directory.
package clamav

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

const (
	dialTimeout = 5 * time.Second
	// scanTimeout bounds one file; clamd reads a large archive for a while.
	scanTimeout = 2 * time.Minute
	chunkSize   = 64 << 10
)

// Client talks to one clamd.
type Client struct {
	addr string
}

// NewClient creates a Client for clamd at addr, host:port.
func NewClient(addr string) *Client {
	return &Client{addr: addr}
}

// Ping checks that clamd answers.
func (c *Client) Ping(ctx context.Context) error {
	reply, err := c.command(ctx, "PING", nil)
	if err != nil {
		return err
	}
	if reply != "PONG" {
		return fmt.Errorf("clamd: ping answered %q", reply)
	}
	return nil
}

// Scan sends r to clamd and returns the name of the threat it found, or ""
// for a clean file. An error means the file wasn't checked.
func (c *Client) Scan(ctx context.Context, r io.Reader) (threat string, err error) {
	reply, err := c.command(ctx, "INSTREAM", r)
	if err != nil {
		return "", err
	}
	// "stream: OK", "stream: Eicar-Test-Signature FOUND", "INSTREAM size
	// limit exceeded. ERROR".
	reply = strings.TrimPrefix(reply, "stream: ")
	switch {
	case reply == "OK":
		return "", nil
	case strings.HasSuffix(reply, " FOUND"):
		return strings.TrimSuffix(reply, " FOUND"), nil
	}
	return "", fmt.Errorf("clamd: %s", reply)
}

// command sends a z-command (NUL-terminated), the stream after it when
// there is one, and reads the one reply.
func (c *Client) command(ctx context.Context, name string, stream io.Reader) (string, error) {
	d := net.Dialer{Timeout: dialTimeout}
	conn, err := d.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return "", fmt.Errorf("clamd: %w", err)
	}
	defer conn.Close()
	deadline := time.Now().Add(scanTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return "", fmt.Errorf("clamd: %w", err)
	}

	w := bufio.NewWriterSize(conn, chunkSize+4)
	_, sendErr := w.WriteString("z" + name + "\x00")
	if sendErr == nil && stream != nil {
		sendErr = writeChunks(w, stream)
	}
	if sendErr == nil {
		sendErr = w.Flush()
	}

	// clamd answers and hangs up early when a stream is over its limit, so
	// a reply is read even after a failed send; but not after the file
	// failed to read, which leaves clamd waiting for the rest.
	var local *readError
	if errors.As(sendErr, &local) {
		return "", sendErr
	}
	reply, err := bufio.NewReader(conn).ReadBytes(0)
	if len(reply) == 0 {
		if sendErr != nil {
			return "", fmt.Errorf("clamd: send: %w", sendErr)
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("clamd: read reply: %w", err)
		}
	}
	return string(bytes.TrimRight(reply, "\x00\n")), nil
}

// writeChunks sends the stream as INSTREAM wants it: each chunk after its
// length as four big-endian bytes, and a zero length at the end.
func writeChunks(w io.Writer, r io.Reader) error {
	buf := make([]byte, chunkSize)
	var size [4]byte
	for {
		n, err := io.ReadFull(r, buf)
		if n > 0 && n <= chunkSize {
			binary.BigEndian.PutUint32(size[:], uint32(n))
			if _, werr := w.Write(size[:]); werr != nil {
				return fmt.Errorf("clamd: %w", werr)
			}
			if _, werr := w.Write(buf[:n]); werr != nil {
				return fmt.Errorf("clamd: %w", werr)
			}
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			break
		}
		if err != nil {
			return &readError{err}
		}
	}
	binary.BigEndian.PutUint32(size[:], 0)
	if _, err := w.Write(size[:]); err != nil {
		return fmt.Errorf("clamd: %w", err)
	}
	return nil
}

// readError is the file failing to read while it was being sent.
type readError struct{ err error }

func (e *readError) Error() string { return "clamd: read file: " + e.err.Error() }
func (e *readError) Unwrap() error { return e.err }
