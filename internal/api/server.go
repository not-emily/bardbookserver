package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/not-emily/bardbookserver/internal/auth"
	"github.com/not-emily/bardbookserver/internal/config"
	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/jobs"
	"github.com/not-emily/bardbookserver/internal/media"
	"github.com/not-emily/bardbookserver/internal/metadata/provider"
	"github.com/not-emily/bardbookserver/internal/scan"
	"github.com/not-emily/bardbookserver/internal/space"
	"github.com/not-emily/bardbookserver/internal/upload"
	"github.com/not-emily/bardbookserver/internal/web"
)

const shutdownGrace = 15 * time.Second

type Server struct {
	cfg       *config.Config
	db        *db.DB
	store     *auth.Store
	space     *space.Store
	covers    *media.Cache
	providers *provider.Registry
	scanner   *scan.Scanner
	uploads   *upload.Store
	queue     *jobs.Queue
	templates *web.Templates
	static    http.Handler
	logger    *slog.Logger
	version   string

	loginLimiter *auth.Limiter
	pinLimiter   *auth.Limiter
}

func NewServer(cfg *config.Config, database *db.DB, logger *slog.Logger, version string) (*Server, error) {
	covers := media.NewCache(cfg.CoverCacheDir())
	providers := provider.New(cfg.Metadata, database.DB, logger)

	templates, err := web.Parse()
	if err != nil {
		return nil, err
	}
	static, err := web.Static()
	if err != nil {
		return nil, err
	}

	return &Server{
		cfg:       cfg,
		db:        database,
		store:     auth.NewStore(database.DB),
		space:     space.NewStore(database),
		covers:    covers,
		providers: providers,
		// Fix Match writes metadata into the index, which is the scanner's job
		// whether a file change or a person triggered it.
		scanner: scan.NewScanner(database.DB, covers, providers, logger),
		uploads: upload.NewStore(database.DB, upload.Limits{
			MaxBytes:   cfg.Upload.MaxBytes(),
			ChunkBytes: cfg.Upload.ChunkBytes(),
		}),
		queue:        jobs.NewQueue(database.DB),
		templates:    templates,
		static:       static,
		logger:       logger,
		version:      version,
		loginLimiter: auth.NewLimiter(loginAttempts, loginWindow),
		pinLimiter:   auth.NewLimiter(pinAttempts, pinWindow),
	}, nil
}

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)

	// A public route stays on the outer mux: ServeMux prefers the more
	// specific pattern, so it wins over the /api/v1/ catch-all below.
	authed := http.NewServeMux()
	for _, r := range s.apiRoutes() {
		pattern := r.Method + " " + r.Path
		if r.Access == public {
			mux.HandleFunc(pattern, r.Handler)
			continue
		}
		authed.HandleFunc(pattern, r.Handler)
	}
	mux.Handle("/api/v1/", s.requireAuth(authed))

	mux.Handle("GET /static/", s.static)
	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /login", s.handleWebLogin)
	mux.HandleFunc("GET /setup", s.handleSetupPage)
	mux.HandleFunc("POST /setup", s.handleSetupSubmit)

	pages := http.NewServeMux()
	pages.HandleFunc("GET /{$}", s.handleDashboard)
	pages.HandleFunc("POST /logout", s.handleWebLogout)
	pages.HandleFunc("GET /setup/source", s.handleSetupSourcePage)
	pages.HandleFunc("POST /setup/source", s.handleSetupSourceSubmit)
	pages.HandleFunc("GET /setup/scan", s.handleSetupScanPage)
	pages.HandleFunc("POST /setup/scan", s.handleSetupScanStart)
	pages.HandleFunc("GET /sources", s.handleSourcesPage)
	pages.HandleFunc("GET /sources/new", s.handleSetupSourcePage)
	pages.HandleFunc("POST /sources/new", s.handleSetupSourceSubmit)
	pages.HandleFunc("GET /sources/{id}/edit", s.handleSourceEditPage)
	pages.HandleFunc("POST /sources/{id}", s.handleSourceEditSubmit)
	pages.HandleFunc("POST /sources/{id}/scan", s.handleSourceScan)
	pages.HandleFunc("GET /users", s.handleUsersPage)
	pages.HandleFunc("POST /users", s.handleUserCreate)
	pages.HandleFunc("POST /users/{id}", s.handleUserUpdate)
	pages.HandleFunc("GET /devices", s.handleDevicesPage)
	pages.HandleFunc("POST /devices/{id}/revoke", s.handleDeviceRevokeForm)
	pages.HandleFunc("GET /jobs", s.handleJobsPage)
	pages.HandleFunc("GET /ages", s.handleRatingQueuePage)
	pages.HandleFunc("POST /ages", s.handleConfirmAges)
	pages.HandleFunc("GET /genres", s.handleGenresPage)
	pages.HandleFunc("POST /genres", s.handleMapSubject)
	pages.HandleFunc("GET /matches", s.handleMatchesPage)
	pages.HandleFunc("GET /books/{id}/match", s.handleBookMatchPage)
	pages.HandleFunc("POST /books/{id}/match", s.handleBookMatchApply)
	pages.HandleFunc("GET /upload", s.handleUploadPage)
	pages.HandleFunc("GET /diagnostics", s.handleDiagnosticsPage)
	mux.Handle("/", s.requireWebAuth(pages))

	resolver := proxyResolver{trusted: s.cfg.TrustedPrefixes()}

	var h http.Handler = mux
	h = requestLogger(s.logger)(h)
	h = recoverer(s.logger)(h)
	h = resolver.middleware(h)
	return h
}

// Run serves until ctx is cancelled, then drains in-flight requests.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.cfg.ListenAddr(),
		Handler:           s.handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		s.logger.Info("listening",
			"addr", srv.Addr,
			"tls", s.cfg.TLS.Mode,
			"trusted_proxies", len(s.cfg.TrustedPrefixes()))
		if err := s.listen(srv); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
			return
		}
		errc <- nil
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	s.logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	status := http.StatusOK
	body := map[string]any{"status": "ok", "version": s.version}

	if err := s.db.PingContext(r.Context()); err != nil {
		status = http.StatusServiceUnavailable
		body["status"] = "degraded"
		body["error"] = "database unavailable"
		s.logger.Error("healthz database ping failed", "err", err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
