package integration

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/pmaxis/helpic/internal/config"
)

func TestAdmin_Storage(t *testing.T) {
	srv, _, _ := newAPIWith(t, func(cfg *config.Config) {
		cfg.MaxUploadBytes = 1 << 20
		cfg.UploadQuotaBytes = 2 << 20
	})
	boss := signInAdmin(t, srv, "boss@example.com")
	turnOnTwoFactor(t, srv, boss)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	freelancerID := myID(t, srv, freelancer)
	taskID := newTask(t, srv, freelancer, map[string]any{"client_id": myID(t, srv, client), "title": "Logo"})["id"].(string)
	for _, name := range []string{"a.txt", "b.txt"} {
		resp := upload(t, srv, freelancer, taskID, name, bytes.Repeat([]byte("x"), 900<<10))
		wantStatus(t, resp, http.StatusCreated)
		resp.Body.Close()
	}

	// The account's card says how much it uploaded, and against what quota.
	user := field(decode(t, get(t, boss, srv.URL+"/api/v1/admin/users/"+freelancerID)), "user")
	if user["files"] != 2.0 || user["files_bytes"] != float64(2*900<<10) || user["quota_bytes"] != float64(2<<20) {
		t.Fatalf("freelancer's files = %v, %v of %v", user["files"], user["files_bytes"], user["quota_bytes"])
	}

	// The system status tells how much the files uploaded take, and how
	// much room their disk has left.
	resp := get(t, boss, srv.URL+"/api/v1/admin/system")
	wantStatus(t, resp, http.StatusOK)
	body := decode(t, resp)
	if files := body["files"].(map[string]any); files["count"] != 2.0 || files["bytes"] != float64(2*900<<10) {
		t.Fatalf("files = %v", files)
	}
	disk, ok := body["disk"].(map[string]any)
	if !ok || disk["total_bytes"].(float64) <= 0 || disk["free_bytes"].(float64) > disk["total_bytes"].(float64) {
		t.Fatalf("disk = %v", disk)
	}
}
