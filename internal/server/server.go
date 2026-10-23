// Package server wires the shared infrastructure and feature handlers into a
// single application. It contains assembly only — no business logic.
package server

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	httpSwagger "github.com/swaggo/http-swagger/v2"

	_ "github.com/pmaxis/helpic/docs" // the generated Swagger spec (make swagger)
	"github.com/pmaxis/helpic/internal/admin"
	"github.com/pmaxis/helpic/internal/alerts"
	"github.com/pmaxis/helpic/internal/applog"
	"github.com/pmaxis/helpic/internal/attachments"
	"github.com/pmaxis/helpic/internal/auth"
	"github.com/pmaxis/helpic/internal/board"
	"github.com/pmaxis/helpic/internal/clients"
	"github.com/pmaxis/helpic/internal/comments"
	"github.com/pmaxis/helpic/internal/config"
	"github.com/pmaxis/helpic/internal/jobs"
	"github.com/pmaxis/helpic/internal/mail"
	"github.com/pmaxis/helpic/internal/middleware"
	"github.com/pmaxis/helpic/internal/notifications"
	"github.com/pmaxis/helpic/internal/profile"
	"github.com/pmaxis/helpic/internal/projects"
	"github.com/pmaxis/helpic/internal/rates"
	"github.com/pmaxis/helpic/internal/reminders"
	"github.com/pmaxis/helpic/internal/reports"
	"github.com/pmaxis/helpic/internal/response"
	"github.com/pmaxis/helpic/internal/tasks"
	"github.com/pmaxis/helpic/internal/team"
	"github.com/pmaxis/helpic/internal/telegram"
	"github.com/pmaxis/helpic/internal/timelog"
	"github.com/pmaxis/helpic/internal/token"
	"github.com/pmaxis/helpic/internal/user"
)

// Version is the build, set at link time:
// -ldflags "-X github.com/pmaxis/helpic/internal/server.Version=…".
var Version = "dev"

// App is the assembled application.
type App struct {
	Handler http.Handler
	// background is the work that runs beside the HTTP server.
	background []func(ctx context.Context)
}

// Run does the application's background work until ctx ends.
func (a *App) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, run := range a.background {
		wg.Add(1)
		go func() {
			defer wg.Done()
			run(ctx)
		}()
	}
	wg.Wait()
}

