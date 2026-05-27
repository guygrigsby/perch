// Package config loads an app's ~/.config/<app>/config.toml into a
// caller-defined struct.
package config

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/guygrigsby/perch/internal/xdg"
)

// Dir returns the app's config directory (XDG-aware).
func Dir(app string) (string, error) {
	return xdg.ConfigDir(app)
}

// Load decodes <Dir>/config.toml into v. A missing file is not an error:
// v keeps whatever defaults the caller set. A malformed file is an error.
func Load(app string, v any) error {
	dir, err := Dir(app)
	if err != nil {
		return err
	}
	_, err = toml.DecodeFile(filepath.Join(dir, "config.toml"), v)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
