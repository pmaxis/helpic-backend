package integration

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// logoPNG is a small PNG, filled with the given shade so two logos differ.
func logoPNG(t *testing.T, shade uint8) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 4))
	for x := range 8 {
		for y := range 4 {
			img.Set(x, y, color.RGBA{shade, 0, 0, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func putLogo(t *testing.T, srv *httptest.Server, c *http.Client, content []byte) *http.Response {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "logo.png")
	if err != nil {
		t.Fatal(err)
	}
	part.Write(content)
	mw.Close()
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/v1/me/branding/logo", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// image reads an image response's bytes, wanting the given status.
func imageBytes(t *testing.T, c *http.Client, url string, want int) []byte {
	t.Helper()
	resp := get(t, c, url)
	defer resp.Body.Close()
	wantStatus(t, resp, want)
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if want == http.StatusOK && resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("logo content type = %q", resp.Header.Get("Content-Type"))
	}
	return data
}

func TestBranding_Settings(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	url := srv.URL + "/api/v1/me/branding"

	// Nothing to start with.
	resp := get(t, freelancer, url)
	wantStatus(t, resp, http.StatusOK)
	if b := field(decode(t, resp), "branding"); b["color"] != nil || b["details"] != "" || b["logo_id"] != nil {
		t.Fatalf("new branding = %v", b)
	}
	// Reports are the freelancer's, so the look is too.
	resp = get(t, client, url)
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()

	// A color and details, trimmed and in lower case.
	resp = do(t, freelancer, http.MethodPut, url, map[string]any{"color": " #1D4ED8 ", "details": "  ФОП Іваненко\r\nIBAN UA00 0000  "})
	wantStatus(t, resp, http.StatusOK)
	if b := field(decode(t, resp), "branding"); b["color"] != "#1d4ed8" || b["details"] != "ФОП Іваненко\nIBAN UA00 0000" {
		t.Fatalf("saved branding = %v", b)
	}
	for _, body := range []map[string]any{
		{"color": "blue", "details": ""},
		{"color": nil, "details": string(bytes.Repeat([]byte("line\n"), 16))},
	} {
		resp = do(t, freelancer, http.MethodPut, url, body)
		wantStatus(t, resp, http.StatusBadRequest)
		resp.Body.Close()
	}
	// "" clears the color.
	resp = do(t, freelancer, http.MethodPut, url, map[string]any{"color": "", "details": "ФОП Іваненко"})
	wantStatus(t, resp, http.StatusOK)
	if b := field(decode(t, resp), "branding"); b["color"] != nil {
		t.Fatalf("cleared color = %v", b)
	}

	// A logo is a whole PNG or JPEG.
	for _, bad := range [][]byte{[]byte("<svg xmlns='http://www.w3.org/2000/svg'/>"), pngBytes} {
		resp = putLogo(t, srv, freelancer, bad)
		wantStatus(t, resp, http.StatusBadRequest)
		resp.Body.Close()
	}
	resp = putLogo(t, srv, freelancer, append(logoPNG(t, 1), bytes.Repeat([]byte{0}, 512<<10)...))
	wantStatus(t, resp, http.StatusRequestEntityTooLarge)
	resp.Body.Close()
	imageBytes(t, freelancer, url+"/logo", http.StatusNotFound)

	logo := logoPNG(t, 10)
	resp = putLogo(t, srv, freelancer, logo)
	wantStatus(t, resp, http.StatusOK)
	if b := field(decode(t, resp), "branding"); b["logo_id"] == nil || b["details"] != "ФОП Іваненко" {
		t.Fatalf("branding with a logo = %v", b)
	}
	if got := imageBytes(t, freelancer, url+"/logo", http.StatusOK); !bytes.Equal(got, logo) {
		t.Fatal("the logo came back changed")
	}

	resp = do(t, freelancer, http.MethodDelete, url+"/logo", nil)
	wantStatus(t, resp, http.StatusOK)
	if b := field(decode(t, resp), "branding"); b["logo_id"] != nil {
		t.Fatalf("branding without the logo = %v", b)
	}
	imageBytes(t, freelancer, url+"/logo", http.StatusNotFound)
}

func TestBranding_SentReportKeepsItsLook(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	clientID := myID(t, srv, client)
	today := time.Now().Format("2006-01-02")
	brandingURL := srv.URL + "/api/v1/me/branding"

	task := newTask(t, srv, freelancer, map[string]any{"client_id": clientID, "title": "Logo"})
	resp := post(t, freelancer, srv.URL+"/api/v1/tasks/"+task["id"].(string)+"/status", map[string]string{"status": "done"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	resp = do(t, freelancer, http.MethodPut, brandingURL, map[string]any{"color": "#123456", "details": "Old details"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	oldLogo := logoPNG(t, 20)
	resp = putLogo(t, srv, freelancer, oldLogo)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	resp = post(t, freelancer, srv.URL+"/api/v1/reports", map[string]string{"client_id": clientID, "from": today, "to": today})
	wantStatus(t, resp, http.StatusCreated)
	rep := field(decode(t, resp), "report")
	reportURL := srv.URL + "/api/v1/reports/" + rep["id"].(string)
	if b := field(rep, "branding"); b["color"] != "#123456" || b["details"] != "Old details" || b["logo_id"] == nil {
		t.Fatalf("draft's branding = %v", b)
	}
	// The client sees no draft, nor its logo.
	imageBytes(t, client, reportURL+"/logo", http.StatusNotFound)

	resp = post(t, freelancer, reportURL+"/send", nil)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// A new look, and the logo taken away.
	resp = do(t, freelancer, http.MethodPut, brandingURL, map[string]any{"color": "#abcdef", "details": "New details"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = do(t, freelancer, http.MethodDelete, brandingURL+"/logo", nil)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// The sent report still looks as it was sent, to both sides.
	for _, c := range []*http.Client{freelancer, client} {
		resp = get(t, c, reportURL)
		wantStatus(t, resp, http.StatusOK)
		if b := field(field(decode(t, resp), "report"), "branding"); b["color"] != "#123456" || b["details"] != "Old details" || b["logo_id"] == nil {
			t.Fatalf("sent report's branding = %v", b)
		}
		if got := imageBytes(t, c, reportURL+"/logo", http.StatusOK); !bytes.Equal(got, oldLogo) {
			t.Fatal("the sent report's logo changed")
		}
	}

	// A new draft takes the new look; without a logo, it has none.
	other := newTask(t, srv, freelancer, map[string]any{"client_id": clientID, "title": "Landing"})
	resp = post(t, freelancer, srv.URL+"/api/v1/tasks/"+other["id"].(string)+"/status", map[string]string{"status": "done"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = post(t, freelancer, srv.URL+"/api/v1/reports", map[string]string{"client_id": clientID, "from": today, "to": today})
	wantStatus(t, resp, http.StatusCreated)
	draft := field(decode(t, resp), "report")
	if b := field(draft, "branding"); b["color"] != "#abcdef" || b["details"] != "New details" || b["logo_id"] != nil {
		t.Fatalf("new draft's branding = %v", b)
	}
	imageBytes(t, freelancer, srv.URL+"/api/v1/reports/"+draft["id"].(string)+"/logo", http.StatusNotFound)
}
