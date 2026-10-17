package reports

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// ErrPDFUnavailable means no Chrome/Chromium binary was found on this server.
var ErrPDFUnavailable = errors.New("pdf export unavailable: chrome not found")

const (
	pdfTimeout     = 30 * time.Second
	maxConcurrency = 2
)

// PDFRenderer prints HTML to PDF with headless Chrome/Chromium.
type PDFRenderer struct {
	chrome string
	sem    chan struct{}
}

// NewPDFRenderer uses chromePath when set, otherwise looks for a Chrome or
// Chromium binary in the usual places. A renderer without a binary returns
// ErrPDFUnavailable from Render.
func NewPDFRenderer(chromePath string) *PDFRenderer {
	return &PDFRenderer{chrome: findChrome(chromePath), sem: make(chan struct{}, maxConcurrency)}
}

// Available reports whether a Chrome binary was found.
func (r *PDFRenderer) Available() bool { return r.chrome != "" }

func findChrome(explicit string) string {
	if explicit != "" {
		if p, err := exec.LookPath(explicit); err == nil {
			return p
		}
		return ""
	}
	for _, name := range []string{"chromium-browser", "chromium", "google-chrome", "google-chrome-stable"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	for _, p := range []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// Render prints an HTML document (rendered in ModePDF) to PDF bytes.
//
// Each run gets a throwaway profile and temp dir, and all network access is
// routed to a dead proxy. The document itself is our own escaped template
// output and carries a CSP that forbids scripts and every external load.
// (Chrome's scriptEnabled=false blink setting can't be used: it breaks
// --print-to-pdf in the new headless mode.)
func (r *PDFRenderer) Render(ctx context.Context, html []byte) ([]byte, error) {
	if r.chrome == "" {
		return nil, ErrPDFUnavailable
	}

	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	dir, err := os.MkdirTemp("", "helpic-pdf-*")
	if err != nil {
		return nil, fmt.Errorf("pdf temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	in := filepath.Join(dir, "report.html")
	out := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(in, html, 0o600); err != nil {
		return nil, fmt.Errorf("pdf write html: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, pdfTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, r.chrome,
		"--headless=new",
		"--no-sandbox",
		"--disable-gpu",
		"--disable-dev-shm-usage",
		"--disable-extensions",
		"--disable-background-networking",
		"--disable-sync",
		"--no-first-run",
		"--no-default-browser-check",
		"--mute-audio",
		"--proxy-server=127.0.0.1:9",
		"--proxy-bypass-list=<-loopback>",
		"--user-data-dir="+filepath.Join(dir, "profile"),
		"--crash-dumps-dir="+dir,
		"--no-pdf-header-footer",
		"--print-to-pdf="+out,
		"file://"+in,
	)
	cmd.Env = append(os.Environ(), "HOME="+dir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("pdf render timed out: %w", ctx.Err())
		}
		tail := stderr.Bytes()
		if len(tail) > 500 {
			tail = tail[len(tail)-500:]
		}
		return nil, fmt.Errorf("pdf render: %w: %s", err, tail)
	}

	pdf, err := os.ReadFile(out)
	if err != nil {
		return nil, fmt.Errorf("pdf read output: %w", err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF")) {
		return nil, errors.New("pdf render: output is not a PDF")
	}
	return pdf, nil
}
