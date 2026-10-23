package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/pmaxis/helpic/internal/applog"
	"github.com/pmaxis/helpic/internal/request"
	"github.com/pmaxis/helpic/internal/response"
	"github.com/pmaxis/helpic/internal/user"
)

// Handler exposes the admin panel over HTTP.
type Handler struct {
	svc  *Service
	logs *applog.Repository
}

// NewHandler creates an admin Handler; logs are the server's warnings and
// errors.
func NewHandler(svc *Service, logs *applog.Repository) *Handler {
	return &Handler{svc: svc, logs: logs}
}

// Routes mounts the endpoints. Must be called inside an authenticated group;
// they answer only admins with two-factor on.
func (h *Handler) Routes(r chi.Router) {
	r.Route("/admin", func(r chi.Router) {
		r.Use(h.RequireAdmin)
		r.Get("/stats", h.Stats)
		r.Get("/users", h.Users)
		r.Get("/users/{id}", h.User)
		r.Get("/logs", h.Logs)
		r.Get("/actions", h.Actions)
		r.Post("/users/{id}/block", h.act(ActionBlock))
		r.Post("/users/{id}/unblock", h.act(ActionUnblock))
		r.Post("/users/{id}/end-sessions", h.act(ActionEndSessions))
		r.Post("/users/{id}/reset-two-factor", h.act(ActionResetTwoFactor))
	})
}

// RequireAdmin lets through only admins who sign in with two-factor; others
// get a 403.
func (h *Handler) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID, role, ok := request.UserRole(w, r)
		if !ok {
			return
		}
		if role != user.RoleAdmin {
			response.Error(w, http.StatusForbidden, response.CodeForbidden, "You don't have access to this resource")
			return
		}
		twoFactor, err := h.svc.TwoFactor(r.Context(), userID)
		if err != nil {
			response.Internal(w, err)
			return
		}
		if !twoFactor {
			response.Error(w, http.StatusForbidden, response.CodeForbidden, "Turn on two-factor sign-in to open the admin panel")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// TotalsDTO is the app as it stands.
type TotalsDTO struct {
	Users          int `json:"users"`
	Freelancers    int `json:"freelancers"`
	Clients        int `json:"clients"`
	PendingSignups int `json:"pending_signups"`
	TwoFactor      int `json:"two_factor"`
	ActiveDay      int `json:"active_day"`
	ActiveWeek     int `json:"active_week"`
	ActiveMonth    int `json:"active_month"`
	Tasks          int `json:"tasks"`
	Projects       int `json:"projects"`
}

// PeriodDTO is what happened over a stretch of days.
type PeriodDTO struct {
	// Active is how many people used Helpic on any of the days.
	Active         int `json:"active"`
	NewUsers       int `json:"new_users"`
	TasksCreated   int `json:"tasks_created"`
	TasksCompleted int `json:"tasks_completed"`
}

func toPeriodDTO(p Period) PeriodDTO {
	return PeriodDTO{
		Active: p.Active, NewUsers: p.NewUsers, TasksCreated: p.TasksCreated, TasksCompleted: p.TasksCompleted,
	}
}

// DayDTO is one day.
type DayDTO struct {
	// Day is YYYY-MM-DD in the app's time zone.
	Day            string `json:"day"`
	Signups        int    `json:"signups"`
	Active         int    `json:"active"`
	TasksCreated   int    `json:"tasks_created"`
	TasksCompleted int    `json:"tasks_completed"`
}

func toDayDTOs(days []Day) []DayDTO {
	out := make([]DayDTO, 0, len(days))
	for _, d := range days {
		out = append(out, DayDTO{Day: date(d.Day), Signups: d.Signups, Active: d.Active, TasksCreated: d.TasksCreated, TasksCompleted: d.TasksCompleted})
	}
	return out
}

// StatsResponse is everything the statistics page shows.
type StatsResponse struct {
	// From and To are the period's first and last day, YYYY-MM-DD.
	From     string    `json:"from"`
	To       string    `json:"to"`
	Totals   TotalsDTO `json:"totals"`
	Period   PeriodDTO `json:"period"`
	Previous PeriodDTO `json:"previous"`
	Days     []DayDTO  `json:"days"`
	// PreviousDays is the period before, as long, to compare with.
	PreviousDays []DayDTO `json:"previous_days"`
}

// Stats handles GET /api/v1/admin/stats?days={n}.
//
//	@Summary		How Helpic is used
//	@Description	Totals now; the last days (today included) against the days before them, also day by day. Days are the app's time zone's. Counts only, never anyone's content.
//	@Tags			admin
//	@Produce		json
//	@Param			days	query		int						false	"The period, 7 to 365 days; 30 by default"
//	@Success		200		{object}	StatsResponse			"OK"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid period"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403		{object}	response.ErrorEnvelope	"Not an admin, or two-factor is off"
//	@Router			/admin/stats [get]
func (h *Handler) Stats(w http.ResponseWriter, r *http.Request) {
	days := 30
	if raw := r.URL.Query().Get("days"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < MinPeriod || n > MaxPeriod {
			response.ValidationError(w, map[string]string{"days": "Ask for 7 to 365 days"})
			return
		}
		days = n
	}
	s, err := h.svc.Stats(r.Context(), days)
	if err != nil {
		response.Internal(w, err)
		return
	}
	t := s.Totals
	out := StatsResponse{
		From: date(s.From),
		To:   date(s.To),
		Totals: TotalsDTO{
			Users: t.Users, Freelancers: t.Freelancers, Clients: t.Clients, PendingSignups: t.PendingSignups, TwoFactor: t.TwoFactor,
			ActiveDay: t.ActiveDay, ActiveWeek: t.ActiveWeek, ActiveMonth: t.ActiveMonth, Tasks: t.Tasks, Projects: t.Projects,
		},
		Period:       toPeriodDTO(s.Period),
		Previous:     toPeriodDTO(s.Previous),
		Days:         toDayDTOs(s.Days),
		PreviousDays: toDayDTOs(s.PreviousDays),
	}
	response.JSON(w, http.StatusOK, out)
}

// AccountDTO is an account in the admin panel's list.
type AccountDTO struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	TwoFactor bool   `json:"two_factor"`
	// BlockedAt is when an admin blocked them; null when they aren't.
	BlockedAt *time.Time `json:"blocked_at"`
	Tasks     int        `json:"tasks"`
	Projects  int        `json:"projects"`
	// ActiveDays is on how many of the last 30 days they used Helpic.
	ActiveDays int `json:"active_days"`
	// LastActive is the last day they used Helpic, YYYY-MM-DD; null if never.
	LastActive *string   `json:"last_active"`
	CreatedAt  time.Time `json:"created_at"`
}

