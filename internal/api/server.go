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
	"github.com/not-emily/bardbookserver/internal/media"
	"github.com/not-emily/bardbookserver/internal/space"
)

const shutdownGrace = 15 * time.Second

type Server struct {
	cfg     *config.Config
	db      *db.DB
	store   *auth.Store
	space   *space.Store
	covers  *media.Cache
	logger  *slog.Logger
	version string

	loginLimiter *auth.Limiter
	pinLimiter   *auth.Limiter
}

func NewServer(cfg *config.Config, database *db.DB, logger *slog.Logger, version string) *Server {
	return &Server{
		cfg:          cfg,
		db:           database,
		store:        auth.NewStore(database.DB),
		space:        space.NewStore(database),
		covers:       media.NewCache(cfg.CoverCacheDir()),
		logger:       logger,
		version:      version,
		loginLimiter: auth.NewLimiter(loginAttempts, loginWindow),
		pinLimiter:   auth.NewLimiter(pinAttempts, pinWindow),
	}
}

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)

	authed := http.NewServeMux()
	authed.HandleFunc("POST /api/v1/auth/logout", s.handleLogout)
	authed.HandleFunc("GET /api/v1/auth/me", s.handleMe)
	authed.HandleFunc("POST /api/v1/auth/switch", s.handleSwitchProfile)
	authed.HandleFunc("GET /api/v1/devices", s.handleListDevices)
	authed.HandleFunc("DELETE /api/v1/devices/{id}", s.handleRevokeDevice)
	authed.HandleFunc("GET /api/v1/books", s.handleListBooks)
	authed.HandleFunc("GET /api/v1/books/{id}", s.handleGetBook)
	authed.HandleFunc("GET /api/v1/books/{id}/cover", s.handleBookCover)
	authed.HandleFunc("GET /api/v1/files/{id}/content", s.handleFileContent)
	authed.HandleFunc("GET /api/v1/search", s.handleSearch)
	authed.HandleFunc("GET /api/v1/facets", s.handleListFacets)
	authed.HandleFunc("GET /api/v1/facets/{name}", s.handleGetFacet)
	authed.HandleFunc("GET /api/v1/series", s.handleListSeries)
	authed.HandleFunc("GET /api/v1/series/{id}", s.handleGetSeries)
	authed.HandleFunc("GET /api/v1/collections", s.handleListCollections)
	authed.HandleFunc("GET /api/v1/collections/{id}", s.handleGetCollection)
	authed.HandleFunc("GET /api/v1/editions/{id}/progress", s.handleGetProgress)
	authed.HandleFunc("PUT /api/v1/editions/{id}/progress", s.handleSetProgress)
	authed.HandleFunc("PUT /api/v1/books/{id}/saved", s.handleSaveBook)
	authed.HandleFunc("DELETE /api/v1/books/{id}/saved", s.handleUnsaveBook)
	authed.HandleFunc("PUT /api/v1/books/{id}/rating", s.handleSetRating)
	authed.HandleFunc("DELETE /api/v1/books/{id}/rating", s.handleClearRating)
	authed.HandleFunc("PUT /api/v1/books/{id}/state", s.handleSetState)
	authed.HandleFunc("GET /api/v1/history", s.handleHistory)
	authed.HandleFunc("GET /api/v1/sync", s.handleSync)
	authed.HandleFunc("POST /api/v1/editions/{id}/handoff", s.handleHandoff)
	authed.HandleFunc("GET /api/v1/books/{id}/handoff", s.handleBookHandoffTargets)
	authed.HandleFunc("GET /api/v1/annotations", s.handleListAnnotations)
	authed.HandleFunc("GET /api/v1/annotations/changes", s.handleAnnotationChanges)
	authed.HandleFunc("PUT /api/v1/annotations/{uuid}", s.handleSaveAnnotation)
	authed.HandleFunc("DELETE /api/v1/annotations/{uuid}", s.handleDeleteAnnotation)
	authed.HandleFunc("POST /api/v1/collections", s.handleCreateCollection)
	authed.HandleFunc("PATCH /api/v1/collections/{id}", s.handleUpdateCollection)
	authed.HandleFunc("DELETE /api/v1/collections/{id}", s.handleDeleteCollection)
	authed.HandleFunc("PUT /api/v1/collections/{id}/books/{bookID}", s.handleAddToCollection)
	authed.HandleFunc("DELETE /api/v1/collections/{id}/books/{bookID}", s.handleRemoveFromCollection)
	authed.HandleFunc("GET /api/v1/groupings/uncertain", s.handleListUncertainGroupings)
	authed.HandleFunc("POST /api/v1/editions/{id}/group", s.handleMergeEdition)
	authed.HandleFunc("DELETE /api/v1/editions/{id}/group", s.handleSplitEdition)
	mux.Handle("/api/v1/", s.requireAuth(authed))

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
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
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
