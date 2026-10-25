package telegram

import (
	"context"
	"errors"
	"html"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pmaxis/helpic/internal/comments"
	"github.com/pmaxis/helpic/internal/i18n"
	"github.com/pmaxis/helpic/internal/notifications"
	"github.com/pmaxis/helpic/internal/reports"
	"github.com/pmaxis/helpic/internal/tasks"
	"github.com/pmaxis/helpic/internal/timelog"
	"github.com/pmaxis/helpic/internal/user"
)

const (
	// messageTTL is how long the bot remembers a message it sent: a reply to
	// an older one no longer finds its task.
	messageTTL = 30 * 24 * time.Hour
	// draftTTL is how long a task from a message waits for its sender to
	// pick who it's with.
	draftTTL = 24 * time.Hour
	// maxTargets caps the people offered for a task from a message; the
	// rest are in the app.
	maxTargets = 8
	// maxTitle is a task title's length, as the tasks package takes it.
	maxTitle = 200
)

// Button data, at most 64 bytes: what a press does, and on what.
const (
	cbStartTimer = "ts:" // + task ID
	cbStopTimer  = "tp"
	cbSendBack   = "tr:" // + task ID
	cbPay        = "rp:" // + report ID: asks first
	cbPayYes     = "ry:" // + report ID
	cbPayNo      = "rn:" // + report ID
	cbNewTask    = "nt:" // + draft ID + ":" + "p" for personal, or the target's place
	cbInProject  = "np:" // + draft ID + ":" + the project's place, or "-" for none
	cbDropDraft  = "nx:" // + draft ID
)

// taskActions is what the bot does with tasks. Implemented by *tasks.Service.
type taskActions interface {
	Get(ctx context.Context, userID, id string) (tasks.Task, error)
	Create(ctx context.Context, userID, role string, p tasks.Params) (tasks.Task, error)
	SetStatus(ctx context.Context, userID, role, id, status string) (tasks.Task, error)
}

// commentActions is implemented by *comments.Service.
type commentActions interface {
	Create(ctx context.Context, userID, taskID, body string) (comments.Comment, error)
}

// timerActions is implemented by *timelog.Service.
type timerActions interface {
	Running(ctx context.Context, userID string) (timelog.Entry, error)
	Start(ctx context.Context, userID, taskID string) (timelog.Entry, error)
	Stop(ctx context.Context, userID string) (timelog.Entry, error)
}

// reportActions is implemented by *reports.Service.
type reportActions interface {
	Get(ctx context.Context, userID, id string) (reports.Report, error)
	MarkPaid(ctx context.Context, clientID, id string) (reports.Report, error)
}

// Actions are the services the bot acts through, as the user it acts for:
// the same rules as in the app apply.
type Actions struct {
	Tasks    taskActions
	Comments commentActions
	Timer    timerActions
	Reports  reportActions
}

// UseActions lets the bot act, not only tell. Without it, it only tells.
func (s *Service) UseActions(a Actions) {
	s.act = &a
}

// translator words the bot's texts in one language.
type translator func(text string, args ...any) string

func translate(locale string) translator {
	return func(text string, args ...any) string { return i18n.T(locale, text, args...) }
}

// --- buttons under a notification ---------------------------------------------

// actionButtons are the buttons that act on what the notification is about,
// as far as its recipient may.
func (s *Service) actionButtons(ctx context.Context, l Link, n notifications.Notification) []Button {
	if s.act == nil {
		return nil
	}
	t := translate(l.Locale)
	var row []Button
	switch {
	case n.Kind == notifications.KindReportSent && n.ReportID != "":
		r, err := s.act.Reports.Get(ctx, l.UserID, n.ReportID)
		if err == nil && r.Status == reports.StatusSent && r.Client != nil && r.Client.ID == l.UserID {
			row = append(row, Button{Text: t("💸 Mark as paid"), CallbackData: cbPay + r.ID})
		}
	case n.TaskID != "" && n.Kind != notifications.KindTaskDeleted:
		task, err := s.act.Tasks.Get(ctx, l.UserID, n.TaskID)
		if err != nil {
			return nil
		}
		if task.Status != tasks.StatusDone && tasks.CanTrackTime(task, l.UserID) {
			row = append(row, Button{Text: t("▶️ Start timer"), CallbackData: cbStartTimer + task.ID})
		}
		if n.Kind == notifications.KindTaskStatus && task.Status == tasks.StatusDone &&
			tasks.CanTransition(task, l.Role, l.UserID, tasks.StatusInProgress) {
			row = append(row, Button{Text: t("↩️ Send back for more work"), CallbackData: cbSendBack + task.ID})
		}
	}
	return row
}

