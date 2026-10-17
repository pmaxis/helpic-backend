// Package server wires the shared infrastructure and feature handlers into a
// single application. It contains assembly only — no business logic.
package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	httpSwagger "github.com/swaggo/http-swagger/v2"

	_ "github.com/pmaxis/helpic/docs" // the generated Swagger spec (make swagger)
	"github.com/pmaxis/helpic/internal/attachments"
	"github.com/pmaxis/helpic/internal/auth"
	"github.com/pmaxis/helpic/internal/board"
	"github.com/pmaxis/helpic/internal/clients"
	"github.com/pmaxis/helpic/internal/comments"
	"github.com/pmaxis/helpic/internal/config"
	"github.com/pmaxis/helpic/internal/middleware"
	"github.com/pmaxis/helpic/internal/notifications"
	"github.com/pmaxis/helpic/internal/profile"
	"github.com/pmaxis/helpic/internal/projects"
	"github.com/pmaxis/helpic/internal/reports"
	"github.com/pmaxis/helpic/internal/response"
	"github.com/pmaxis/helpic/internal/tasks"
	"github.com/pmaxis/helpic/internal/team"
	"github.com/pmaxis/helpic/internal/token"
	"github.com/pmaxis/helpic/internal/user"
)

// App is the assembled application.
type App struct {
	Handler http.Handler
}

// New builds the fully wired application.
func New(cfg config.Config, pool *pgxpool.Pool) (*App, error) {
	tokens := token.NewManager(cfg.JWTSecret, cfg.JWTExpiration)

	// --- auth + user ----------------------------------------------------------
	authRepo := auth.NewRepository(pool)
	authSvc := auth.NewService(authRepo, tokens, cfg.RefreshExpiration)
	cookie := auth.CookieConfig{
		Name:        cfg.CookieName,
		Path:        cfg.CookiePath,
		RefreshName: cfg.RefreshCookieName,
		RefreshPath: cfg.RefreshCookiePath,
		Secure:      cfg.CookieSecure,
		SameSite:    cfg.CookieSameSite,
	}
	authHandler := auth.NewHandler(authSvc, cookie)

	// --- notifications: what the other side of shared work did -------------
	notifier := notifications.NewService(notifications.NewRepository(pool))
	notificationsHandler := notifications.NewHandler(notifier)

	userRepo := user.NewRepository(pool)
	userHandler := user.NewHandler(user.NewService(userRepo))
	profileHandler := profile.NewHandler(profile.NewService(profile.NewRepository(pool), authSvc))

	// --- clients: invitations and freelancer–client links ---------------------
	clientsRepo := clients.NewRepository(pool)
	clientsHandler := clients.NewHandler(clients.NewService(clientsRepo, authSvc, notifier), clientsRepo, cookie)

	// --- a client's company and its members -----------------------------------
	teamHandler := team.NewHandler(team.NewService(team.NewRepository(pool), authSvc, notifier), cookie)

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
	attachmentsSvc := attachments.NewService(attachments.NewRepository(pool), uploads, tasksSvc, cfg.MaxUploadBytes)
	tasksSvc.OnDelete(attachmentsSvc.TaskDeleted)
	attachmentsHandler := attachments.NewHandler(attachmentsSvc)

	// --- the discussion on a task ----------------------------------------------
	commentsHandler := comments.NewHandler(comments.NewService(comments.NewRepository(pool), tasksSvc))

	// --- reports of work done, sent to clients ------------------------------
	reportsHandler := reports.NewHandler(reports.NewService(reports.NewRepository(pool), notifier, tasksSvc), reports.NewPDFRenderer(cfg.ChromePath))

	// --- each user's kanban board over their tasks ----------------------------
	boardHandler := board.NewHandler(board.NewService(board.NewRepository(pool), tasksSvc))

	// --- router --------------------------------------------------------------
	authMiddleware := middleware.Auth(tokens, cfg.CookieName)

	r := chi.NewRouter()
	r.Use(middleware.Recovery(!cfg.IsProduction()))
	r.Use(middleware.CORS(cfg.CORSOrigin))

	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
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
			r.Post("/register", authHandler.Register)
			r.Post("/login", authHandler.Login)
			r.Post("/refresh", authHandler.Refresh)
			r.Post("/logout", authHandler.Logout)
		})
		clientsHandler.PublicRoutes(r)
		teamHandler.PublicRoutes(r)

		r.Group(func(r chi.Router) {
			r.Use(authMiddleware)
			r.Get("/me", userHandler.Me)
			profileHandler.Routes(r)
			authHandler.SessionRoutes(r)
			tasksHandler.Routes(r)
			attachmentsHandler.Routes(r)
			commentsHandler.Routes(r)
			projectsHandler.Routes(r)
			boardHandler.Routes(r)
			notificationsHandler.Routes(r)
			reportsHandler.Routes(r)

			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireRole(user.RoleFreelancer))
				clientsHandler.FreelancerRoutes(r)
			})
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireRole(user.RoleClient))
				clientsHandler.ClientRoutes(r)
				teamHandler.ClientRoutes(r)
			})
		})
	})

	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "Resource not found")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		response.Error(w, http.StatusMethodNotAllowed, response.CodeMethodNotAllowed, "Method not allowed")
	})

	return &App{Handler: r}, nil
}
