package api

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"

	"github.com/not-emily/bardbookserver/internal/config"
)

// redirectToTLS sends a plain request to the port Bard actually serves TLS on.
func (s *Server) redirectToTLS() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(r.Host); err == nil {
			host = h
		}
		if s.cfg.Port != 443 {
			host = net.JoinHostPort(host, strconv.Itoa(s.cfg.Port))
		}

		target := url.URL{Scheme: "https", Host: host, Path: r.URL.Path, RawQuery: r.URL.RawQuery}
		http.Redirect(w, r, target.String(), http.StatusMovedPermanently)
	})
}

// listen starts srv under whichever TLS mode is configured and returns when it
// stops.
func (s *Server) listen(srv *http.Server) error {
	switch s.cfg.TLS.Mode {
	case config.TLSFiles:
		return srv.ListenAndServeTLS(s.cfg.TLS.CertFile, s.cfg.TLS.KeyFile)
	case config.TLSAuto:
		return s.listenAutocert(srv)
	default:
		return srv.ListenAndServe()
	}
}

// listenAutocert serves with certificates Let's Encrypt issues on demand.
//
// A challenge can only be answered where the CA looks for it: TLS-ALPN-01 on
// 443, or HTTP-01 on 80. Bard's default port is neither, so the HTTP-01
// listener is what makes `tls: auto` work on any other port, and losing it is
// reported rather than left to a silent renewal failure months later.
func (s *Server) listenAutocert(srv *http.Server) error {
	manager := &autocert.Manager{
		Cache:      autocert.DirCache(filepath.Join(s.cfg.DataDir, "acme")),
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(s.cfg.TLS.Domains...),
	}
	if s.cfg.TLS.ACMEDirectory != "" {
		manager.Client = &acme.Client{DirectoryURL: s.cfg.TLS.ACMEDirectory}
		s.logger.Warn("using a non-default ACME directory",
			"directory", s.cfg.TLS.ACMEDirectory)
	}

	srv.TLSConfig = manager.TLSConfig()
	srv.TLSConfig.MinVersion = tls.VersionTLS12

	challenge := &http.Server{
		Addr: s.cfg.TLS.ChallengeAddr,
		// autocert's own fallback redirects to port 443, which is the wrong
		// place whenever Bard is not serving there.
		Handler:           manager.HTTPHandler(s.redirectToTLS()),
		ReadHeaderTimeout: srv.ReadHeaderTimeout,
	}
	go func() {
		s.logger.Info("acme challenge listener", "addr", challenge.Addr, "domains", s.cfg.TLS.Domains)
		if err := challenge.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error("acme challenge listener stopped; certificates cannot be issued or renewed",
				"addr", challenge.Addr, "error", err)
		}
	}()
	defer challenge.Close()

	// The certificates come from the manager, so the files stay empty.
	if err := srv.ListenAndServeTLS("", ""); err != nil {
		return fmt.Errorf("serve tls: %w", err)
	}
	return nil
}
