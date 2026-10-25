package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pmaxis/helpic/internal/config"
	"github.com/pmaxis/helpic/internal/server"
)

// eicar is the standard antivirus test file: harmless, and every scanner
// reports it.
var eicar = []byte(`X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`)

// fakeClamd speaks enough of clamd's protocol for Helpic: PING, and
// INSTREAM finding eicar. While broken, it answers every scan with an error.
type fakeClamd struct {
	addr   string
	broken atomic.Bool
	scans  atomic.Int32
}

func newFakeClamd(t *testing.T) *fakeClamd {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	f := &fakeClamd{addr: ln.Addr().String()}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(conn)
		}
	}()
	return f
}

func (f *fakeClamd) serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	cmd, err := r.ReadString(0)
	if err != nil {
		return
	}
	switch cmd {
	case "zPING\x00":
		conn.Write([]byte("PONG\x00")) //nolint:errcheck
	case "zINSTREAM\x00":
		var file bytes.Buffer
		for {
			var size uint32
			if err := binary.Read(r, binary.BigEndian, &size); err != nil {
				return
			}
			if size == 0 {
				break
			}
			if _, err := io.CopyN(&file, r, int64(size)); err != nil {
				return
			}
		}
		f.scans.Add(1)
		switch {
		case f.broken.Load():
			conn.Write([]byte("Can't allocate memory ERROR\x00")) //nolint:errcheck
		case bytes.Contains(file.Bytes(), eicar):
			conn.Write([]byte("stream: Eicar-Test-Signature FOUND\x00")) //nolint:errcheck
		default:
			conn.Write([]byte("stream: OK\x00")) //nolint:errcheck
		}
	}
}

// scanOf is the file's scan state as the task's list gives it.
func scanOf(t *testing.T, srv string, c *http.Client, taskID, id string) map[string]any {
	t.Helper()
	resp := get(t, c, srv+"/api/v1/attachments?task="+taskID)
	wantStatus(t, resp, http.StatusOK)
	for _, a := range decode(t, resp)["attachments"].([]any) {
		if a := a.(map[string]any); a["id"] == id {
			return a
		}
	}
	t.Fatalf("file %s not on the task", id)
	return nil
}

// waitScan waits for the file's scan to be want, and returns the file.
func waitScan(t *testing.T, srv string, c *http.Client, taskID, id, want string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		a := scanOf(t, srv, c, taskID, id)
		if a["scan"] == want {
			return a
		}
		if time.Now().After(deadline) {
			t.Fatalf("scan = %v, want %s", a["scan"], want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func uploaded(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	wantStatus(t, resp, http.StatusCreated)
	return field(decode(t, resp), "attachment")
}

func TestVirusScan_FileDownloadsOnceChecked(t *testing.T) {
	clamd := newFakeClamd(t)
	var uploads string
	srv, _, _ := newAPIWith(t, func(cfg *config.Config) { cfg.ClamAVAddress = clamd.addr; uploads = cfg.UploadsDir })
	freelancer := signUpFreelancer(t, srv, "scan@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "scanclient@example.com"))
	taskID := newTask(t, srv, freelancer, map[string]any{"client_id": myID(t, srv, client), "title": "Brief"})["id"].(string)

	// A clean file waits for its check, then downloads.
	clean := uploaded(t, upload(t, srv, client, taskID, "brief.txt", []byte("the brief")))
	if clean["scan"] != "pending" {
		t.Fatalf("a new upload's scan = %v, want pending", clean["scan"])
	}
	waitScan(t, srv.URL, freelancer, taskID, clean["id"].(string), "clean")
	resp := get(t, freelancer, srv.URL+"/api/v1/attachments/"+clean["id"].(string))
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Malware never downloads, and its bytes are gone; the row says why.
	bad := uploaded(t, upload(t, srv, client, taskID, "invoice.pdf", eicar))
	if a := waitScan(t, srv.URL, freelancer, taskID, bad["id"].(string), "infected"); a["threat"] != "Eicar-Test-Signature" {
		t.Fatalf("infected file = %v", a)
	}
	resp = get(t, freelancer, srv.URL+"/api/v1/attachments/"+bad["id"].(string))
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()
	if _, err := os.Stat(filepath.Join(uploads, bad["id"].(string))); !os.IsNotExist(err) {
		t.Fatalf("the infected file is still on disk: %v", err)
	}
	// Its uploader can still clear it off the task.
	resp = do(t, client, http.MethodDelete, srv.URL+"/api/v1/attachments/"+bad["id"].(string), nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
}

func TestVirusScan_PendingWhileTheScannerFails(t *testing.T) {
	clamd := newFakeClamd(t)
	clamd.broken.Store(true)
	srv, _, app := newAPIWith(t, func(cfg *config.Config) { cfg.ClamAVAddress = clamd.addr })
	freelancer := signUpFreelancer(t, srv, "down@example.com")
	taskID := newTask(t, srv, freelancer, map[string]any{"title": "Notes"})["id"].(string)

	f := uploaded(t, upload(t, srv, freelancer, taskID, "notes.txt", []byte("notes")))
	id := f["id"].(string)
	deadline := time.Now().Add(5 * time.Second)
	for clamd.scans.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if a := scanOf(t, srv.URL, freelancer, taskID, id); a["scan"] != "pending" {
		t.Fatalf("scan with the scanner failing = %v, want pending", a["scan"])
	}
	resp := get(t, freelancer, srv.URL+"/api/v1/attachments/"+id)
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()

	// The background job checks it once the scanner works again.
	clamd.broken.Store(false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go app.Run(ctx)
	waitScan(t, srv.URL, freelancer, taskID, id, "clean")
}

// Files stored before a scanner was set up download as before, and are
// checked once there is one.
func TestVirusScan_FilesFromBeforeTheScanner(t *testing.T) {
	var uploads string
	srv, _, _ := newAPIWith(t, func(cfg *config.Config) { uploads = cfg.UploadsDir })
	freelancer := signUpFreelancer(t, srv, "before@example.com")
	taskID := newTask(t, srv, freelancer, map[string]any{"title": "Old files"})["id"].(string)

	old := uploaded(t, upload(t, srv, freelancer, taskID, "old.txt", eicar))
	if old["scan"] != "unscanned" {
		t.Fatalf("scan without a scanner = %v, want unscanned", old["scan"])
	}
	resp := get(t, freelancer, srv.URL+"/api/v1/attachments/"+old["id"].(string))
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// The same database and files, now with a scanner.
	clamd := newFakeClamd(t)
	cfg := testCfg
	cfg.UploadsDir = uploads
	cfg.ClamAVAddress = clamd.addr
	app, err := server.New(cfg, testPool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go app.Run(ctx)
	waitScan(t, srv.URL, freelancer, taskID, old["id"].(string), "infected")
}