func toAccountDTO(a Account) AccountDTO {
	d := AccountDTO{ID: a.ID, Email: a.Email, Name: a.Name, Role: a.Role, TwoFactor: a.TwoFactor, BlockedAt: a.BlockedAt, Tasks: a.Tasks, Projects: a.Projects, ActiveDays: a.ActiveDays, CreatedAt: a.CreatedAt}
	if a.LastActive != nil {
		day := date(*a.LastActive)
		d.LastActive = &day
	}
	return d
}

// UsersResponse is one page of accounts and how many match in all.
type UsersResponse struct {
	Users []AccountDTO `json:"users"`
	Page  int          `json:"page"`
	Limit int          `json:"limit"`
	Total int          `json:"total"`
}

// accountsPage is a page's size; maxAccountsPage the most one may ask for.
const (
	accountsPage    = 25
	maxAccountsPage = 100
)

// Users handles GET /api/v1/admin/users?q={text}&role={role}&activity={activity}&sort={sort}&page={n}&limit={n}.
//
//	@Summary		Accounts
//	@Description	Every account, the newest first: who they are and how much they use Helpic. A page at a time, with how many match in all.
//	@Tags			admin
//	@Produce		json
//	@Param			q			query		string					false	"Part of the email or the name"
//	@Param			role		query		string					false	"freelancer or client"
//	@Param			activity	query		string					false	"active (in the last 7 days), idle (not in the last 30), never or blocked"
//	@Param			sort		query		string					false	"created (newest first, the default), last_active or tasks"
//	@Param			page		query		int						false	"The page, from 1"
//	@Param			limit		query		int						false	"Accounts per page, 25 by default, at most 100"
//	@Success		200			{object}	UsersResponse			"OK"
//	@Failure		400			{object}	response.ErrorEnvelope	"Invalid role, activity, sort, page or limit"
//	@Failure		401			{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403			{object}	response.ErrorEnvelope	"Not an admin, or two-factor is off"
//	@Router			/admin/users [get]
func (h *Handler) Users(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := AccountFilter{Query: q.Get("q"), Role: q.Get("role"), Activity: q.Get("activity"), Sort: q.Get("sort"), Page: 1, Limit: accountsPage}
	if f.Role != "" && f.Role != user.RoleFreelancer && f.Role != user.RoleClient {
		response.ValidationError(w, map[string]string{"role": "Choose freelancer or client"})
		return
	}
	if f.Activity != "" && f.Activity != ActivityActive && f.Activity != ActivityIdle && f.Activity != ActivityNever && f.Activity != ActivityBlocked {
		response.ValidationError(w, map[string]string{"activity": "Choose active, idle, never or blocked"})
		return
	}
	if f.Sort == "" {
		f.Sort = SortCreated
	} else if _, ok := orders[f.Sort]; !ok {
		response.ValidationError(w, map[string]string{"sort": "Sort by created, last_active or tasks"})
		return
	}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxAccountsPage {
			response.ValidationError(w, map[string]string{"limit": "Ask for 1 to 100 accounts"})
			return
		}
		f.Limit = n
	}
	if raw := q.Get("page"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			response.ValidationError(w, map[string]string{"page": "This page link is broken. Start from the first page."})
			return
		}
		f.Page = n
	}
	list, total, err := h.svc.Accounts(r.Context(), f)
	if err != nil {
		response.Internal(w, err)
		return
	}
	out := UsersResponse{Users: make([]AccountDTO, 0, len(list)), Page: f.Page, Limit: f.Limit, Total: total}
	for _, a := range list {
		out.Users = append(out.Users, toAccountDTO(a))
	}
	response.JSON(w, http.StatusOK, out)
}

