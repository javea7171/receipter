package http

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"time"

	loginflow "receipter/frontend/login"
	sessioncontext "receipter/frontend/shared/context"
	"receipter/infrastructure/audit"
	"receipter/infrastructure/cache"
	projectinfra "receipter/infrastructure/project"
	"receipter/infrastructure/rbac"
	sessioncookie "receipter/infrastructure/session"
	"receipter/infrastructure/sqlite"
	"receipter/models"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

//go:embed assets/*
var assets embed.FS

var ShutdownTimeout = 2 * time.Second

// Server bundles dependencies and route wiring.
type Server struct {
	Addr   string
	ln     net.Listener
	server *http.Server
	router *chi.Mux

	DB           *sqlite.DB
	SessionCache *cache.UserSessionCache
	UserCache    *cache.UserCache
	RbacCache    *cache.RbacRolesCache
	Rbac         *rbac.Rbac
	Audit        *audit.Service
}

// NewServer creates a new http server.
func NewServer(addr string, db *sqlite.DB, sessionCache *cache.UserSessionCache, userCache *cache.UserCache, r *rbac.Rbac, rbacCache *cache.RbacRolesCache, auditSvc *audit.Service) *Server {
	s := &Server{
		Addr:         addr,
		router:       chi.NewRouter(),
		DB:           db,
		SessionCache: sessionCache,
		UserCache:    userCache,
		RbacCache:    rbacCache,
		Rbac:         r,
		Audit:        auditSvc,
		server: &http.Server{
			MaxHeaderBytes: 1 << 20,
		},
	}

	// Secure headers first.
	s.router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("X-XSS-Protection", "1; mode=block")
			next.ServeHTTP(w, r)
		})
	})

	s.router.Use(middleware.Logger)
	s.router.Use(middleware.Recoverer)
	s.router.Use(middleware.RequestID)
	s.router.Use(middleware.Compress(5))
	s.router.Use(s.CSRFMiddleware)

	// Handle root requests - check auth status but don't require it.
	s.router.Get("/", func(w http.ResponseWriter, r *http.Request) {
		sessionCookie, err := r.Cookie(sessioncookie.CookieName)
		if err != nil || sessionCookie.Value == "" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		session, ok := s.resolveSession(r.Context(), sessionCookie.Value)
		if !ok || session.Expired() {
			http.SetCookie(w, sessioncookie.SessionCookie("", -1))
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		if session.User.Role == rbac.RoleClient {
			http.Redirect(w, r, "/tasker/pallets/sku-view", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/tasker/projects", http.StatusSeeOther)
	})

	s.router.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Serve assets from embedded FS.
	var assetsFS fs.FS = assets
	if sub, err := fs.Sub(assets, "assets"); err == nil {
		assetsFS = sub
	} else {
		slog.Error("assets subfs init failed; serving fallback fs", slog.Any("err", err))
	}
	s.router.Handle("/assets/*", http.StripPrefix("/assets/", http.FileServer(http.FS(assetsFS))))

	s.RegisterLoginRoutes()

	s.router.Group(func(r chi.Router) {
		r.Route("/tasker", func(r chi.Router) {
			r.Use(s.AuthenticateMiddleware)
			s.RegisterFrontendRoutes(r)
			s.RegisterAdminRoutes(r)
		})
	})

	s.server.Handler = s.router
	return s
}

// AuthenticateMiddleware loads session and applies RBAC checks.
func (s *Server) AuthenticateMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessionCookie, err := r.Cookie(sessioncookie.CookieName)
		if err != nil || sessionCookie.Value == "" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		sessionToken := sessionCookie.Value
		session, ok := s.resolveSession(r.Context(), sessionToken)
		if !ok {
			slog.Warn("session not found in cache", slog.String("method", r.Method), slog.String("path", r.URL.Path))
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		if session.Expired() {
			http.SetCookie(w, sessioncookie.SessionCookie("", -1))
			s.SessionCache.DeleteSessionBySessionToken(sessionToken)
			if err := DeleteSessionByID(s.DB, sessionToken); err != nil {
				slog.Error("cannot delete session from DB", slog.String("session_id", sessionToken), slog.Any("err", err))
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		s.ensureSessionActiveProject(r.Context(), &session)

		path := r.URL.Path
		skipRBAC := path == "/login" || path == "/logout"

		isAdmin := false
		for _, role := range session.UserRoles {
			if role == rbac.RoleAdmin {
				isAdmin = true
				break
			}
		}

		if isAdmin {
			session.ScreenPermissions = s.RbacCache.GetAllRouteNames()
			skipRBAC = true
		}

		if session.ScreenPermissions == nil {
			session.ScreenPermissions = s.buildRbacNamedRoutesMap(session.UserRoles)
			if session.ScreenPermissions == nil {
				session.ScreenPermissions = make(map[string]int)
			}
		}

		if !skipRBAC {
			if !s.RbacValidation(session.UserRoles, path, r.Method) {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
		}

		ctx := sessioncontext.NewContextWithSession(r.Context(), session)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) resolveSession(ctx context.Context, token string) (session models.Session, ok bool) {
	if cached, found := s.SessionCache.FindSessionBySessionToken(token); found {
		return cached, true
	}

	dbSession, err := loginflow.LoadSessionByToken(ctx, s.DB, token)
	if err != nil {
		if err != sql.ErrNoRows {
			slog.Error("load session from db failed", slog.String("session_id", token), slog.Any("err", err))
		}
		return session, false
	}

	s.SessionCache.AddSession(dbSession)
	s.UserCache.Add(dbSession.User.Username, dbSession.User)
	return dbSession, true
}

func (s *Server) ensureSessionActiveProject(ctx context.Context, session *models.Session) {
	if session == nil || session.ID == "" {
		return
	}
	if session.User.Role == rbac.RoleClient {
		projectID, err := projectinfra.ResolveClientActiveProjectID(ctx, s.DB, session.UserID, session.ActiveProjectID)
		if err != nil {
			slog.Error("resolve client session project failed", slog.String("session_id", session.ID), slog.Any("err", err))
			return
		}
		if sameProjectID(session.ActiveProjectID, projectID) {
			return
		}
		if err := projectinfra.SetSessionActiveProjectID(ctx, s.DB, session.ID, projectID); err != nil {
			slog.Error("set client session active project failed", slog.String("session_id", session.ID), slog.Any("err", err))
			return
		}
		session.ActiveProjectID = projectID
		if s.SessionCache != nil {
			s.SessionCache.AddSession(*session)
		}
		return
	}

	projectID, err := projectinfra.ResolveSessionActiveProjectID(ctx, s.DB, session.ActiveProjectID)
	if err != nil {
		slog.Error("resolve session active project failed", slog.String("session_id", session.ID), slog.Any("err", err))
		return
	}
	if sameProjectID(session.ActiveProjectID, projectID) {
		return
	}
	if err := projectinfra.SetSessionActiveProjectID(ctx, s.DB, session.ID, projectID); err != nil {
		slog.Error("set session active project failed", slog.String("session_id", session.ID), slog.Any("err", err))
		return
	}
	session.ActiveProjectID = projectID
	if s.SessionCache != nil {
		s.SessionCache.AddSession(*session)
	}
}

func sameProjectID(a, b *int64) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func hasRole(roles []string, role string) bool {
	for _, r := range roles {
		if r == role {
			return true
		}
	}
	return false
}

func (s *Server) buildRbacNamedRoutesMap(userRoles []string) map[string]int {
	perms := make(map[string]int)
	resources := s.RbacCache.GetRolesAndResources(userRoles)
	if len(resources) == 0 {
		return nil
	}
	for _, res := range resources {
		perms[res.UserResourceCode] = 1
	}
	return perms
}

func (s *Server) RbacValidation(userRoles []string, url, method string) bool {
	if len(userRoles) == 0 {
		return false
	}
	resources := s.RbacCache.GetRolesAndResources(userRoles)
	if len(resources) == 0 {
		return false
	}
	return rbac.ValidateResourceAccess(resources, url, method)
}

// Start starts the HTTP server.
func (s *Server) Start() error {
	var err error
	if s.ln, err = net.Listen("tcp", s.Addr); err != nil {
		return err
	}
	go s.server.Serve(s.ln)
	return nil
}

// Stop gracefully shuts down the server.
func (s *Server) Stop() error {
	if s.ln == nil {
		return fmt.Errorf("HTTP server has not been started or is already stopped")
	}
	ctx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
	defer cancel()
	if err := s.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to shutdown HTTP server: %v", err)
	}
	s.ln = nil
	return nil
}

// DeleteSessionByID deletes a session by its ID using a write transaction.
func DeleteSessionByID(db *sqlite.DB, sessionID string) error {
	return loginflow.DeleteSessionByToken(context.Background(), db, sessionID)
}
