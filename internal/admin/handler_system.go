package admin

import (
	"net/http"
	"time"

	"github.com/pmaxis/helpic/internal/response"
)

// UseSystem lets the panel report on the server it is served from.
func (h *Handler) UseSystem(s System) {
	h.system = s
}

// CheckDTO is how one service Helpic depends on answered.
type CheckDTO struct {
	// Name is database, mail or telegram.
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

// SystemResponse is the server as it stands.
type SystemResponse struct {
	Version   string         `json:"version"`
	GoVersion string         `json:"go_version"`
	StartedAt time.Time      `json:"started_at"`
	Checks    []CheckDTO     `json:"checks"`
	Jobs      []JobDTO       `json:"jobs"`
	Emails    EmailCountsDTO `json:"emails"`
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
//	@Description	Checks the database, the mail server and the Telegram bot on the spot, and tells how this server's background jobs ran since it started and how the outgoing email stands.
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
	response.JSON(w, http.StatusOK, out)
}