// New builds the fully wired application.
func New(cfg config.Config, pool *pgxpool.Pool) (*App, error) {
	tokens := token.NewManager(cfg.JWTSecret, cfg.JWTExpiration)

	// --- outgoing email, sent in the background ------------------------------
	var sender mail.Sender = mail.Log{}
	// mailCheck tells the admin panel whether the mail server answers; nil
	// without one.
	var mailCheck func(context.Context) error
	if cfg.SMTPHost != "" {
		smtp, err := mail.NewSMTP(mail.SMTPConfig{
			Host: cfg.SMTPHost, Port: cfg.SMTPPort, Username: cfg.SMTPUsername, Password: cfg.SMTPPassword, From: cfg.MailFrom,
		})
		if err != nil {
			return nil, err
		}
		sender, mailCheck = smtp, smtp.Check
	}
	outbox := mail.NewOutbox(pool, sender)

	// --- auth + user ----------------------------------------------------------
	authRepo := auth.NewRepository(pool)
	authSvc := auth.NewService(authRepo, tokens, cfg.RefreshExpiration, outbox, cfg.AppURL)
	cookie := auth.CookieConfig{
		Name:        cfg.CookieName,
		Path:        cfg.CookiePath,
		RefreshName: cfg.RefreshCookieName,
		RefreshPath: cfg.RefreshCookiePath,
		Secure:      cfg.CookieSecure,
		SameSite:    cfg.CookieSameSite,
	}
	authHandler := auth.NewHandler(authSvc, cookie)

	// --- rate limits: slow down password guessing and sign-up spam -----------
	lim := newLimits(pool, cfg.RateLimit)
	if lim.loginFailures != nil {
		authHandler.LimitFailedLogins(lim.loginFailures, lim.emailFailures)
		authHandler.LimitResetRequests(lim.resetRequests, lim.emailResets)
	}

	// --- two-factor sign-in: authenticator secrets sealed with their own key -
	// Development may go without one; the key is then made from JWT_SECRET
	// (production refuses to start without TWO_FACTOR_KEY).
	twoFactorKey := cfg.TwoFactorKey
	if twoFactorKey == "" {
		twoFactorKey = cfg.JWTSecret
	}
	sealer, err := auth.NewSealer(twoFactorKey)
	if err != nil {
		return nil, err
	}
	authSvc.UseTwoFactor(sealer, lim.twoFactorCodes)

	// --- notifications: what the other side of shared work did -------------
	notifier := notifications.NewService(notifications.NewRepository(pool))
	notificationsHandler := notifications.NewHandler(notifier)
	emailer := notifications.NewEmailer(pool, outbox, cfg.AppURL)
	emailPrefsHandler := notifications.NewEmailHandler(emailer)

	// --- the same notifications in the user's Telegram chat -----------------
	var bot *telegram.Client
	if cfg.TelegramBotToken != "" {
		bot = telegram.NewClient(cfg.TelegramAPIURL, cfg.TelegramBotToken)
	}
	telegramSvc := telegram.NewService(telegram.NewRepository(pool), bot, cfg.AppURL)
	notifier.OnCreate(telegramSvc.Enqueue)
	telegramHandler := telegram.NewHandler(telegramSvc)

	userRepo := user.NewRepository(pool)
	userHandler := user.NewHandler(user.NewService(userRepo))
	profileSvc := profile.NewService(profile.NewRepository(pool), authSvc, outbox, cfg.AppURL)
	profileHandler := profile.NewHandler(profileSvc)

	// --- clients: invitations and freelancer–client links ---------------------
	clientsRepo := clients.NewRepository(pool)
	clientsHandler := clients.NewHandler(clients.NewService(clientsRepo, authSvc, notifier, outbox, cfg.AppURL), clientsRepo, cookie)

	// --- a client's company and its members -----------------------------------
	teamHandler := team.NewHandler(team.NewService(team.NewRepository(pool), authSvc, notifier, outbox, cfg.AppURL), cookie)

	// --- projects grouping tasks ---------------------------------------------
	projectsHandler := projects.NewHandler(projects.NewService(projects.NewRepository(pool), notifier))

	// --- tasks between a freelancer and a client, or a freelancer's own ------
	tasksSvc := tasks.NewService(tasks.NewRepository(pool), notifier)
	tasksHandler := tasks.NewHandler(tasksSvc)

	// --- files on tasks: mockups, specs, results -------------------------------
	uploads, err := attachments.NewStore(cfg.UploadsDir)
	if err != nil {
		return nil, err
	}
	attachmentsSvc := attachments.NewService(attachments.NewRepository(pool), uploads, tasksSvc, cfg.MaxUploadBytes, cfg.UploadQuotaBytes)
	tasksSvc.OnDelete(attachmentsSvc.TaskDeleted)
	attachmentsHandler := attachments.NewHandler(attachmentsSvc)

	// --- the discussion on a task ----------------------------------------------
	commentsHandler := comments.NewHandler(comments.NewService(comments.NewRepository(pool), tasksSvc))

	// --- time spent on tasks, which prices the ones billed by the hour --------
	timelogHandler := timelog.NewHandler(timelog.NewService(timelog.NewRepository(pool), tasksSvc, cfg.Location))

	// --- reports of work done, sent to clients ------------------------------
	zone := "UTC"
	if cfg.Location != nil {
		zone = cfg.Location.String()
	}
	reportsSvc := reports.NewService(reports.NewRepository(pool), notifier, tasksSvc, zone)
	reportsHandler := reports.NewHandler(reportsSvc)

	// --- reports' totals in hryvnias, at the NBU's official rate ------------
	var ratesSvc *rates.Service
	if cfg.NBUURL != "" {
		ratesSvc = rates.NewService(rates.NewRepository(pool), rates.NewClient(cfg.NBUURL), cfg.Location)
		reportsSvc.ShowInUAH(ratesSvc)
	}

	// --- reminders before and after due dates ---------------------------------
	remindersSvc := reminders.NewService(reminders.NewRepository(pool), notifier, cfg.Location, cfg.ReminderHour)

	// --- each user's kanban board over their tasks ----------------------------
	boardHandler := board.NewHandler(board.NewService(board.NewRepository(pool), tasksSvc))

	// --- the admin panel: how Helpic is used, for admin accounts -----------
	adminRepo := admin.NewRepository(pool)
	adminSvc := admin.NewService(adminRepo, cfg.Location)
	adminSvc.UseMail(outbox)
	adminSvc.UseOutbox(outbox)
	adminSvc.UseSignupLinks(authSvc)
	appLogs := applog.NewRepository(pool)
	adminHandler := admin.NewHandler(adminSvc, appLogs)
	adminHandler.UseCodes(authSvc)
	authSvc.OnSignIn(adminSvc.RecordSignIn)
	activity := admin.NewActivity(adminRepo, adminSvc)
	background := jobs.NewRegistry()
	var botCheck func(context.Context) error
	if bot != nil {
		botCheck = func(ctx context.Context) error {
			_, err := bot.GetMe(ctx)
			return err
		}
	}

	// --- alerts to the admins, through a bot of their own -------------------
	var watcher *alerts.Watcher
	var alertCheck func(context.Context) error
	if cfg.AlertBotToken != "" {
		alertBot := alerts.NewTelegram(cfg.TelegramAPIURL, cfg.AlertBotToken, cfg.AlertChatID)
		alertCheck = alertBot.Check
		checks := []alerts.Check{alerts.Database(pool.Ping), alerts.Jobs(background), alerts.Email(outbox), alerts.Errors(appLogs)}
		if mailCheck != nil {
			checks = append(checks, alerts.MailServer(mailCheck))
		}
		if botCheck != nil {
			checks = append(checks, alerts.Bot(botCheck))
		}
		watcher = alerts.NewWatcher(alertBot, cfg.AlertLocale, checks...)
		adminHandler.UseAlerts(watcher)
	}
	adminHandler.UseSystem(admin.System{
		Version: Version,
		Started: time.Now(),
		Probes: []admin.Probe{
			{Name: "database", Check: pool.Ping},
			{Name: "mail", Check: mailCheck},
			{Name: "telegram", Check: botCheck},
			{Name: "alerts", Check: alertCheck},
		},
		Jobs: background,
	})

	// --- router --------------------------------------------------------------
	authMiddleware := middleware.Auth(tokens, authSvc, cfg.CookieName)

	r := chi.NewRouter()
	r.Use(middleware.RealIP(cfg.TrustedProxies))
	r.Use(middleware.Logger)
	r.Use(middleware.Locale)
	r.Use(middleware.Recovery(!cfg.IsProduction()))
	r.Use(middleware.CORS(cfg.CORSOrigin))

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			response.Error(w, http.StatusServiceUnavailable, response.CodeInternal, "The database is not reachable")
			return
		}
		response.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// The Swagger UI and the spec it reads, generated from the handlers' comments.
	if cfg.SwaggerEnabled {
		r.Get("/api/docs", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/api/docs/index.html", http.StatusMovedPermanently)
		})
		r.Get("/api/docs/*", httpSwagger.Handler(httpSwagger.URL("/api/docs/doc.json")))
	}

	// The API is versioned in the path: a breaking change gets /api/v1/v2 next to it.
	r.Route("/api/v1", func(r chi.Router) {
		r.Route("/auth", func(r chi.Router) {
			r.With(byIP(lim.signIn), byIP(lim.signUp)).Post("/register", authHandler.Register)
			r.With(byIP(lim.signIn)).Post("/signups/confirm", authHandler.ConfirmSignup)
			r.With(byIP(lim.signIn)).Post("/login", authHandler.Login)
			r.With(byIP(lim.refresh)).Post("/refresh", authHandler.Refresh)
			r.With(byIP(lim.signIn)).Post("/password/forgot", authHandler.Forgot)
			r.With(byIP(lim.signIn)).Post("/password/reset", authHandler.Reset)
			r.Post("/logout", authHandler.Logout)
		})
		// Accepting an invitation signs in or makes an account.
		r.Group(func(r chi.Router) {
			r.Use(byIP(lim.signIn))
			clientsHandler.PublicRoutes(r)
			teamHandler.PublicRoutes(r)
			// Confirming a new email signs nobody in, but guesses at links count.
			profileHandler.PublicRoutes(r)
		})

		r.Group(func(r chi.Router) {
			r.Use(authMiddleware)
			r.Use(activity.Track)
			// The account itself: every role, admins included.
			r.Get("/me", userHandler.Me)
			profileHandler.Routes(r, byUser(lim.passwordChecks))
			authHandler.SessionRoutes(r)
			authHandler.TwoFactorRoutes(r, byUser(lim.passwordChecks))
			adminHandler.Routes(r)

			// The work: freelancers and clients only, never admins.
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireRole(user.RoleFreelancer, user.RoleClient))
				tasksHandler.Routes(r)
				attachmentsHandler.Routes(r)
				commentsHandler.Routes(r)
				timelogHandler.Routes(r)
				projectsHandler.Routes(r)
				boardHandler.Routes(r)
				notificationsHandler.Routes(r)
				emailPrefsHandler.Routes(r)
				telegramHandler.Routes(r)
				reportsHandler.Routes(r)

				r.Group(func(r chi.Router) {
					r.Use(middleware.RequireRole(user.RoleFreelancer))
					clientsHandler.FreelancerRoutes(r, byUser(lim.invites))
				})
				r.Group(func(r chi.Router) {
					r.Use(middleware.RequireRole(user.RoleClient))
					clientsHandler.ClientRoutes(r)
					teamHandler.ClientRoutes(r, byUser(lim.invites))
				})
			})
		})
	})

	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "Resource not found")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		response.Error(w, http.StatusMethodNotAllowed, response.CodeMethodNotAllowed, "Method not allowed")
	})

	// Expired sessions, connect links nobody opened, old reminders and old app logs.
	cleanup := func(ctx context.Context) error {
		return errors.Join(authSvc.Cleanup(ctx), profileSvc.Cleanup(ctx), telegramSvc.Cleanup(ctx), remindersSvc.Cleanup(ctx), outbox.Cleanup(ctx), appLogs.Cleanup(ctx))
	}

	run := []func(context.Context){
		telegramSvc.Run,
		func(ctx context.Context) { background.Every(ctx, "email", 5*time.Second, outbox.Run) },
		func(ctx context.Context) { background.Every(ctx, "notification emails", time.Minute, emailer.Run) },
		func(ctx context.Context) {
			background.Every(ctx, "deadline reminders", 15*time.Minute, remindersSvc.Run)
		},
		func(ctx context.Context) { background.Every(ctx, "cleanup", time.Hour, cleanup) },
		func(ctx context.Context) { background.Every(ctx, "rate limit sweep", time.Minute, lim.sweep) },
	}
	if watcher != nil {
		run = append(run, func(ctx context.Context) { background.Every(ctx, "alerts", 2*time.Minute, watcher.Run) })
	}
	if ratesSvc != nil {
		run = append(run, func(ctx context.Context) { background.Every(ctx, "exchange rates", time.Hour, ratesSvc.Run) })
	}
	return &App{Handler: r, background: run}, nil
}