// keyboard puts the action buttons over the one that opens the app.
func keyboard(actions []Button, open *Button) Keyboard {
	var k Keyboard
	if len(actions) > 0 {
		k = append(k, actions)
	}
	if open != nil {
		k = append(k, []Button{*open})
	}
	return k
}

// --- messages to the bot --------------------------------------------------------

// respond answers a message that isn't a command, from a connected chat: a
// reply to a notification about a task comments on it; anything else becomes
// a task once the sender picks who it's with.
func (s *Service) respond(ctx context.Context, l Link, m Message) {
	t := translate(l.Locale)
	text := strings.TrimSpace(m.Text)
	if text == "" {
		text = strings.TrimSpace(m.Caption)
	}
	if text == "" {
		s.reply(ctx, m.Chat.ID, t("I only read text: send or forward a message to make a task of it."))
		return
	}
	if m.ReplyTo != nil {
		taskID, err := s.repo.TaskOf(ctx, m.Chat.ID, m.ReplyTo.MessageID, l.UserID)
		if err != nil {
			slog.Error("telegram: find replied task", "error", err)
		}
		if taskID != "" {
			s.comment(ctx, l, m.Chat.ID, taskID, text)
			return
		}
	}
	s.offerTask(ctx, l, m, text)
}

// comment posts the text as the user's comment on the task.
func (s *Service) comment(ctx context.Context, l Link, chatID int64, taskID, body string) {
	t := translate(l.Locale)
	if utf8.RuneCountInString(body) > 10000 {
		s.reply(ctx, chatID, t("The comment is too long"))
		return
	}
	if _, err := s.act.Comments.Create(ctx, l.UserID, taskID, body); err != nil {
		s.reply(ctx, chatID, s.failure(t, err))
		return
	}
	title := ""
	if task, err := s.act.Tasks.Get(ctx, l.UserID, taskID); err == nil {
		title = task.Title
	}
	s.sendTracked(ctx, l, chatID, t("💬 Commented on “%s”.", html.EscapeString(title)), keyboard(nil, s.openTask(taskID, l.Locale)), taskID)
}

// offerTask keeps the message as a draft task and asks who it's with.
func (s *Service) offerTask(ctx context.Context, l Link, m Message, text string) {
	t := translate(l.Locale)
	title, _, _ := strings.Cut(text, "\n")
	title = strings.TrimSpace(title)
	if utf8.RuneCountInString(title) > maxTitle {
		title = strings.TrimSpace(string([]rune(title)[:maxTitle-1])) + "…"
	}
	description := ""
	if title != text {
		description = text
	}
	if m.ForwardOrigin != nil {
		if from := m.ForwardOrigin.Name(); from != "" {
			description = strings.TrimSpace(description + "\n\n" + t("Forwarded from %s", from))
		}
	}
	targets, err := s.repo.Counterparts(ctx, l.UserID, l.Role)
	if err != nil {
		slog.Error("telegram: list counterparts", "error", err)
		s.reply(ctx, m.Chat.ID, t("Something went wrong. Please try again."))
		return
	}
	if len(targets) > maxTargets {
		targets = targets[:maxTargets]
	}
	id, err := s.repo.NewDraft(ctx, l.UserID, Draft{Title: title, Description: description, Targets: targets})
	if err != nil {
		slog.Error("telegram: new draft", "error", err)
		s.reply(ctx, m.Chat.ID, t("Something went wrong. Please try again."))
		return
	}
	draft := strconv.FormatInt(id, 10)
	k := Keyboard{{{Text: t("👤 Personal"), CallbackData: cbNewTask + draft + ":p"}}}
	for i, target := range targets {
		k = append(k, []Button{{Text: target.Name, CallbackData: cbNewTask + draft + ":" + strconv.Itoa(i)}})
	}
	k = append(k, []Button{{Text: t("Cancel"), CallbackData: cbDropDraft + draft}})
	question := t("Make a task “%s”? Choose who it's with.", html.EscapeString(title))
	s.sendTracked(ctx, l, m.Chat.ID, question, k, "")
}

