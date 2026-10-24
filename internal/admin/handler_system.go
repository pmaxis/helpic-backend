package admin

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/pmaxis/helpic/internal/request"

	"github.com/pmaxis/helpic/internal/response"
)

// UseSystem lets the panel report on the server it is served from.
func (h *Handler) UseSystem(s System) {
	h.system = s
}

// AlertTester sends a test alert to the admins' chat.
type AlertTester interface {
	Test(ctx context.Context, by string) error
}

// UseAlerts lets the panel send a test alert; without it, alerts are off.
func (h *Handler) UseAlerts(a AlertTester) {
	h.alerts = a
}

// TestAlert handles POST /api/v1/admin/system/test-alert.
//
//	@Summary		Send a test alert
//	@Description	Posts a test message to the admins' alert chat, saying which admin asked, to see alerts arrive.
//	@Tags			admin
//	@Success		204
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403	{object}	response.ErrorEnvelope	"Not an admin, or two-factor is off"
//	@Failure		409	{object}	response.ErrorEnvelope	"Alerts are off"
//	@Failure		422	{object}	response.ErrorEnvelope	"Telegram didn't take it"
//	@Router			/admin/system/test-alert [post]
func (h *Handler) TestAlert(w http.ResponseWriter, r *http.Request) {
	adminID, ok := request.User(w, r)
	if !ok {
		return
	}
	if h.alerts == nil {
		response.Error(w, http.StatusConflict, response.CodeConflict, "Alerts are off: set ALERT_TELEGRAM_BOT_TOKEN and ALERT_TELEGRAM_CHAT_ID")
		return
	}
	email, err := h.svc.repo.Email(r.Context(), adminID)
	if err != nil {
		response.Internal(w, err)
		return
	}
	if err := h.alerts.Test(r.Context(), email); err != nil {
		slog.Warn("test alert failed", "error", err)
		response.Error(w, http.StatusUnprocessableEntity, response.CodeUnprocessable, "Telegram didn't take the test alert: check the bot's token, the chat ID and that the bot is in the chat")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// CheckDTO is how one service Helpic depends on answered.
type CheckDTO struct {
	// Name is database, mail, telegram or alerts.
	Name string `json:"name"`
	// State is ok, failing or off (left out of the config).
	State     string `json:"state"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error"`
}

// JobDTO is how a background job's runs on this server went.
type JobDTO struct {
	Name            string     `json:"name"`
	IntervalSeconds int64      `json:"interval_seconds"`
	LastRun         *time.Time `json:"last_run"`
	LastSuccess     *time.Time `json:"last_success"`
	LastError       string     `json:"last_error"`
	Running         bool       `json:"running"`
	Runs            int        `json:"runs"`
	Failures        int        `json:"failures"`
	// State is ok, failing (the last run failed), late (it hasn't run again
	// in time) or waiting (not run yet).
	State string `json:"state"`
}

// UploaderDTO is an account by what it has uploaded.
type UploaderDTO struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
	Files int    `json:"files"`
	Bytes int64  `json:"bytes"`
}

// DiskDTO is the disk the files are kept on.
type DiskDTO struct {
	TotalBytes uint64 `json:"total_bytes"`
	FreeBytes  uint64 `json:"free_bytes"`
}

// StorageDTO is the files on tasks as they stand: sizes and counts only.
type StorageDTO struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
	// Accounts have uploaded a file at least; NearQuota have used 90% of
	// the quota or more, 0 without a quota.
	Accounts     int   `json:"accounts"`
	NearQuota    int   `json:"near_quota"`
	QuotaBytes   int64 `json:"quota_bytes"`
	MaxFileBytes int64 `json:"max_file_bytes"`
	// Top are the accounts that uploaded the most, the most first.
	Top []UploaderDTO `json:"top"`
	// Disk is null when it can't be read.
	Disk *DiskDTO `json:"disk"`
}

func toStorageDTO(s Storage) StorageDTO {
	out := StorageDTO{Files: s.Files, Bytes: s.Bytes, Accounts: s.Accounts, NearQuota: s.NearQuota,
		QuotaBytes: s.QuotaBytes, MaxFileBytes: s.MaxFileBytes, Top: make([]UploaderDTO, 0, len(s.Top))}
	for _, u := range s.Top {
		out.Top = append(out.Top, UploaderDTO{ID: u.ID, Email: u.Email, Name: u.Name, Files: u.Files, Bytes: u.Bytes})
	}
	if s.Disk != nil {
		out.Disk = &DiskDTO{TotalBytes: s.Disk.TotalBytes, FreeBytes: s.Disk.FreeBytes}
	}
	return out
}

// SystemResponse is the server as it stands.
type SystemResponse struct {
	Version   string         `json:"version"`
	GoVersion string         `json:"go_version"`
	StartedAt time.Time      `json:"started_at"`
	Checks    []CheckDTO     `json:"checks"`
	Jobs      []JobDTO       `json:"jobs"`
	Emails    EmailCountsDTO `json:"emails"`
	Storage   StorageDTO     `json:"storage"`
}

func optionalTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// System handles GET /api/v1/admin/system.
//
//	@Summary		System status
//	@Description	Checks the database, the mail server and the Telegram bot on the spot, and tells how this server's background jobs ran since it started, how the outgoing email stands and how much the files on tasks take, against the quota and the disk.
//	@Tags			admin
//	@Produce		json
//	@Success		200	{object}	SystemResponse			"OK"
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403	{object}	response.ErrorEnvelope	"Not an admin, or two-factor is off"
//	@Router			/admin/system [get]
func (h *Handler) System(w http.ResponseWriter, r *http.Request) {
	rep := h.system.Report(r.Context())
	out := SystemResponse{
		Version: rep.Version, GoVersion: rep.GoVersion, StartedAt: rep.Started,
		Checks: make([]CheckDTO, 0, len(rep.Checks)), Jobs: make([]JobDTO, 0, len(rep.Jobs)),
	}
	for _, c := range rep.Checks {
		out.Checks = append(out.Checks, CheckDTO{Name: c.Name, State: c.State, LatencyMS: c.Latency.Milliseconds(), Error: c.Error})
	}
	for _, j := range rep.Jobs {
		out.Jobs = append(out.Jobs, JobDTO{
			Name: j.Name, IntervalSeconds: int64(j.Interval / time.Second),
			LastRun: optionalTime(j.LastRun), LastSuccess: optionalTime(j.LastSuccess), LastError: j.LastError,
			Running: j.Running, Runs: j.Runs, Failures: j.Failures, State: j.State,
		})
	}
	// The email counts come from the database: unlike the jobs, they cover
	// every server.
	if counts, err := h.svc.EmailCounts(r.Context()); err == nil {
		out.Emails = toEmailCountsDTO(counts)
	}
	st, err := h.svc.Storage(r.Context())
	if err != nil {
		slog.Warn("read storage", "error", err)
	}
	out.Storage = toStorageDTO(st)
	response.JSON(w, http.StatusOK, out)
}
