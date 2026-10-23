package admin

import (
	"context"
	"runtime"
	"sync"
	"time"

	"github.com/pmaxis/helpic/internal/jobs"
)

// The state of what Helpic runs on: the services it talks to, checked on
// request, and its background jobs.

// Probe checks one service Helpic depends on. Check is nil when the service
// is turned off in the config.
type Probe struct {
	Name  string
	Check func(context.Context) error
}

// How a probe or a job stands.
const (
	StateOK = "ok"
	// StateOff is a service the config leaves out.
	StateOff = "off"
	// StateFailing is a check, or a job's last run, that failed.
	StateFailing = "failing"
	// StateLate is a job that should have run again by now.
	StateLate = "late"
	// StateWaiting is a job that hasn't run yet.
	StateWaiting = "waiting"
)

// probeTimeout bounds one check.
const probeTimeout = 5 * time.Second

// System reports on the server the panel is served from.
type System struct {
	Version string
	Started time.Time
	Probes  []Probe
	Jobs    *jobs.Registry
}

// CheckResult is how one probe went.
type CheckResult struct {
	Name    string
	State   string
	Latency time.Duration
	Error   string
}

// JobResult is a job's status and what it means.
type JobResult struct {
	jobs.Status
	State string
}

// Report is the server as it stands.
type Report struct {
	Version   string
	GoVersion string
	Started   time.Time
	Checks    []CheckResult
	Jobs      []JobResult
}

// Report runs every probe at once and reads the jobs' status.
func (s System) Report(ctx context.Context) Report {
	out := Report{Version: s.Version, GoVersion: runtime.Version(), Started: s.Started, Checks: make([]CheckResult, len(s.Probes))}
	var wg sync.WaitGroup
	for i, p := range s.Probes {
		out.Checks[i] = CheckResult{Name: p.Name, State: StateOff}
		if p.Check == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(ctx, probeTimeout)
			defer cancel()
			start := time.Now()
			err := p.Check(ctx)
			res := &out.Checks[i]
			res.Latency, res.State = time.Since(start), StateOK
			if err != nil {
				res.State, res.Error = StateFailing, err.Error()
			}
		}()
	}
	wg.Wait()

	if s.Jobs != nil {
		now := time.Now()
		for _, st := range s.Jobs.Statuses() {
			out.Jobs = append(out.Jobs, JobResult{Status: st, State: jobState(st, now)})
		}
	}
	return out
}

// jobState tells whether a job is keeping up. One that hasn't started its
// next run two intervals and a minute after the last is late.
func jobState(st jobs.Status, now time.Time) string {
	switch {
	case st.LastRun.IsZero():
		return StateWaiting
	case st.LastError != "":
		return StateFailing
	case !st.Running && now.Sub(st.LastRun) > 2*st.Interval+time.Minute:
		return StateLate
	}
	return StateOK
}