// showTimer tells what the user's timer is on, with a button to stop it.
func (s *Service) showTimer(ctx context.Context, l Link, chatID int64) {
	t := translate(l.Locale)
	e, err := s.act.Timer.Running(ctx, l.UserID)
	if errors.Is(err, timelog.ErrNoTimer) {
		s.reply(ctx, chatID, t("No timer is running. Start one with the button under a task's notification, or in Helpic."))
		return
	}
	if err != nil {
		s.reply(ctx, chatID, s.failure(t, err))
		return
	}
	s.sendTracked(ctx, l, chatID, t("⏱ The timer is on “%s”: %s so far.", html.EscapeString(e.TaskTitle), duration(t, elapsed(e))),
		Keyboard{{{Text: t("⏹ Stop timer"), CallbackData: cbStopTimer}}}, e.TaskID)
}

// --- button presses -------------------------------------------------------------

// press does what a button under one of the bot's messages says, as the
// user the message was for, then answers the press with a short note.
func (s *Service) press(ctx context.Context, q CallbackQuery) {
	if q.Message == nil {
		s.answer(ctx, q.ID, "")
		return
	}
	chatID, messageID := q.Message.Chat.ID, q.Message.MessageID
	locale := i18n.Normalize(q.From.LanguageCode)
	l, err := s.repo.ByChat(ctx, chatID, messageID)
	if err != nil || s.act == nil {
		if err != nil && !errors.Is(err, ErrNotConnected) {
			slog.Error("telegram: load chat", "error", err)
		}
		s.answer(ctx, q.ID, i18n.T(locale, "This chat isn't connected to Helpic."))
		return
	}
	t := translate(l.Locale)
	buttons := q.Message.Buttons()
	data := q.Data

	switch {
	case strings.HasPrefix(data, cbStartTimer):
		taskID := strings.TrimPrefix(data, cbStartTimer)
		e, err := s.act.Timer.Start(ctx, l.UserID, taskID)
		if err != nil {
			s.answer(ctx, q.ID, s.failure(t, err))
			return
		}
		s.answer(ctx, q.ID, t("▶️ Timer started on “%s”", e.TaskTitle))
		s.setButtons(ctx, chatID, messageID, buttons.swap(data, &Button{Text: t("⏹ Stop timer"), CallbackData: cbStopTimer}))

	case data == cbStopTimer:
		e, err := s.act.Timer.Stop(ctx, l.UserID)
		if errors.Is(err, timelog.ErrNoTimer) {
			s.answer(ctx, q.ID, t("No timer is running."))
			s.setButtons(ctx, chatID, messageID, buttons.swap(data, nil))
			return
		}
		if err != nil {
			s.answer(ctx, q.ID, s.failure(t, err))
			return
		}
		s.answer(ctx, q.ID, t("⏹ %s on “%s”", duration(t, e.Seconds), e.TaskTitle))
		s.setButtons(ctx, chatID, messageID, buttons.swap(data, &Button{Text: t("▶️ Start timer"), CallbackData: cbStartTimer + e.TaskID}))

	case strings.HasPrefix(data, cbSendBack):
		task, err := s.act.Tasks.SetStatus(ctx, l.UserID, l.Role, strings.TrimPrefix(data, cbSendBack), tasks.StatusInProgress)
		if err != nil {
			s.answer(ctx, q.ID, s.failure(t, err))
			return
		}
		s.answer(ctx, q.ID, t("↩️ “%s” is back in progress", task.Title))
		s.setButtons(ctx, chatID, messageID, buttons.swap(data, nil))

	case strings.HasPrefix(data, cbPay):
		id := strings.TrimPrefix(data, cbPay)
		s.answer(ctx, q.ID, "")
		s.setButtons(ctx, chatID, messageID, buttons.swapRow(data, []Button{
			{Text: t("✅ Yes, it's paid"), CallbackData: cbPayYes + id},
			{Text: t("Cancel"), CallbackData: cbPayNo + id},
		}))

	case strings.HasPrefix(data, cbPayNo):
		id := strings.TrimPrefix(data, cbPayNo)
		s.answer(ctx, q.ID, "")
		s.setButtons(ctx, chatID, messageID, buttons.swapRow(data, []Button{{Text: t("💸 Mark as paid"), CallbackData: cbPay + id}}))

	case strings.HasPrefix(data, cbPayYes):
		r, err := s.act.Reports.MarkPaid(ctx, l.UserID, strings.TrimPrefix(data, cbPayYes))
		if err != nil {
			s.answer(ctx, q.ID, s.failure(t, err))
			if errors.Is(err, reports.ErrNotSent) {
				s.setButtons(ctx, chatID, messageID, buttons.swapRow(data, nil))
			}
			return
		}
		s.answer(ctx, q.ID, t("💸 “%s” is marked paid", r.Title))
		s.setButtons(ctx, chatID, messageID, buttons.swapRow(data, nil))

	case strings.HasPrefix(data, cbNewTask):
		s.chooseFor(ctx, l, q, strings.TrimPrefix(data, cbNewTask))

	case strings.HasPrefix(data, cbInProject):
		s.inProject(ctx, l, q, strings.TrimPrefix(data, cbInProject))

	case strings.HasPrefix(data, cbDropDraft):
		id, _ := strconv.ParseInt(strings.TrimPrefix(data, cbDropDraft), 10, 64)
		if _, _, err := s.repo.TakeDraft(ctx, l.UserID, id); err != nil {
			slog.Error("telegram: drop draft", "error", err)
		}
		s.answer(ctx, q.ID, "")
		s.edit(ctx, chatID, messageID, t("No task made."), nil)

	default:
		s.answer(ctx, q.ID, "")
	}
}