// TeamDTO is the client company an account owns or belongs to.
type TeamDTO struct {
	Name string `json:"name"`
	// Role is owner, member or contractor.
	Role    string `json:"role"`
	Members int    `json:"members"`
}

// AccountDetailDTO is one account in full: still counts and metadata, never
// their work.
type AccountDetailDTO struct {
	AccountDTO
	Locale   string   `json:"locale"`
	Currency string   `json:"currency"`
	Team     *TeamDTO `json:"team"`
	// Partners are a freelancer's clients, or a client's freelancers.
	Partners      int   `json:"partners"`
	ReportsSent   int   `json:"reports_sent"`
	ReportsPaid   int   `json:"reports_paid"`
	SecondsLogged int64 `json:"seconds_logged"`
	Comments      int   `json:"comments"`
	Telegram      bool  `json:"telegram"`
	// Sessions are their signed-in devices now.
	Sessions   int        `json:"sessions"`
	LastSignIn *time.Time `json:"last_sign_in"`
	// ActiveSince is the Monday 26 weeks back; ActiveOn the days since then
	// they used Helpic, YYYY-MM-DD, oldest first.
	ActiveSince string   `json:"active_since"`
	ActiveOn    []string `json:"active_on"`
}

// User handles GET /api/v1/admin/users/{id}.
//
//	@Summary		An account
//	@Description	One account in full: its team, how much it has done (counts only) and the days it used Helpic over the last 26 weeks.
//	@Tags			admin
//	@Produce		json
//	@Param			id	path		string						true	"User ID"
//	@Success		200	{object}	map[string]AccountDetailDTO	"user"
//	@Failure		401	{object}	response.ErrorEnvelope		"Not signed in"
//	@Failure		403	{object}	response.ErrorEnvelope		"Not an admin, or two-factor is off"
//	@Failure		404	{object}	response.ErrorEnvelope		"No such account"
//	@Router			/admin/users/{id} [get]
func (h *Handler) User(w http.ResponseWriter, r *http.Request) {
	d, err := h.svc.AccountDetail(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, ErrNotFound) {
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "Resource not found")
		return
	}
	if err != nil {
		response.Internal(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]AccountDetailDTO{"user": toAccountDetailDTO(d)})
}

