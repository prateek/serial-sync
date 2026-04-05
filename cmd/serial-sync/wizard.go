package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/prateek/serial-sync/internal/app"
	"github.com/prateek/serial-sync/internal/config"
)

type wizardSpec struct {
	Path          string
	SourceURL     string
	SourceID      string
	AuthProfileID string
	PublisherID   string
	PublisherPath string
	TrackKey      string
	TrackName     string
	BootstrapAuth bool
	Sample        bool
}

func (cmd *WizardCmd) Run() error {
	roots, err := config.DefaultRoots()
	if err != nil {
		return err
	}
	spec, err := cmd.collectWizardSpec(roots)
	if err != nil {
		return err
	}
	if !cmd.Force {
		if _, err := os.Stat(spec.Path); err == nil {
			return fmt.Errorf("config already exists at %s; rerun with --force to overwrite", spec.Path)
		}
	}
	cfg := buildWizardConfig(spec, roots)
	payload, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(spec.Path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(spec.Path, payload, 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote config to %s\n", spec.Path)

	if spec.BootstrapAuth || spec.Sample {
		if err := withService(spec.Path, func(ctx context.Context, service *app.Service) error {
			if spec.BootstrapAuth {
				result, err := service.BootstrapAuth(ctx, spec.SourceID, spec.AuthProfileID, false, "wizard auth bootstrap")
				fmt.Println(app.FormatAuthBootstrapResult(result))
				if err != nil {
					return err
				}
			}
			if spec.Sample {
				result, err := service.Sync(ctx, spec.SourceID, true, "wizard sample")
				fmt.Println(app.FormatSyncResult(result))
				if err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}

	fmt.Println("next steps:")
	fmt.Printf("  serial-sync --config %s auth bootstrap --source %s\n", spec.Path, spec.SourceID)
	fmt.Printf("  serial-sync --config %s run once --source %s --target %s\n", spec.Path, spec.SourceID, spec.PublisherID)
	return nil
}

func (cmd *WizardCmd) collectWizardSpec(roots config.Roots) (wizardSpec, error) {
	defaultPath, err := config.DefaultConfigPath()
	if err != nil {
		return wizardSpec{}, err
	}
	spec := wizardSpec{
		Path:          firstNonEmptyString(cmd.Path, defaultPath),
		SourceURL:     strings.TrimSpace(cmd.SourceURL),
		SourceID:      strings.TrimSpace(cmd.SourceID),
		AuthProfileID: firstNonEmptyString(strings.TrimSpace(cmd.AuthProfileID), "patreon-default"),
		PublisherID:   firstNonEmptyString(strings.TrimSpace(cmd.PublisherID), "local-files"),
		PublisherPath: firstNonEmptyString(strings.TrimSpace(cmd.PublisherPath), filepath.Join(roots.StateDir, "published")),
		TrackKey:      firstNonEmptyString(strings.TrimSpace(cmd.TrackKey), "main-series"),
		TrackName:     strings.TrimSpace(cmd.TrackName),
		BootstrapAuth: cmd.BootstrapAuth,
		Sample:        cmd.Sample,
	}
	if cmd.NonInteractive {
		if spec.SourceURL == "" {
			return wizardSpec{}, errors.New("--source-url is required in --non-interactive mode")
		}
		return finalizeWizardSpec(spec)
	}

	reader := bufio.NewReader(os.Stdin)
	spec.Path = promptWithDefault(reader, "Config path", spec.Path)
	spec.SourceURL = promptRequired(reader, "Patreon source URL", spec.SourceURL)
	spec.SourceID = promptWithDefault(reader, "Source ID", spec.SourceID)
	spec.AuthProfileID = promptWithDefault(reader, "Auth profile ID", spec.AuthProfileID)
	spec.PublisherID = promptWithDefault(reader, "Publisher ID", spec.PublisherID)
	spec.PublisherPath = promptWithDefault(reader, "Publisher path", spec.PublisherPath)
	spec.TrackKey = promptWithDefault(reader, "Fallback track key", spec.TrackKey)
	spec.TrackName = promptWithDefault(reader, "Fallback track name", spec.TrackName)
	spec.BootstrapAuth = promptYesNo(reader, "Bootstrap auth now", spec.BootstrapAuth)
	spec.Sample = promptYesNo(reader, "Run a dry-run sample sync now", spec.Sample)
	return finalizeWizardSpec(spec)
}

func finalizeWizardSpec(spec wizardSpec) (wizardSpec, error) {
	spec.SourceURL = strings.TrimSpace(spec.SourceURL)
	if spec.SourceURL == "" {
		return wizardSpec{}, errors.New("wizard requires a Patreon source URL")
	}
	if spec.SourceID == "" {
		spec.SourceID = deriveWizardSourceID(spec.SourceURL)
	}
	if spec.TrackName == "" {
		spec.TrackName = humanizeIdentifier(spec.SourceID)
	}
	if spec.SourceID == "" {
		return wizardSpec{}, errors.New("wizard could not derive a source ID; pass --source-id")
	}
	return spec, nil
}

func buildWizardConfig(spec wizardSpec, roots config.Roots) *config.Config {
	cfg := &config.Config{
		AuthProfiles: []config.AuthProfile{
			{
				ID:            spec.AuthProfileID,
				Provider:      "patreon",
				Mode:          "username_password",
				UsernameEnv:   "PATREON_USERNAME",
				PasswordEnv:   "PATREON_PASSWORD",
				TOTPSecretEnv: "PATREON_TOTP_SECRET",
				SessionPath:   filepath.Join(roots.StateDir, "sessions", spec.AuthProfileID+".json"),
			},
		},
		Publishers: []config.PublisherConfig{
			{
				ID:      spec.PublisherID,
				Kind:    "filesystem",
				Path:    spec.PublisherPath,
				Enabled: true,
			},
		},
		Sources: []config.SourceConfig{
			{
				ID:          spec.SourceID,
				Provider:    "patreon",
				URL:         spec.SourceURL,
				AuthProfile: spec.AuthProfileID,
				Enabled:     true,
			},
		},
		Rules: []config.RuleConfig{
			{
				Source:             spec.SourceID,
				Priority:           10,
				MatchType:          "fallback",
				TrackKey:           spec.TrackKey,
				TrackName:          spec.TrackName,
				ReleaseRole:        "chapter",
				ContentStrategy:    "attachment_preferred",
				AttachmentGlob:     []string{"*.epub", "*.pdf"},
				AttachmentPriority: []string{"epub", "pdf"},
			},
		},
	}
	cfg.ApplyDefaults(roots)
	return cfg
}

func promptWithDefault(reader *bufio.Reader, label, current string) string {
	if current == "" {
		fmt.Printf("%s: ", label)
	} else {
		fmt.Printf("%s [%s]: ", label, current)
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		return current
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return current
	}
	return line
}

func promptRequired(reader *bufio.Reader, label, current string) string {
	for {
		value := promptWithDefault(reader, label, current)
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
}

func promptYesNo(reader *bufio.Reader, label string, current bool) bool {
	defaultLabel := "n"
	if current {
		defaultLabel = "y"
	}
	for {
		fmt.Printf("%s [y/N default %s]: ", label, defaultLabel)
		line, err := reader.ReadString('\n')
		if err != nil {
			return current
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "":
			return current
		case "y", "yes":
			return true
		case "n", "no":
			return false
		}
	}
}

func deriveWizardSourceID(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return ""
	}
	if parsed, err := url.Parse(trimmed); err == nil {
		parts := strings.FieldsFunc(parsed.Path, func(r rune) bool { return r == '/' })
		if len(parts) >= 2 {
			return slugifyIdentifier(parts[len(parts)-2])
		}
		if len(parts) == 1 {
			return slugifyIdentifier(parts[0])
		}
	}
	base := filepath.Base(trimmed)
	base = strings.TrimSpace(strings.Trim(base, "/"))
	return slugifyIdentifier(base)
}

func humanizeIdentifier(input string) string {
	parts := strings.FieldsFunc(input, func(r rune) bool {
		return r == '-' || r == '_' || r == ' '
	})
	if len(parts) == 0 {
		return "Main Series"
	}
	for idx := range parts {
		parts[idx] = strings.Title(parts[idx])
	}
	return strings.Join(parts, " ")
}

func slugifyIdentifier(input string) string {
	input = strings.TrimSpace(strings.ToLower(input))
	if input == "" {
		return ""
	}
	var builder strings.Builder
	lastDash := false
	for _, r := range input {
		isAlphaNum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlphaNum {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
