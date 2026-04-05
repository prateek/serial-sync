package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"
)

const (
	DefaultConfigEnv    = "SERIAL_SYNC_CONFIG"
	ContainerModeEnv    = "SERIAL_SYNC_CONTAINER"
	containerConfigDir  = "/config"
	containerStateDir   = "/state"
	containerCacheDir   = "/state/cache"
	containerRuntimeDir = "/tmp/serial-sync-runtime"
)

type Config struct {
	Runtime      RuntimeConfig     `toml:"runtime"`
	Scheduler    SchedulerConfig   `toml:"scheduler"`
	AuthProfiles []AuthProfile     `toml:"auth_profiles"`
	Publishers   []PublisherConfig `toml:"publishers"`
	Sources      []SourceConfig    `toml:"sources"`
	Rules        []RuleConfig      `toml:"rules"`
}

type RuntimeConfig struct {
	LogLevel     string `toml:"log_level"`
	LogFormat    string `toml:"log_format"`
	StoreDriver  string `toml:"store_driver"`
	StoreDSN     string `toml:"store_dsn"`
	ArtifactRoot string `toml:"artifact_root"`
	SupportRoot  string `toml:"support_root"`
}

type SchedulerConfig struct {
	Mode         string `toml:"mode"`
	PollInterval string `toml:"poll_interval"`
	LeaseTTL     string `toml:"lease_ttl"`
	HealthAddr   string `toml:"health_addr"`
}

type AuthProfile struct {
	ID            string `toml:"id"`
	Provider      string `toml:"provider"`
	Mode          string `toml:"mode"`
	UsernameEnv   string `toml:"username_env"`
	PasswordEnv   string `toml:"password_env"`
	TOTPSecretEnv string `toml:"totp_secret_env"`
	SessionPath   string `toml:"session_path"`
}

type PublisherConfig struct {
	ID      string   `toml:"id"`
	Kind    string   `toml:"kind"`
	Path    string   `toml:"path"`
	Command []string `toml:"command"`
	Enabled bool     `toml:"enabled"`
}

type SourceConfig struct {
	ID          string `toml:"id"`
	Provider    string `toml:"provider"`
	URL         string `toml:"url"`
	AuthProfile string `toml:"auth_profile"`
	Enabled     bool   `toml:"enabled"`
	FixtureDir  string `toml:"fixture_dir"`
}

type RuleConfig struct {
	Source             string   `toml:"source"`
	Priority           int      `toml:"priority"`
	MatchType          string   `toml:"match_type"`
	MatchValue         string   `toml:"match_value"`
	TrackKey           string   `toml:"track_key"`
	TrackName          string   `toml:"track_name"`
	ReleaseRole        string   `toml:"release_role"`
	ContentStrategy    string   `toml:"content_strategy"`
	AttachmentGlob     []string `toml:"attachment_glob"`
	AttachmentPriority []string `toml:"attachment_priority"`
	AnthologyMode      bool     `toml:"anthology_mode"`
}

type Roots struct {
	ConfigDir     string
	StateDir      string
	CacheDir      string
	RuntimeDir    string
	Containerized bool
}

