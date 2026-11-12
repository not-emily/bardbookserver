package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/not-emily/bardbookserver/internal/config"
	"github.com/not-emily/bardbookserver/internal/db"
)

const shutdownGrace = 15 * time.Second

type Server struct {
	cfg     *config.Config
	db      *db.DB
	logger  *slog.Logger
	version string
}

func NewServer(cfg *config.Config, database *db.DB, logger *slog.Logger, version string) *Server {
	return &Server{cfg: cfg, db: database, logger: logger, version: version}
}

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)

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
