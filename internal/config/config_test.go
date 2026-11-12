package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bard.yml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yml"))
	if err != nil {
		t.Fatalf("a missing config must not be an error: %v", err)
	}
	if cfg.Port != DefaultPort {
		t.Errorf("Port = %d, want %d", cfg.Port, DefaultPort)
	}
	if cfg.TLS.Mode != TLSOff {
		t.Errorf("TLS.Mode = %q, want %q", cfg.TLS.Mode, TLSOff)
	}
	if len(cfg.TrustedPrefixes()) != 0 {
		t.Errorf("TrustedPrefixes = %v, want empty", cfg.TrustedPrefixes())
	}
}

// The shipped example must load, since it is what an operator starts from.
func TestLoadExampleConfig(t *testing.T) {
	cfg, err := Load("example.yml")
	if err != nil {
		t.Fatalf("example.yml does not load: %v", err)
	}
	if cfg.Port != DefaultPort {
		t.Errorf("Port = %d, want %d", cfg.Port, DefaultPort)
	}
	if cfg.TLS.Mode != TLSOff {
		t.Errorf("TLS.Mode = %q, want %q -- a bare off resolves to a bool in YAML", cfg.TLS.Mode, TLSOff)
	}
}

func TestTrustedProxyParsing(t *testing.T) {
	path := writeConfig(t, `
trusted_proxies: ["127.0.0.1", "10.0.0.0/8", "::1/128"]
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	prefixes := cfg.TrustedPrefixes()
	if len(prefixes) != 3 {
		t.Fatalf("got %d prefixes, want 3", len(prefixes))
	}
	if got := prefixes[0].String(); got != "127.0.0.1/32" {
		t.Errorf("bare address normalised to %q, want 127.0.0.1/32", got)
	}
}

func TestValidationRejectsBadValues(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"port zero", "port: 0"},
		{"port above range", "port: 70000"},
		{"ephemeral port", "port: 50000"},
		{"zero workers", "workers: 0"},
		{"unknown log level", `log_level: chatty`},
		{"unknown tls mode", `tls: {mode: maybe}`},
		{"tls auto without domains", `tls: {mode: auto}`},
		{"tls files without key", `tls: {mode: files, cert_file: /tmp/c.pem}`},
		{"malformed proxy cidr", `trusted_proxies: ["10.0.0.0/wat"]`},
		{"malformed proxy address", `trusted_proxies: ["nonsense"]`},
		{"empty data dir", `data_dir: ""`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, tc.body)); err == nil {
				t.Fatal("expected a validation error, got nil")
			}
		})
	}
}

func TestDerivedPaths(t *testing.T) {
	path := writeConfig(t, "data_dir: /srv/bard\nport: 9000\nbind_addr: 127.0.0.1\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.DatabasePath(); got != "/srv/bard/bard.db" {
		t.Errorf("DatabasePath = %q", got)
	}
	if got := cfg.ListenAddr(); got != "127.0.0.1:9000" {
		t.Errorf("ListenAddr = %q", got)
	}
}