func DefaultRoots() (Roots, error) {
	if shouldUseContainerRoots() {
		return Roots{
			ConfigDir:     containerConfigDir,
			StateDir:      containerStateDir,
			CacheDir:      containerCacheDir,
			RuntimeDir:    getenvDefault("SERIAL_SYNC_RUNTIME_DIR", containerRuntimeDir),
			Containerized: true,
		}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Roots{}, err
	}
	configHome := getenvDefault("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	stateHome := getenvDefault("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	cacheHome := getenvDefault("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	runtimeHome := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeHome == "" {
		runtimeHome = filepath.Join(os.TempDir(), "serial-sync-runtime")
	}
	return Roots{
		ConfigDir:     filepath.Join(configHome, "serial-sync"),
		StateDir:      filepath.Join(stateHome, "serial-sync"),
		CacheDir:      filepath.Join(cacheHome, "serial-sync"),
		RuntimeDir:    filepath.Join(runtimeHome, "serial-sync"),
		Containerized: false,
	}, nil
}

func DefaultConfigPath() (string, error) {
	if explicit := os.Getenv(DefaultConfigEnv); explicit != "" {
		return explicit, nil
	}
	roots, err := DefaultRoots()
	if err != nil {
		return "", err
	}
	return filepath.Join(roots.ConfigDir, "config.toml"), nil
}

func Load(path string) (*Config, Roots, error) {
	roots, err := DefaultRoots()
	if err != nil {
		return nil, Roots{}, err
	}
	if path == "" {
		path, err = DefaultConfigPath()
		if err != nil {
			return nil, Roots{}, err
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, Roots{}, err
	}
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, Roots{}, err
	}
	cfg.ApplyDefaults(roots)
	if err := cfg.expandPaths(roots); err != nil {
		return nil, Roots{}, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, Roots{}, err
	}
	return &cfg, roots, nil
}

func (c *Config) ApplyDefaults(roots Roots) {
	if c.Runtime.LogLevel == "" {
		c.Runtime.LogLevel = "info"
	}
	if c.Runtime.LogFormat == "" {
		c.Runtime.LogFormat = "text"
	}
	if c.Runtime.StoreDriver == "" {
		c.Runtime.StoreDriver = "sqlite"
	}
	if c.Runtime.StoreDSN == "" {
		c.Runtime.StoreDSN = filepath.Join(roots.StateDir, "state.db")
	}
	if c.Runtime.ArtifactRoot == "" {
		c.Runtime.ArtifactRoot = filepath.Join(roots.StateDir, "artifacts")
	}
	if c.Runtime.SupportRoot == "" {
		c.Runtime.SupportRoot = filepath.Join(roots.StateDir, "support")
	}
	if normalizeSchedulerMode(c.Scheduler.Mode) == "" {
		c.Scheduler.Mode = "interval"
	}
	if c.Scheduler.PollInterval == "" {
		c.Scheduler.PollInterval = "1h"
	}
	if c.Scheduler.LeaseTTL == "" {
		c.Scheduler.LeaseTTL = "30m"
	}
	if c.Scheduler.HealthAddr == "" {
		c.Scheduler.HealthAddr = "127.0.0.1:8099"
	}
}

func (c *Config) expandPaths(roots Roots) error {
	expand := func(input string) string {
		replacer := strings.NewReplacer(
			"${XDG_CONFIG_HOME}", strings.TrimSuffix(roots.ConfigDir, "/serial-sync"),
			"${XDG_STATE_HOME}", strings.TrimSuffix(roots.StateDir, "/serial-sync"),
			"${XDG_CACHE_HOME}", strings.TrimSuffix(roots.CacheDir, "/serial-sync"),
			"${XDG_RUNTIME_DIR}", strings.TrimSuffix(roots.RuntimeDir, "/serial-sync"),
			"${HOME}", getenvDefault("HOME", ""),
		)
		output := replacer.Replace(input)
		return os.ExpandEnv(output)
	}
	c.Runtime.StoreDSN = expand(c.Runtime.StoreDSN)
	c.Runtime.ArtifactRoot = expand(c.Runtime.ArtifactRoot)
	c.Runtime.SupportRoot = expand(c.Runtime.SupportRoot)
	for idx := range c.AuthProfiles {
		c.AuthProfiles[idx].SessionPath = expand(c.AuthProfiles[idx].SessionPath)
	}
	for idx := range c.Publishers {
		c.Publishers[idx].Path = expand(c.Publishers[idx].Path)
		for argIdx := range c.Publishers[idx].Command {
			c.Publishers[idx].Command[argIdx] = expand(c.Publishers[idx].Command[argIdx])
		}
	}
	for idx := range c.Sources {
		c.Sources[idx].FixtureDir = expand(c.Sources[idx].FixtureDir)
	}
	return nil
}

func (c *Config) Validate() error {
	if len(c.Sources) == 0 {
		return errors.New("config must define at least one source")
	}
	sourceIDs := map[string]struct{}{}
	authIDs := map[string]struct{}{}
	publisherIDs := map[string]struct{}{}
	for _, auth := range c.AuthProfiles {
		if auth.ID == "" {
			return errors.New("auth profile id is required")
		}
		if auth.Provider == "" {
			return fmt.Errorf("auth profile %q provider is required", auth.ID)
		}
		switch normalizeAuthMode(auth.Mode) {
		case "", "fixture":
		case "username_password":
			if auth.UsernameEnv == "" {
				return fmt.Errorf("auth profile %q username_env is required for username_password mode", auth.ID)
			}
			if auth.PasswordEnv == "" {
				return fmt.Errorf("auth profile %q password_env is required for username_password mode", auth.ID)
			}
			if auth.SessionPath == "" {
				return fmt.Errorf("auth profile %q session_path is required for username_password mode", auth.ID)
			}
			if strings.TrimSpace(auth.TOTPSecretEnv) != "" && strings.TrimSpace(auth.TOTPSecretEnv) == strings.TrimSpace(auth.PasswordEnv) {
				return fmt.Errorf("auth profile %q totp_secret_env must not match password_env", auth.ID)
			}
		default:
			return fmt.Errorf("auth profile %q has unsupported mode %q", auth.ID, auth.Mode)
		}
		authIDs[auth.ID] = struct{}{}
	}
	switch normalizeSchedulerMode(c.Scheduler.Mode) {
	case "", "interval", "disabled":
	default:
		return fmt.Errorf("scheduler mode %q is unsupported", c.Scheduler.Mode)
	}
	if strings.TrimSpace(c.Scheduler.PollInterval) != "" {
		if _, err := time.ParseDuration(c.Scheduler.PollInterval); err != nil {
			return fmt.Errorf("scheduler poll_interval %q is invalid: %w", c.Scheduler.PollInterval, err)
		}
	}
	if strings.TrimSpace(c.Scheduler.LeaseTTL) != "" {
		if _, err := time.ParseDuration(c.Scheduler.LeaseTTL); err != nil {
			return fmt.Errorf("scheduler lease_ttl %q is invalid: %w", c.Scheduler.LeaseTTL, err)
		}
	}
	for _, source := range c.Sources {
		if source.ID == "" {
			return errors.New("source id is required")
		}
		if _, exists := sourceIDs[source.ID]; exists {
			return fmt.Errorf("duplicate source id %q", source.ID)
		}
		sourceIDs[source.ID] = struct{}{}
		if source.Provider == "" {
			return fmt.Errorf("source %q provider is required", source.ID)
		}
		if source.URL == "" {
			return fmt.Errorf("source %q url is required", source.ID)
		}
		if source.AuthProfile != "" {
			auth, ok := c.AuthProfileByID(source.AuthProfile)
			if !ok {
				return fmt.Errorf("source %q references unknown auth profile %q", source.ID, source.AuthProfile)
			}
			if auth.Provider != source.Provider {
				return fmt.Errorf("source %q provider %q does not match auth profile %q provider %q", source.ID, source.Provider, auth.ID, auth.Provider)
			}
		}
	}
	for _, publisher := range c.Publishers {
		if publisher.ID == "" {
			return errors.New("publisher id is required")
		}
		if _, exists := publisherIDs[publisher.ID]; exists {
			return fmt.Errorf("duplicate publisher id %q", publisher.ID)
		}
		publisherIDs[publisher.ID] = struct{}{}
		switch normalizePublisherKind(publisher.Kind) {
		case "filesystem":
			if publisher.Path == "" {
				return fmt.Errorf("publisher %q path is required for filesystem targets", publisher.ID)
			}
		case "exec":
			if len(publisher.Command) == 0 {
				return fmt.Errorf("publisher %q command is required for exec targets", publisher.ID)
			}
		default:
			return fmt.Errorf("publisher %q has unsupported kind %q", publisher.ID, publisher.Kind)
		}
	}
	for _, rule := range c.Rules {
		if rule.Source == "" {
			return errors.New("rule source is required")
		}
		if _, ok := sourceIDs[rule.Source]; !ok {
			return fmt.Errorf("rule references unknown source %q", rule.Source)
		}
		if rule.TrackKey == "" {
			return fmt.Errorf("rule for source %q must set track_key", rule.Source)
		}
	}
	return nil
}

func (c *Config) AuthProfileByID(id string) (AuthProfile, bool) {
	for _, profile := range c.AuthProfiles {
		if profile.ID == id {
			return profile, true
		}
	}
	return AuthProfile{}, false
}

func (c *Config) SourceByID(id string) (SourceConfig, bool) {
	for _, source := range c.Sources {
		if source.ID == id {
			return source, true
		}
	}
	return SourceConfig{}, false
}

func (c *Config) RulesForSource(sourceID string) []RuleConfig {
	var rules []RuleConfig
	for _, rule := range c.Rules {
		if rule.Source == sourceID {
			rules = append(rules, rule)
		}
	}
	return rules
}

func (c *Config) PublisherByID(id string) (PublisherConfig, bool) {
	for _, publisher := range c.Publishers {
		if publisher.ID == id {
			return publisher, true
		}
	}
	return PublisherConfig{}, false
}

func EnsureDirs(roots Roots, cfg *Config) error {
	dirs := []string{
		roots.StateDir,
		roots.CacheDir,
		roots.RuntimeDir,
		filepath.Dir(cfg.Runtime.StoreDSN),
		cfg.Runtime.ArtifactRoot,
		cfg.Runtime.SupportRoot,
	}
	for _, auth := range cfg.AuthProfiles {
		if auth.SessionPath != "" {
			dirs = append(dirs, filepath.Dir(auth.SessionPath))
		}
	}
	for _, publisher := range cfg.Publishers {
		if normalizePublisherKind(publisher.Kind) == "filesystem" && publisher.Path != "" {
			dirs = append(dirs, publisher.Path)
		}
	}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func ExampleConfig() string {
	roots, err := DefaultRoots()
	if err != nil {
		roots = Roots{
			ConfigDir:  "${XDG_CONFIG_HOME}/serial-sync",
			StateDir:   "${XDG_STATE_HOME}/serial-sync",
			CacheDir:   "${XDG_CACHE_HOME}/serial-sync",
			RuntimeDir: "${XDG_RUNTIME_DIR}/serial-sync",
		}
	}
	storeDSN := "${XDG_STATE_HOME}/serial-sync/state.db"
	artifactRoot := "${XDG_STATE_HOME}/serial-sync/artifacts"
	supportRoot := "${XDG_STATE_HOME}/serial-sync/support"
	sessionPath := "${XDG_STATE_HOME}/serial-sync/sessions/patreon-default.json"
	publishPath := "${XDG_STATE_HOME}/serial-sync/published"
	if roots.Containerized {
		storeDSN = filepath.Join(roots.StateDir, "state.db")
		artifactRoot = filepath.Join(roots.StateDir, "artifacts")
		supportRoot = filepath.Join(roots.StateDir, "support")
		sessionPath = filepath.Join(roots.StateDir, "sessions", "patreon-default.json")
		publishPath = filepath.Join(roots.StateDir, "published")
	}
	return fmt.Sprintf(`[runtime]
log_level = "info"
log_format = "text"
store_driver = "sqlite"
store_dsn = %q
artifact_root = %q
support_root = %q

[scheduler]
mode = "interval"
poll_interval = "1h"
lease_ttl = "30m"
health_addr = "127.0.0.1:8099"

[[auth_profiles]]
id = "patreon-default"
provider = "patreon"
mode = "username_password"
username_env = "PATREON_USERNAME"
password_env = "PATREON_PASSWORD"
totp_secret_env = "PATREON_TOTP_SECRET"
session_path = %q

[[publishers]]
id = "local-files"
kind = "filesystem"
path = %q
enabled = true

[[sources]]
id = "example-creator"
provider = "patreon"
url = "https://www.patreon.com/c/ExampleCreator/posts"
auth_profile = "patreon-default"
enabled = true

[[rules]]
source = "example-creator"
priority = 10
match_type = "fallback"
match_value = ""
track_key = "main-series"
track_name = "Main Series"
release_role = "chapter"
content_strategy = "attachment_preferred"
attachment_glob = ["*.epub", "*.pdf"]
attachment_priority = ["epub", "pdf"]
`, storeDSN, artifactRoot, supportRoot, sessionPath, publishPath)
}

func getenvDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func normalizePublisherKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "exec", "command":
		return "exec"
	default:
		return strings.ToLower(strings.TrimSpace(kind))
	}
}

func normalizeAuthMode(mode string) string {
	return strings.ToLower(strings.TrimSpace(mode))
}

func normalizeSchedulerMode(mode string) string {
	return strings.ToLower(strings.TrimSpace(mode))
}

func shouldUseContainerRoots() bool {
	if value := strings.ToLower(strings.TrimSpace(os.Getenv(ContainerModeEnv))); value != "" {
		return value == "1" || value == "true" || value == "yes"
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	return false
}
