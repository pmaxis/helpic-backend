package pdf_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pmaxis/helpic/internal/pdf"
)

func TestClient_NoURL(t *testing.T) {
	c := pdf.NewClient("")
	if c.Available() {
		t.Fatal("available without a URL")
	}
	if _, err := c.Render(context.Background(), []byte("<p>hi</p>")); !errors.Is(err, pdf.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestClient_Render(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/render" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		got = string(body)
		_, _ = w.Write([]byte("%PDF-1.7 fake"))
	}))
	defer srv.Close()

	file, err := pdf.NewClient(srv.URL+"/").Render(context.Background(), []byte("<p>hi</p>"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "<p>hi</p>" || string(file) != "%PDF-1.7 fake" {
		t.Fatalf("sent %q, got %q", got, file)
	}
}

func TestClient_ServiceFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := pdf.NewClient(srv.URL).Render(context.Background(), []byte("<p>hi</p>"))
	if err == nil || errors.Is(err, pdf.ErrUnavailable) {
		t.Fatalf("err = %v, want a render failure", err)
	}
}

func TestHandler_NoChrome(t *testing.T) {
	srv := httptest.NewServer(pdf.NewHandler(pdf.NewChrome("/nonexistent/helpic-test-chrome")))
	defer srv.Close()

	if _, err := pdf.NewClient(srv.URL).Render(context.Background(), []byte("<p>hi</p>")); err == nil {
		t.Fatal("rendered without Chrome")
	}
}
