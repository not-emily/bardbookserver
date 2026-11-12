package api

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

type contextKey int

const (
	clientAddrKey contextKey = iota
	clientSchemeKey
)

// ClientAddr returns the caller's address as resolved against the trusted
// proxy allowlist. Rate limiting and audit logging must use this rather than
// RemoteAddr.
func ClientAddr(ctx context.Context) netip.Addr {
	addr, _ := ctx.Value(clientAddrKey).(netip.Addr)
	return addr
}

// ClientScheme is the scheme the client actually used, which differs from the
// connection's scheme whenever a proxy terminates TLS.
func ClientScheme(ctx context.Context) string {
	if scheme, ok := ctx.Value(clientSchemeKey).(string); ok {
		return scheme
	}
	return "http"
}

type proxyResolver struct {
	trusted []netip.Prefix
}

func (p proxyResolver) isTrusted(addr netip.Addr) bool {
	for _, prefix := range p.trusted {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// clientAddr walks X-Forwarded-For right to left and returns the rightmost
// address not belonging to a trusted proxy.
//
// Direction matters: a proxy appends the address it saw, so the rightmost
// untrusted entry is the furthest hop we have any reason to believe. Reading
// left to right would return whatever the caller chose to prepend.
func (p proxyResolver) clientAddr(r *http.Request) netip.Addr {
	peer := peerAddr(r.RemoteAddr)
	if !p.isTrusted(peer) {
		return peer
	}

	hops := forwardedHops(r)
	for i := len(hops) - 1; i >= 0; i-- {
		addr, err := netip.ParseAddr(hops[i])
		if err != nil {
			continue
		}
		addr = addr.Unmap().WithZone("")
		if !p.isTrusted(addr) {
			return addr
		}
	}
	return peer
}

func (p proxyResolver) clientScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if !p.isTrusted(peerAddr(r.RemoteAddr)) {
		return "http"
	}
	if proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); proto != "" {
		if scheme := strings.ToLower(strings.Split(proto, ",")[0]); scheme == "https" {
			return "https"
		}
	}
	return "http"
}

func forwardedHops(r *http.Request) []string {
	var hops []string
	for _, header := range r.Header.Values("X-Forwarded-For") {
		for _, part := range strings.Split(header, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				hops = append(hops, trimmed)
			}
		}
	}
	return hops
}

func peerAddr(remoteAddr string) netip.Addr {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return addr.Unmap().WithZone("")
}

func (p proxyResolver) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), clientAddrKey, p.clientAddr(r))
		ctx = context.WithValue(ctx, clientSchemeKey, p.clientScheme(r))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			logger.LogAttrs(r.Context(), slog.LevelInfo, "request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.String("client", ClientAddr(r.Context()).String()),
				slog.Duration("took", time.Since(start)),
			)
		})
	}
}

func recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					logger.Error("panic serving request",
						"path", r.URL.Path,
						"panic", v,
						"client", ClientAddr(r.Context()).String())
					http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