// chooseFor takes who the drafted task is with ("p" for personal, or a
// target's place) and, when that pair has projects, asks which one; without
// any, it makes the task at once.
func (s *Service) chooseFor(ctx context.Context, l Link, q CallbackQuery, arg string) {
	t := translate(l.Locale)
	chatID, messageID := q.Message.Chat.ID, q.Message.MessageID
	rawID, choice, _ := strings.Cut(arg, ":")
	id, _ := strconv.ParseInt(rawID, 10, 64)
	d, ok, err := s.repo.Draft(ctx, l.UserID, id)
	if err != nil {
		slog.Error("telegram: load draft", "error", err)
		s.answer(ctx, q.ID, t("Something went wrong. Please try again."))
		return
	}
	if !ok {
		s.answer(ctx, q.ID, t("This task was already made, or cancelled."))
		s.setButtons(ctx, chatID, messageID, nil)
		return
	}
	target, ok := d.target(choice)
	if !ok {
		s.answer(ctx, q.ID, "")
		return
	}
	projects, err := s.projectsFor(ctx, l, target)
	if err != nil {
		slog.Error("telegram: list draft projects", "error", err)
		s.answer(ctx, q.ID, t("Something went wrong. Please try again."))
		return
	}
	if len(projects) == 0 {
		s.makeTask(ctx, l, q, id, choice, "")
		return
	}
	if len(projects) > maxTargets {
		projects = projects[:maxTargets]
	}
	if err := s.repo.ChooseFor(ctx, l.UserID, id, choice, projects); err != nil {
		slog.Error("telegram: choose for draft", "error", err)
		s.answer(ctx, q.ID, t("Something went wrong. Please try again."))
		return
	}
	s.answer(ctx, q.ID, "")
	draft := strconv.FormatInt(id, 10)
	k := Keyboard{}
	for i, p := range projects {
		k = append(k, []Button{{Text: "📁 " + p.Name, CallbackData: cbInProject + draft + ":" + strconv.Itoa(i)}})
	}
	k = append(k, []Button{{Text: t("No project"), CallbackData: cbInProject + draft + ":-"}})
	k = append(k, []Button{{Text: t("Cancel"), CallbackData: cbDropDraft + draft}})
	s.edit(ctx, chatID, messageID, t("Task “%s” with %s. Which project is it in?",
		html.EscapeString(d.Title), html.EscapeString(s.withName(t, target))), k)
}

