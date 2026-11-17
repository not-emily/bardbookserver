package api

import (
	"net/http"
	"net/netip"
	"strings"
)

// Verdict levels, ordered by how much they should worry the reader.
const (
	verdictOK      = "ok"
	verdictWarn    = "warn"
	verdictBad     = "bad"
	verdictUnknown = "unknown"
)

// diagnosis answers one question: which address does rate limiting key on for
// this request, and is that the right one? A wrong `trusted_proxies` fails
// silently in both directions, and only a request that actually arrived
// through the deployment can settle it.
type diagnosis struct {
	Peer           string
	PeerTrusted    bool
	Forwarded      []string
	ForwardedProto string
	Client         string
	Scheme         string
	SecureContext  bool
	TrustedProxies []string

	Level    string
	Headline string
	Detail   string
}

func (s *Server) diagnose(r *http.Request) diagnosis {
	resolver := proxyResolver{trusted: s.cfg.TrustedPrefixes()}

	peer := peerAddr(r.RemoteAddr)
	hops := forwardedHops(r)
	client := resolver.clientAddr(r)
	scheme := resolver.clientScheme(r)

	configured := make([]string, 0, len(s.cfg.TrustedPrefixes()))
	for _, prefix := range s.cfg.TrustedPrefixes() {
		configured = append(configured, prefix.String())
	}

	d := diagnosis{
		Peer:           addrText(peer),
		PeerTrusted:    resolver.isTrusted(peer),
		Forwarded:      hops,
		ForwardedProto: strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")),
		Client:         addrText(client),
		Scheme:         scheme,
		SecureContext:  scheme == "https" || client.IsLoopback(),
		TrustedProxies: configured,
	}
	d.Level, d.Headline, d.Detail = verdict(d, peer, client, len(configured) > 0)
	return d
}

func verdict(d diagnosis, peer, client netip.Addr, trusting bool) (level, headline, detail string) {
	switch {
	case !trusting && len(d.Forwarded) > 0:
		return verdictBad,
			"Something is forwarding to Bard, and Bard is ignoring it.",
			"This request carried an X-Forwarded-For header, but trusted_proxies is " +
				"empty, so Bard discards it and keeps the address it can see. Every " +
				"client behind that proxy shares one rate-limit bucket, which means " +
				"one person failing logins locks out everybody. Set trusted_proxies " +
				"to " + suggest(peer) + "."

	case !trusting && peer.IsLoopback():
		return verdictUnknown,
			"This request came from the server itself, so it proves nothing.",
			"Load this page the way a real client would — through your tunnel, " +
				"proxy or port forward — and the verdict will mean something."

	case !trusting:
		return verdictOK,
			"Bard is reading addresses straight off the connection.",
			"No proxy is in front of this request and trusted_proxies is empty, " +
				"which is the right pair. Rate limiting keys on " + d.Client + "."

	case d.PeerTrusted && len(d.Forwarded) == 0:
		return verdictWarn,
			"Bard trusts this hop, but it is not telling Bard who the client is.",
			"The request came from " + d.Peer + ", which trusted_proxies covers, " +
				"but there is no X-Forwarded-For header. Everything therefore " +
				"resolves to " + d.Client + " and shares one rate-limit bucket. " +
				"Configure the proxy to send X-Forwarded-For."

	case d.PeerTrusted && client == peer:
		return verdictWarn,
			"Every forwarded address is itself trusted.",
			"Bard walked the X-Forwarded-For chain and found nothing outside " +
				"trusted_proxies, so it fell back to " + d.Client + ". That usually " +
				"means trusted_proxies is wider than it should be — it should list " +
				"only the hops in front of Bard, never the clients."

	case d.PeerTrusted:
		return verdictOK,
			"Client addresses are resolving correctly.",
			"The request reached Bard from " + d.Peer + ", which trusted_proxies " +
				"covers, and the forwarded chain resolves to " + d.Client + ". That " +
				"is the address rate limiting keys on, so two clients on different " +
				"networks are limited independently."

	default:
		return verdictBad,
			"A proxy is in front of Bard that trusted_proxies does not cover.",
			"This request arrived from " + d.Peer + ", which is not in " +
				"trusted_proxies, so its X-Forwarded-For header is discarded and " +
				"every client behind it resolves to " + d.Client + ". Either add " +
				suggest(peer) + " to trusted_proxies, or remove the proxy."
	}
}

// suggest offers the single-host prefix for the hop actually seen, which is
// the narrowest value that would work.
func suggest(peer netip.Addr) string {
	if !peer.IsValid() {
		return `the proxy's address`
	}
	return `["` + netip.PrefixFrom(peer, peer.BitLen()).String() + `"]`
}

func addrText(a netip.Addr) string {
	if !a.IsValid() {
		return "unknown"
	}
	return a.String()
}

func (s *Server) handleDiagnosticsPage(w http.ResponseWriter, r *http.Request) {
	data := s.page(r, "Diagnostics")
	data.Diagnosis = s.diagnose(r)
	data.BindAddr = s.cfg.ListenAddr()
	data.TLSMode = s.cfg.TLS.Mode
	s.render(w, http.StatusOK, "diagnostics.html", data)
}
