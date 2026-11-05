// Package cli implements the account commands of the hookshield CLI: login,
// logout, whoami, endpoints, listen and trigger. They talk to the control
// API with an API key (Authorization: Bearer hs_live_…).
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// DefaultAPIURL is the API of HookShield Cloud. Self-hosted installs pass
// their own URL to `hookshield login --api-url`.
const DefaultAPIURL = "https://api.hookshield.dev"

// Environment variables that override the saved configuration (CI, scripts).
const (
	EnvAPIURL = "HOOKSHIELD_API_URL"
	EnvAPIKey = "HOOKSHIELD_API_KEY"
	EnvConfig = "HOOKSHIELD_CONFIG" // path of the config file
)

// Config is what `hookshield login` saves.
type Config struct {
	APIURL string `json:"api_url"`
	APIKey string `json:"api_key"`
}

// ErrNotLoggedIn means there is no API key in the config file or environment.
var ErrNotLoggedIn = errors.New("not logged in: run `hookshield login` (or set HOOKSHIELD_API_KEY)")

// ConfigPath returns where the configuration lives:
// $HOOKSHIELD_CONFIG, else <user config dir>/hookshield/config.json
// (~/.config on Linux, ~/Library/Application Support on macOS, %AppData% on Windows).
func ConfigPath(getenv func(string) string) (string, error) {
	if p := getenv(EnvConfig); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("no config directory: %w (set %s)", err, EnvConfig)
	}
	return filepath.Join(dir, "hookshield", "config.json"), nil
}

// LoadConfig reads the config file (missing = empty) and applies the
// environment overrides.
func LoadConfig(getenv func(string) string) (Config, error) {
	var cfg Config
	path, err := ConfigPath(getenv)
	if err != nil {
		return cfg, err
	}
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return cfg, err
	default:
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return cfg, fmt.Errorf("%s: %w", path, err)
		}
	}
	if v := strings.TrimSpace(getenv(EnvAPIURL)); v != "" {
		cfg.APIURL = v
	}
	if v := strings.TrimSpace(getenv(EnvAPIKey)); v != "" {
		cfg.APIKey = v
	}
	if cfg.APIURL == "" {
		cfg.APIURL = DefaultAPIURL
	}
	cfg.APIURL = strings.TrimRight(cfg.APIURL, "/")
	return cfg, nil
}

// SaveConfig writes cfg readable only by the user (0600).
func SaveConfig(getenv func(string) string, cfg Config) (string, error) {
	path, err := ConfigPath(getenv)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	raw, _ := json.MarshalIndent(cfg, "", "  ")
	// A fresh temporary file is always created 0600 (os.WriteFile on a
	// leftover or pre-created file would keep that file's permissions).
	f, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	_, werr := f.Write(append(raw, '\n'))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp, 0o600)
	}
	if werr == nil {
		werr = os.Rename(tmp, path)
	}
	if werr != nil {
		os.Remove(tmp)
		return "", werr
	}
	return path, nil
}

// RemoveConfig deletes the saved configuration; a missing file is not an error.
func RemoveConfig(getenv func(string) string) (string, error) {
	path, err := ConfigPath(getenv)
	if err != nil {
		return "", err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return path, nil
}

// NormalizeAPIURL accepts "hooks.example.com" or a full URL and returns the
// URL without trailing slash.
func NormalizeAPIURL(s string) (string, error) {
	s = strings.TrimRight(strings.TrimSpace(s), "/")
	if s == "" {
		return "", errors.New("API URL is empty")
	}
	if !strings.Contains(s, "://") {
		host := strings.SplitN(s, "/", 2)[0]
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if host == "localhost" || host == "127.0.0.1" || host == "[::1]" || host == "::1" {
			s = "http://" + s // local installs have no TLS
		} else {
			s = "https://" + s
		}
	}
	if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
		return "", fmt.Errorf("API URL must start with http:// or https://, got %q", s)
	}
	return s, nil
}