// inProject makes the drafted task in the project the press chose ("-" for none).
func (s *Service) inProject(ctx context.Context, l Link, q CallbackQuery, arg string) {
	rawID, pick, _ := strings.Cut(arg, ":")
	id, _ := strconv.ParseInt(rawID, 10, 64)
	s.makeTask(ctx, l, q, id, "", pick)
}

// target reads a choice — "p" for personal, or a target's place — as who
// the task is with: nil for a personal one.
func (d Draft) target(choice string) (*Target, bool) {
	if choice == "p" {
		return nil, true
	}
	i, err := strconv.Atoi(choice)
	if err != nil || i < 0 || i >= len(d.Targets) {
		return nil, false
	}
	return &d.Targets[i], true
}

func (s *Service) withName(t translator, target *Target) string {
	if target == nil {
		return t("personal")
	}
	return target.Name
}

// projectsFor are the projects a task with this target may go into. A
// client's own task is in no project.
func (s *Service) projectsFor(ctx context.Context, l Link, target *Target) ([]Target, error) {
	switch {
	case l.Role == user.RoleClient && target == nil:
		return nil, nil
	case l.Role == user.RoleClient:
		client, err := s.repo.ActingClient(ctx, l.UserID)
		if err != nil {
			return nil, err
		}
		return s.repo.Projects(ctx, l.UserID, target.ID, client)
	case target == nil:
		return s.repo.Projects(ctx, l.UserID, l.UserID, "")
	default:
		return s.repo.Projects(ctx, l.UserID, l.UserID, target.ID)
	}
}

// makeTask makes the drafted task: with the target chosen now (choice) or
// earlier, in the project at this place of the ones offered ("-" or "" for
// none).
func (s *Service) makeTask(ctx context.Context, l Link, q CallbackQuery, id int64, choice, pick string) {
	t := translate(l.Locale)
	chatID, messageID := q.Message.Chat.ID, q.Message.MessageID
	d, ok, err := s.repo.TakeDraft(ctx, l.UserID, id)
	if err != nil {
		slog.Error("telegram: take draft", "error", err)
		s.answer(ctx, q.ID, t("Something went wrong. Please try again."))
		return
	}
	if !ok {
		s.answer(ctx, q.ID, t("This task was already made, or cancelled."))
		s.setButtons(ctx, chatID, messageID, nil)
		return
	}
	if choice == "" {
		choice = d.Choice
	}
	target, ok := d.target(choice)
	if !ok {
		s.answer(ctx, q.ID, "")
		return
	}
	p := tasks.Params{Title: d.Title, Description: d.Description, Priority: tasks.PriorityNone}
	with := s.withName(t, target)
	if i, err := strconv.Atoi(pick); err == nil && i >= 0 && i < len(d.Projects) {
		project := d.Projects[i]
		p.ProjectID = &project.ID
		with += " · " + project.Name
	}
	switch {
	case target == nil && p.ProjectID == nil:
		p.Personal = true
	case target == nil:
		// A freelancer's own project: the task is personal through it.
	case l.Role == user.RoleClient:
		p.FreelancerID = target.ID
	default:
		p.ClientID = target.ID
	}
	task, err := s.act.Tasks.Create(ctx, l.UserID, l.Role, p)
	if err != nil {
		s.answer(ctx, q.ID, s.failure(t, err))
		return
	}
	s.answer(ctx, q.ID, "")
	var actions []Button
	if tasks.CanTrackTime(task, l.UserID) {
		actions = append(actions, Button{Text: t("▶️ Start timer"), CallbackData: cbStartTimer + task.ID})
	}
	s.edit(ctx, chatID, messageID, t("✅ Task “%s” made: %s.", html.EscapeString(task.Title), html.EscapeString(with)),
		keyboard(actions, s.openTask(task.ID, l.Locale)))
	if err := s.repo.Remember(ctx, chatID, messageID, l.UserID, task.ID); err != nil {
		slog.Error("telegram: remember message", "error", err)
	}
}

