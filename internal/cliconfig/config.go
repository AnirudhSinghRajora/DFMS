// Package cliconfig manages the dfmsctl on-disk configuration: the set of named
// server "contexts" and which one is active. It owns the file format, its
// location (XDG-aware), and safe read/modify/write semantics. Runtime overrides
// (flags, environment variables) are layered on top by the command package.
package cliconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// appDirName is the per-user directory (under the config home) that holds all
// dfmsctl state.
const appDirName = "dfms"

// File and directory permissions. The config can hold server URLs and, in later
// phases, sits beside token material, so it is kept private to the user.
const (
	dirPerm  os.FileMode = 0o700
	filePerm os.FileMode = 0o600
)

// Config is the complete dfmsctl configuration as persisted to disk.
type Config struct {
	// CurrentContext is the name of the active context. It may be empty when no
	// context has been selected yet.
	CurrentContext string `yaml:"current_context,omitempty"`
	// Contexts maps a context name to its settings.
	Contexts map[string]Context `yaml:"contexts"`
	// Defaults holds optional default settings. It is reserved for later phases
	// (e.g. default output format, multipart threshold) and omitted when unset.
	Defaults *Defaults `yaml:"defaults,omitempty"`
}

// Context describes how to reach a single DFMS server.
type Context struct {
	// URL is the base URL of the DFMS API, e.g. "https://dfms.example.com".
	URL string `yaml:"url"`
	// InsecureSkipVerify disables TLS certificate verification for this context.
	// It is an explicit, per-context opt-in intended for self-signed dev servers.
	InsecureSkipVerify bool `yaml:"insecure_skip_verify,omitempty"`
}

// Defaults holds optional default settings applied when a flag is not given.
// Reserved for a later phase; defined now so the file schema is stable.
type Defaults struct {
	Output             string `yaml:"output,omitempty"`
	MultipartThreshold int64  `yaml:"multipart_threshold,omitempty"`
}

// DefaultPath returns the location of the config file, honoring $XDG_CONFIG_HOME
// and otherwise falling back to ~/.config. The path is the same on every
// platform so behavior is predictable for an XDG-style CLI.
func DefaultPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

func configDir() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, appDirName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home directory: %w", err)
	}
	return filepath.Join(home, ".config", appDirName), nil
}

// Load reads and parses the config at path. A missing file is not an error: it
// returns an empty, ready-to-use config so first-run commands work seamlessly.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return empty(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}
	if cfg.Contexts == nil {
		cfg.Contexts = map[string]Context{}
	}
	return &cfg, nil
}

func empty() *Config {
	return &Config{Contexts: map[string]Context{}}
}

// Save writes the config to path atomically, creating the parent directory if
// needed. The file is written with owner-only permissions.
func (c *Config) Save(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	if err := writeFileAtomic(path, data, filePerm); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	return nil
}

// SetContext adds or replaces the named context.
func (c *Config) SetContext(name string, ctx Context) {
	if c.Contexts == nil {
		c.Contexts = map[string]Context{}
	}
	c.Contexts[name] = ctx
}

// Context returns the named context and whether it exists.
func (c *Config) Context(name string) (Context, bool) {
	ctx, ok := c.Contexts[name]
	return ctx, ok
}

// Use marks name as the active context. It fails if the context is unknown.
func (c *Config) Use(name string) error {
	if _, ok := c.Contexts[name]; !ok {
		return fmt.Errorf("context %q does not exist", name)
	}
	c.CurrentContext = name
	return nil
}

// RemoveContext deletes the named context. It fails if the context is unknown.
// If the removed context was active, the active selection is cleared.
func (c *Config) RemoveContext(name string) error {
	if _, ok := c.Contexts[name]; !ok {
		return fmt.Errorf("context %q does not exist", name)
	}
	delete(c.Contexts, name)
	if c.CurrentContext == name {
		c.CurrentContext = ""
	}
	return nil
}

// Names returns the context names in sorted order for stable output.
func (c *Config) Names() []string {
	names := make([]string, 0, len(c.Contexts))
	for name := range c.Contexts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
