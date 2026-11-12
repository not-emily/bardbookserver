package api

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func resolverFor(t *testing.T, cidrs ...string) proxyResolver {
	t.Helper()
	prefixes := make([]netip.Prefix, 0, len(cidrs))
	for _, c := range cidrs {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			t.Fatalf("bad test CIDR %q: %v", c, err)
		}
		prefixes = append(prefixes, p.Masked())
	}
	return proxyResolver{trusted: prefixes}
}

func TestClientAddr(t *testing.T) {
	tests := []struct {
		name    string
		trusted []string
		peer    string
		xff     []string
		want    string
	}{
		{
			name: "no trusted proxies ignores the header entirely",
			peer: "203.0.113.7:4001",
			xff:  []string{"198.51.100.9"},
			want: "203.0.113.7",
		},
		{
			name:    "spoofed header from an untrusted peer is ignored",
			trusted: []string{"127.0.0.1/32"},
			peer:    "203.0.113.7:4001",
			xff:     []string{"10.0.0.1"},
			want:    "203.0.113.7",
		},
		{
			name:    "trusted peer yields the forwarded client",
			trusted: []string{"127.0.0.1/32"},
			peer:    "127.0.0.1:4001",
			xff:     []string{"198.51.100.9"},
			want:    "198.51.100.9",
		},
		{
			name:    "rightmost untrusted hop wins over a prepended value",
			trusted: []string{"127.0.0.1/32", "10.0.0.0/8"},
			peer:    "127.0.0.1:4001",
			xff:     []string{"1.2.3.4, 198.51.100.9, 10.0.0.5"},
			want:    "198.51.100.9",
		},
		{
			name:    "multiple header instances are flattened",
			trusted: []string{"127.0.0.1/32"},
			peer:    "127.0.0.1:4001",
			xff:     []string{"1.2.3.4", "198.51.100.9"},
			want:    "198.51.100.9",
		},
		{
			name:    "garbage entries are skipped",
			trusted: []string{"127.0.0.1/32"},
			peer:    "127.0.0.1:4001",
			xff:     []string{"198.51.100.9, not-an-address"},
			want:    "198.51.100.9",
		},
		{
			name:    "trusted peer with no header falls back to the peer",
			trusted: []string{"127.0.0.1/32"},
			peer:    "127.0.0.1:4001",
			want:    "127.0.0.1",
		},
		{
			name:    "all hops trusted falls back to the peer",
			trusted: []string{"127.0.0.1/32", "10.0.0.0/8"},
			peer:    "127.0.0.1:4001",
			xff:     []string{"10.0.0.5, 10.0.0.6"},
			want:    "127.0.0.1",
		},
		{
			name:    "ipv4-mapped ipv6 normalises to ipv4",
			trusted: []string{"127.0.0.1/32"},
			peer:    "127.0.0.1:4001",
			xff:     []string{"::ffff:198.51.100.9"},
			want:    "198.51.100.9",
		},
		{
			name:    "ipv6 loopback peer is matched by its own prefix",
			trusted: []string{"::1/128"},
			peer:    "[::1]:4001",
			xff:     []string{"198.51.100.9"},
			want:    "198.51.100.9",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/healthz", nil)
			r.RemoteAddr = tc.peer
			for _, v := range tc.xff {
				r.Header.Add("X-Forwarded-For", v)
			}

			got := resolverFor(t, tc.trusted...).clientAddr(r)
			if got.String() != tc.want {
				t.Errorf("clientAddr = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestClientScheme(t *testing.T) {
	tests := []struct {
		name    string
		trusted []string
		peer    string
		proto   string
		want    string
	}{
		{
			name: "no header means http",
			peer: "127.0.0.1:4001",
			want: "http",
		},
		{
			name:    "untrusted peer cannot claim https",
			trusted: []string{"127.0.0.1/32"},
			peer:    "203.0.113.7:4001",
			proto:   "https",
			want:    "http",
		},
		{
			name:    "trusted peer claiming https is believed",
			trusted: []string{"127.0.0.1/32"},
			peer:    "127.0.0.1:4001",
			proto:   "https",
			want:    "https",
		},
		{
			name:    "first value of a list is used",
			trusted: []string{"127.0.0.1/32"},
			peer:    "127.0.0.1:4001",
			proto:   "https, http",
			want:    "https",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/healthz", nil)
			r.RemoteAddr = tc.peer
			if tc.proto != "" {
				r.Header.Set("X-Forwarded-Proto", tc.proto)
			}

			if got := resolverFor(t, tc.trusted...).clientScheme(r); got != tc.want {
				t.Errorf("clientScheme = %s, want %s", got, tc.want)
			}
		})
	}
}