// failure words why an action didn't happen.
func (s *Service) failure(t translator, err error) string {
	switch {
	case errors.Is(err, tasks.ErrNotFound), errors.Is(err, comments.ErrNotFound):
		return t("The task is gone, or isn't yours to see any more.")
	case errors.Is(err, tasks.ErrArchived):
		return t("The task is archived: nothing on it changes any more.")
	case errors.Is(err, timelog.ErrBilled):
		return t("The task is on a sent report: its time is billed.")
	case errors.Is(err, timelog.ErrNotAllowed):
		return t("Only the task's freelancer tracks time on it.")
	case errors.Is(err, tasks.ErrTransition):
		return t("The task has moved on since: open it in Helpic.")
	case errors.Is(err, tasks.ErrNotLinked):
		return t("You don't work with them any more.")
	case errors.Is(err, reports.ErrNotFound):
		return t("The report is gone.")
	case errors.Is(err, reports.ErrNotSent):
		return t("The report is already marked paid.")
	case errors.Is(err, reports.ErrNotClient):
		return t("Only the report's client marks it paid.")
	default:
		slog.Error("telegram: action", "error", err)
		return t("Something went wrong. Please try again.")
	}
}

// --- sending ---------------------------------------------------------------------

// sendTracked sends a message and remembers it was for the user, about the task.
func (s *Service) sendTracked(ctx context.Context, l Link, chatID int64, text string, k Keyboard, taskID string) {
	sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	id, err := s.bot.Send(sendCtx, chatID, text, k)
	if err != nil {
		slog.Error("telegram: send", "error", err)
		return
	}
	if err := s.repo.Remember(ctx, chatID, id, l.UserID, taskID); err != nil {
		slog.Error("telegram: remember message", "error", err)
	}
}

func (s *Service) edit(ctx context.Context, chatID, messageID int64, text string, k Keyboard) {
	sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	if err := s.bot.EditText(sendCtx, chatID, messageID, text, k); err != nil {
		slog.Error("telegram: edit message", "error", err)
	}
}

func (s *Service) setButtons(ctx context.Context, chatID, messageID int64, k Keyboard) {
	sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	if err := s.bot.EditButtons(sendCtx, chatID, messageID, k); err != nil {
		slog.Error("telegram: edit buttons", "error", err)
	}
}

func (s *Service) answer(ctx context.Context, id, text string) {
	sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	if err := s.bot.AnswerCallback(sendCtx, id, text); err != nil {
		slog.Error("telegram: answer button", "error", err)
	}
}

// openTask is the button that opens the task in the app, nil where Telegram
// takes no link to it.
func (s *Service) openTask(taskID, locale string) *Button {
	return s.button(notifications.Notification{TaskID: taskID}, locale)
}

// --- keyboards ---------------------------------------------------------------------

// swap replaces the button with this data by another, or drops it for nil;
// a row left empty goes.
func (k Keyboard) swap(data string, with *Button) Keyboard {
	out := Keyboard{}
	for _, row := range k {
		next := []Button{}
		for _, b := range row {
			switch {
			case b.CallbackData != data:
				next = append(next, b)
			case with != nil:
				next = append(next, *with)
			}
		}
		if len(next) > 0 {
			out = append(out, next)
		}
	}
	return out
}

// swapRow replaces the row holding the button with this data by another, or
// drops it for nil.
func (k Keyboard) swapRow(data string, with []Button) Keyboard {
	out := Keyboard{}
	for _, row := range k {
		has := false
		for _, b := range row {
			has = has || b.CallbackData == data
		}
		switch {
		case !has:
			out = append(out, row)
		case len(with) > 0:
			out = append(out, with)
		}
	}
	return out
}

// --- time ---------------------------------------------------------------------------

// elapsed is a running entry's time so far.
func elapsed(e timelog.Entry) int64 {
	if e.RunningSince == nil {
		return e.Seconds
	}
	return e.Seconds + int64(time.Since(*e.RunningSince).Seconds())
}

// duration words seconds as "1 h 20 min", "45 min".
func duration(t translator, seconds int64) string {
	m := (seconds + 30) / 60
	switch {
	case m < 60:
		return t("%d min", m)
	case m%60 == 0:
		return t("%d h", m/60)
	default:
		return t("%d h %d min", m/60, m%60)
	}
}
