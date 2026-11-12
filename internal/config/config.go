package config

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	DefaultPort = 2273

	TLSOff   = "off"
	TLSAuto  = "auto"
	TLSFiles = "files"
)

type Config struct {
	DataDir        string   `yaml:"data_dir"`
	BindAddr       string   `yaml:"bind_addr"`
	Port           int      `yaml:"port"`
	TrustedProxies []string `yaml:"trusted_proxies"`
	Workers        int      `yaml:"workers"`
	LogLevel       string   `yaml:"log_level"`
	TLS            TLS      `yaml:"tls"`

	trustedPrefixes []netip.Prefix
}

// TLS is parsed and validated here but not wired until v1 phase 12; Bard runs
// behind a tunnel or proxy in every other deployment recipe.
type TLS struct {
	Mode     string   `yaml:"mode"`
	CertFile string   `yaml:"cert_file"`
	KeyFile  string   `yaml:"key_file"`
	Domains  []string `yaml:"domains"`
}

func defaults() Config {
	return Config{
		DataDir:        defaultDataDir(),
		BindAddr:       "0.0.0.0",
		Port:           DefaultPort,
		TrustedProxies: nil,
		Workers:        2,
		LogLevel:       "info",
		TLS:            TLS{Mode: TLSOff},
	}
}

func defaultDataDir() string {
	if dir := os.Getenv("BARD_DATA_DIR"); dir != "" {
		return dir
	}
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "bard")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "share", "bard")
	}
	return "bard-data"
}

// SearchPaths is where Load looks when given no explicit path.
func SearchPaths() []string {
	if p := os.Getenv("BARD_CONFIG"); p != "" {
		return []string{p}
	}
	return []string{"bard.yml", "/etc/bard/bard.yml"}
}

// Load reads the first config file it finds. A missing file is not an error:
// an install with no config must still serve, so defaults stand alone.
func Load(path string) (*Config, error) {
	cfg := defaults()

	candidates := []string{path}
	if path == "" {
		candidates = SearchPaths()
	}

	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read config %s: %w", p, err)
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parse config %s: %w", p, err)
		}
		break
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) validate() error {
	if strings.TrimSpace(c.DataDir) == "" {
		return errors.New("data_dir: must not be empty")
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("port: %d is outside 1-65535", c.Port)
	}
	if c.Port >= 49152 {
		return fmt.Errorf("port: %d is in the ephemeral range and may collide with an outbound connection", c.Port)
	}
	if c.Workers < 1 {
		return fmt.Errorf("workers: %d must be at least 1", c.Workers)
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("log_level: %q must be one of debug, info, warn, error", c.LogLevel)
	}

	switch c.TLS.Mode {
	case TLSOff:
	case TLSAuto:
		if len(c.TLS.Domains) == 0 {
			return errors.New("tls.domains: at least one domain is required when tls.mode is auto")
		}
	case TLSFiles:
		if c.TLS.CertFile == "" || c.TLS.KeyFile == "" {
			return errors.New("tls.cert_file and tls.key_file are required when tls.mode is files")
		}
	default:
		return fmt.Errorf("tls.mode: %q must be one of off, auto, files", c.TLS.Mode)
	}

	prefixes, err := parseProxies(c.TrustedProxies)
	if err != nil {
		return err
	}
	c.trustedPrefixes = prefixes
	return nil
}

// parseProxies accepts CIDRs and bare addresses, normalising the latter to a
// single-host prefix.
func parseProxies(entries []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(entries))
	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			p, err := netip.ParsePrefix(entry)
			if err != nil {
				return nil, fmt.Errorf("trusted_proxies: %q is not a valid CIDR: %w", entry, err)
			}
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(entry)
		if err != nil {
			return nil, fmt.Errorf("trusted_proxies: %q is not a valid address or CIDR: %w", entry, err)
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

func (c *Config) TrustedPrefixes() []netip.Prefix { return c.trustedPrefixes }

func (c *Config) DatabasePath() string { return filepath.Join(c.DataDir, "bard.db") }

func (c *Config) CoverCacheDir() string { return filepath.Join(c.DataDir, "covers") }

func (c *Config) ListenAddr() string { return fmt.Sprintf("%s:%d", c.BindAddr, c.Port) }
