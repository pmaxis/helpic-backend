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
	freelancerID, clientID := myID(t, srv, freelancer), myID(t, srv, client)
	taskID := newTask(t, srv, freelancer, map[string]any{"client_id": clientID, "title": "Logo"})["id"].(string)

	// The freelancer comes near the quota, at 1.9 of 2 MB with 1 MB and 900 KB; the client uploads a little.
	for _, f := range []struct {
		c    *http.Client
		name string
		size int
	}{{freelancer, "a.txt", 1 << 20}, {freelancer, "b.txt", 900 << 10}, {client, "brief.txt", 1000}} {
		resp := upload(t, srv, f.c, taskID, f.name, bytes.Repeat([]byte("x"), f.size))
		wantStatus(t, resp, http.StatusCreated)
		resp.Body.Close()
	}

	// The account's card says how much it uploaded, and against what quota.
	user := field(decode(t, get(t, boss, srv.URL+"/api/v1/admin/users/"+freelancerID)), "user")
	if user["files"] != 2.0 || user["files_bytes"] != float64(1<<20+900<<10) || user["quota_bytes"] != float64(2<<20) {
		t.Fatalf("freelancer's files = %v, %v of %v", user["files"], user["files_bytes"], user["quota_bytes"])
	}

	// The system page has every file, who is near the quota, who uploaded the most and the disk.
	resp := get(t, boss, srv.URL+"/api/v1/admin/system")
	wantStatus(t, resp, http.StatusOK)
	st := decode(t, resp)["storage"].(map[string]any)
	if st["files"] != 3.0 || st["bytes"] != float64(1<<20+900<<10+1000) || st["accounts"] != 2.0 || st["near_quota"] != 1.0 {
		t.Fatalf("storage = %v", st)
	}
	if st["quota_bytes"] != float64(2<<20) || st["max_file_bytes"] != float64(1<<20) {
		t.Fatalf("limits = %v, %v", st["quota_bytes"], st["max_file_bytes"])
	}
	top := st["top"].([]any)
	if len(top) != 2 || top[0].(map[string]any)["id"] != freelancerID || top[0].(map[string]any)["files"] != 2.0 || top[1].(map[string]any)["id"] != clientID {
		t.Fatalf("top = %v", top)
	}
	disk, ok := st["disk"].(map[string]any)
	if !ok || disk["total_bytes"].(float64) <= 0 || disk["free_bytes"].(float64) > disk["total_bytes"].(float64) {
		t.Fatalf("disk = %v", st["disk"])
	}
}

func TestAdmin_StorageWithoutQuota(t *testing.T) {
	srv, _, _ := newAPIWith(t, func(cfg *config.Config) { cfg.UploadQuotaBytes = 0 })
	boss := signInAdmin(t, srv, "boss@example.com")
	turnOnTwoFactor(t, srv, boss)

	resp := get(t, boss, srv.URL+"/api/v1/admin/system")
	wantStatus(t, resp, http.StatusOK)
	st := decode(t, resp)["storage"].(map[string]any)
	if st["files"] != 0.0 || st["near_quota"] != 0.0 || st["quota_bytes"] != 0.0 || len(st["top"].([]any)) != 0 {
		t.Fatalf("storage = %v", st)
	}
}
