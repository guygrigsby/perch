// Package xdg resolves per-app config locations once, so config and client
// don't each reimplement the XDG_CONFIG_HOME / ~/.config rule.
package xdg

import (
	"os"
	"path/filepath"
)

// ConfigDir returns $XDG_CONFIG_HOME/<app> when XDG_CONFIG_HOME is set,
// otherwise ~/.config/<app>.
func ConfigDir(app string) (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, app), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", app), nil
}