func toAccountDetailDTO(d AccountDetail) AccountDetailDTO {
	out := AccountDetailDTO{
		AccountDTO: toAccountDTO(d.Account), Locale: d.Locale, Currency: d.Currency, Partners: d.Partners,
		ReportsSent: d.ReportsSent, ReportsPaid: d.ReportsPaid, SecondsLogged: d.SecondsLogged, Comments: d.Comments,
		Telegram: d.Telegram, Sessions: d.Sessions, LastSignIn: d.LastSignIn,
		ActiveSince: date(d.ActiveSince), ActiveOn: make([]string, 0, len(d.ActiveOn)),
	}
	if d.Team != nil {
		out.Team = &TeamDTO{Name: d.Team.Name, Role: d.Team.Role, Members: d.Team.Members}
	}
	for _, day := range d.ActiveOn {
		out.ActiveOn = append(out.ActiveOn, date(day))
	}
	return out
}

// LogDTO is one of the server's warnings or errors.
type LogDTO struct {
	ID      int64     `json:"id"`
	At      time.Time `json:"at"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
	// Attrs are what was logged with it: for a request the method, route,
	// status, duration, IP and who made it; for a failure the error.
	Attrs map[string]any `json:"attrs"`
}

// LogsResponse is a page of entries, how many match in all, and the last
// day's counts.
type LogsResponse struct {
	Logs  []LogDTO `json:"logs"`
	Page  int      `json:"page"`
	Limit int      `json:"limit"`
	Total int      `json:"total"`
	// Warnings and Errors are counted over the last 24 hours, whatever the filter.
	Warnings int `json:"warnings"`
	Errors   int `json:"errors"`
}

// logsPage is a page's size; maxLogsPage the most one may ask for.
const (
	logsPage    = 50
	maxLogsPage = 200
)

// Logs handles GET /api/v1/admin/logs?level={level}&q={text}&page={n}&limit={n}.
//
//	@Summary		The server's warnings and errors
//	@Description	The last 30 days, the newest first: failed requests, panics, background jobs. A page at a time, with how many match in all. Requests show their route, never the path.
//	@Tags			admin
//	@Produce		json
//	@Param			level	query		string					false	"warn or error"
//	@Param			q		query		string					false	"Part of the message or of any attribute"
//	@Param			page	query		int						false	"The page, from 1"
//	@Param			limit	query		int						false	"Entries per page, 50 by default, at most 200"
//	@Success		200		{object}	LogsResponse			"OK"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid level, page or limit"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403		{object}	response.ErrorEnvelope	"Not an admin, or two-factor is off"
//	@Router			/admin/logs [get]
func (h *Handler) Logs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := applog.Filter{Query: q.Get("q"), Page: 1, Limit: logsPage}
	switch q.Get("level") {
	case "":
	case "warn":
		f.Level = applog.LevelWarn
	case "error":
		f.Level = applog.LevelError
	default:
		response.ValidationError(w, map[string]string{"level": "Choose warn or error"})
		return
	}
	if raw := q.Get("page"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			response.ValidationError(w, map[string]string{"page": "This page link is broken. Start from the first page."})
			return
		}
		f.Page = n
	}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxLogsPage {
			response.ValidationError(w, map[string]string{"limit": "Ask for 1 to 200 entries"})
			return
		}
		f.Limit = n
	}
	list, total, err := h.logs.List(r.Context(), f)
	if err != nil {
		response.Internal(w, err)
		return
	}
	sum, err := h.logs.Summary(r.Context(), time.Now().Add(-24*time.Hour))
	if err != nil {
		response.Internal(w, err)
		return
	}
	out := LogsResponse{Logs: make([]LogDTO, 0, len(list)), Page: f.Page, Limit: f.Limit, Total: total, Warnings: sum.Warnings, Errors: sum.Errors}
	for _, e := range list {
		out.Logs = append(out.Logs, LogDTO{ID: e.ID, At: e.At, Level: e.Level, Message: e.Message, Attrs: e.Attrs})
	}
	response.JSON(w, http.StatusOK, out)
}

// ActionRequest is the JSON body of an action on an account.
type ActionRequest struct {
	// Reason says why, for the record; optional, at most 500 characters.
	Reason string `json:"reason"`
}

// act handles POST /api/v1/admin/users/{id}/{action}: block, unblock,
// end-sessions and reset-two-factor.
//
//	@Summary		Act on an account
//	@Description	block stops the account signing in and signs it out; unblock lets it in again; end-sessions signs it out everywhere; reset-two-factor turns its two-factor sign-in off, signs it out and emails it. Each is recorded with the reason when it changes something; doing it again changes nothing. Admins' accounts can't be acted on.
//	@Tags			admin
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string						true	"User ID"
//	@Param			action	path		string						true	"block, unblock, end-sessions or reset-two-factor"
//	@Param			body	body		ActionRequest				true	"Request body"
//	@Success		200		{object}	map[string]AccountDetailDTO	"user, as it is now"
//	@Failure		400		{object}	response.ErrorEnvelope		"Reason too long"
//	@Failure		401		{object}	response.ErrorEnvelope		"Not signed in"
//	@Failure		403		{object}	response.ErrorEnvelope		"Not an admin, or two-factor is off"
//	@Failure		404		{object}	response.ErrorEnvelope		"No such account"
//	@Router			/admin/users/{id}/{action} [post]
func (h *Handler) act(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		adminID, ok := request.User(w, r)
		if !ok {
			return
		}
		var req ActionRequest
		if !request.Decode(w, r, &req) {
			return
		}
		reason := strings.TrimSpace(req.Reason)
		if utf8.RuneCountInString(reason) > MaxReason {
			response.ValidationError(w, map[string]string{"reason": "Keep the reason under 500 characters"})
			return
		}
		d, err := h.svc.Act(r.Context(), adminID, chi.URLParam(r, "id"), action, reason)
		if errors.Is(err, ErrNotFound) {
			response.Error(w, http.StatusNotFound, response.CodeNotFound, "Resource not found")
			return
		}
		if err != nil {
			response.Internal(w, err)
			return
		}
		response.JSON(w, http.StatusOK, map[string]AccountDetailDTO{"user": toAccountDetailDTO(d)})
	}
}

// ActionDTO is one thing an admin did to an account.
type ActionDTO struct {
	ID int64     `json:"id"`
	At time.Time `json:"at"`
	// AdminID and TargetID are null once the account is gone; the emails stay.
	AdminID    *string `json:"admin_id"`
	AdminEmail string  `json:"admin_email"`
	// Action is block, unblock, end_sessions or reset_two_factor.
	Action      string  `json:"action"`
	TargetID    *string `json:"target_id"`
	TargetEmail string  `json:"target_email"`
	Reason      string  `json:"reason"`
}

// ActionsResponse is a page of actions and how many match in all.
type ActionsResponse struct {
	Actions []ActionDTO `json:"actions"`
	Page    int         `json:"page"`
	Limit   int         `json:"limit"`
	Total   int         `json:"total"`
}

// Actions handles GET /api/v1/admin/actions?target={id}&page={n}&limit={n}.
//
//	@Summary		What admins did
//	@Description	Every action admins took on accounts, the newest first, a page at a time. Nothing is ever taken out of it.
//	@Tags			admin
//	@Produce		json
//	@Param			target	query		string					false	"Only the actions on this account"
//	@Param			page	query		int						false	"The page, from 1"
//	@Param			limit	query		int						false	"Actions per page, 25 by default, at most 100"
//	@Success		200		{object}	ActionsResponse			"OK"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid page or limit"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403		{object}	response.ErrorEnvelope	"Not an admin, or two-factor is off"
//	@Router			/admin/actions [get]
func (h *Handler) Actions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := ActionFilter{TargetID: q.Get("target"), Page: 1, Limit: accountsPage}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxAccountsPage {
			response.ValidationError(w, map[string]string{"limit": "Ask for 1 to 100 actions"})
			return
		}
		f.Limit = n
	}
	if raw := q.Get("page"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			response.ValidationError(w, map[string]string{"page": "This page link is broken. Start from the first page."})
			return
		}
		f.Page = n
	}
	list, total, err := h.svc.Actions(r.Context(), f)
	if err != nil {
		response.Internal(w, err)
		return
	}
	out := ActionsResponse{Actions: make([]ActionDTO, 0, len(list)), Page: f.Page, Limit: f.Limit, Total: total}
	for _, a := range list {
		out.Actions = append(out.Actions, ActionDTO{
			ID: a.ID, At: a.At, AdminID: a.AdminID, AdminEmail: a.AdminEmail, Action: a.Action,
			TargetID: a.TargetID, TargetEmail: a.TargetEmail, Reason: a.Reason,
		})
	}
	response.JSON(w, http.StatusOK, out)
}
