package pdf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const maxPDFBytes = 50 << 20

// Client prints PDFs through the pdf service.
type Client struct {
	url  string
	http *http.Client
}

// NewClient talks to the pdf service at baseURL (e.g. http://pdf:8081).
// Without a URL Render returns ErrUnavailable.
func NewClient(baseURL string) *Client {
	return &Client{
		url:  strings.TrimRight(baseURL, "/"),
		http: &http.Client{Timeout: 3 * renderTimeout},
	}
}

// Available reports whether a pdf service is configured.
func (c *Client) Available() bool { return c.url != "" }

// Render prints an HTML document to PDF bytes.
func (c *Client) Render(ctx context.Context, html []byte) ([]byte, error) {
	if c.url == "" {
		return nil, ErrUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+"/render", bytes.NewReader(html))
	if err != nil {
		return nil, fmt.Errorf("pdf request: %w", err)
	}
	req.Header.Set("Content-Type", "text/html; charset=utf-8")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pdf service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return nil, fmt.Errorf("pdf service: %s: %s", resp.Status, bytes.TrimSpace(msg))
	}
	pdf, err := io.ReadAll(io.LimitReader(resp.Body, maxPDFBytes))
	if err != nil {
		return nil, fmt.Errorf("pdf service read: %w", err)
	}
	if !isPDF(pdf) {
		return nil, errors.New("pdf service: response is not a PDF")
	}
	return pdf, nil
}
