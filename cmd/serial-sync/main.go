package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/alecthomas/kong"

	"github.com/prateek/serial-sync/internal/app"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/provider"
	"github.com/prateek/serial-sync/internal/provider/patreon"
	"github.com/prateek/serial-sync/internal/store/sqlite"
)

type CLI struct {
	ConfigPath string `name:"config" short:"c" help:"Path to config file."`

	Init     InitCmd     `cmd:"" help:"Write an example config file."`
	Config   ConfigCmd   `cmd:"" help:"Validate configuration."`
	Auth     AuthCmd     `cmd:"" help:"Auth bootstrap and session helpers."`
	Source   SourceCmd   `cmd:"" help:"Inspect configured sources."`
	Track    TrackCmd    `cmd:"" help:"Inspect a track."`
	Release  ReleaseCmd  `cmd:"" help:"Inspect a release."`
	Artifact ArtifactCmd `cmd:"" help:"Inspect an artifact."`
	Runs     RunsCmd     `cmd:"" name:"runs" help:"Inspect a prior run."`
	Support  SupportCmd  `cmd:"" help:"Support bundle helpers."`
	Plan     PlanCmd     `cmd:"" help:"Show planned sync work."`
	Run      RunCmd      `cmd:"" help:"Run end-to-end workflows."`
	Sync     SyncCmd     `cmd:"" help:"Run a sync."`
	Publish  PublishCmd  `cmd:"" help:"Publish canonical artifacts."`
	Wizard   WizardCmd   `cmd:"" help:"Interactive setup wizard."`
	Daemon   DaemonCmd   `cmd:"" help:"Background scheduler."`
}

type InitCmd struct {
	Path  string `help:"Write config to this path."`
	Force bool   `help:"Overwrite an existing config."`
}

type ConfigCmd struct {
	Validate ConfigValidateCmd `cmd:"" help:"Validate config and print the loaded counts."`
}

type AuthCmd struct {
	Bootstrap AuthBootstrapCmd `cmd:"" help:"Create or verify provider session state."`
}

type SourceCmd struct {
	List    SourceListCmd    `cmd:"" help:"List configured sources."`
	Inspect SourceInspectCmd `cmd:"" help:"Inspect a configured source."`
}

type TrackCmd struct {
	Inspect TrackInspectCmd `cmd:"" help:"Inspect a track."`
}

type ReleaseCmd struct {
	Inspect ReleaseInspectCmd `cmd:"" help:"Inspect a release."`
}

type ArtifactCmd struct {
	Inspect ArtifactInspectCmd `cmd:"" help:"Inspect an artifact."`
}

type RunsCmd struct {
	Inspect RunInspectCmd `cmd:"" help:"Inspect a prior run."`
}

type RunCmd struct {
	Once RunOnceCmd `cmd:"" help:"Run sync and publish back-to-back."`
}

type SupportCmd struct {
	Bundle SupportBundleCmd `cmd:"" help:"Export a support bundle for a run."`
}

type PlanCmd struct {
	Sync PlanSyncCmd `cmd:"" help:"Show sync work without mutating state."`
}

type ConfigValidateCmd struct{}

type AuthBootstrapCmd struct {
	SourceID      string `name:"source" help:"Limit auth bootstrap to one source."`
	AuthProfileID string `name:"auth-profile" help:"Limit auth bootstrap to one auth profile."`
	Force         bool   `name:"force" help:"Discard any existing session and log in again."`
}

type SourceListCmd struct{}

type SourceInspectCmd struct {
	Source string `arg:"" name:"source" help:"Source ID to inspect."`
}

type TrackInspectCmd struct {
	Track string `arg:"" name:"track" help:"Track ID to inspect."`
}

type ReleaseInspectCmd struct {
	Release string `arg:"" name:"release" help:"Release ID to inspect."`
}

type ArtifactInspectCmd struct {
	Artifact string `arg:"" name:"artifact" help:"Artifact ID to inspect."`
}

type RunInspectCmd struct {
	RunID string `arg:"" name:"run-id" help:"Run ID to inspect."`
}

type SupportBundleCmd struct {
	RunID string `arg:"" name:"run-id" help:"Run ID to export."`
}

type RunOnceCmd struct {
	SourceID string `name:"source" help:"Limit the run to one source."`
	TargetID string `name:"target" help:"Limit publish to one target."`
}

type PlanSyncCmd struct {
	SourceID string `name:"source" help:"Limit planning to one source."`
}

type SyncCmd struct {
	SourceID string `name:"source" help:"Limit sync to one source."`
	DryRun   bool   `name:"dry-run" help:"Show planned actions without mutating state."`
}

type PublishCmd struct {
	SourceID string `name:"source" help:"Limit publish to one source."`
	TargetID string `name:"target" help:"Limit publish to one target."`
	DryRun   bool   `name:"dry-run" help:"Show planned publish actions without mutating state."`
}

type WizardCmd struct{}

type DaemonCmd struct {
	SourceID     string `name:"source" help:"Limit daemon runs to one source."`
	TargetID     string `name:"target" help:"Limit daemon publishes to one target."`
	PollInterval string `name:"poll-interval" help:"Override scheduler poll interval for daemon mode."`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cli := CLI{}
	parser, err := newParser(&cli, os.Stdout, os.Stderr, nil)
	if err != nil {
		return err
	}
	args = normalizeHelpArgs(args)
	if len(args) == 0 {
		_, _ = parser.Parse([]string{"--help"})
		return nil
	}
	if wantsHelp(args) {
		_, _ = parser.Parse(args)
		return nil
	}
	ctx, err := parser.Parse(args)
	if err != nil {
		return err
	}
	return ctx.Run(&cli)
}

func (cmd *InitCmd) Run() error {
	path := cmd.Path
	if path == "" {
		defaultPath, err := config.DefaultConfigPath()
		if err != nil {
			return err
		}
		path = defaultPath
	}
	if !cmd.Force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("config already exists at %s; rerun with --force to overwrite", path)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(config.ExampleConfig()), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote example config to %s\n", path)
	return nil
}

func (cmd *ConfigValidateCmd) Run(cli *CLI) error {
	return withService(cli.ConfigPath, func(_ context.Context, service *app.Service) error {
		fmt.Printf(
			"config ok: %d source(s), %d rule(s), %d publisher(s)\n",
			len(service.Config.Sources),
			len(service.Config.Rules),
			len(service.Config.Publishers),
		)
		return nil
	})
}

func (cmd *AuthBootstrapCmd) Run(cli *CLI) error {
	return withService(cli.ConfigPath, func(ctx context.Context, service *app.Service) error {
		result, err := service.BootstrapAuth(ctx, cmd.SourceID, cmd.AuthProfileID, cmd.Force, "auth bootstrap")
		fmt.Println(app.FormatAuthBootstrapResult(result))
		return err
	})
}

func (cmd *SourceListCmd) Run(cli *CLI) error {
	return withService(cli.ConfigPath, func(_ context.Context, service *app.Service) error {
		for _, source := range service.Config.Sources {
			status := "disabled"
			if source.Enabled {
				status = "enabled"
			}
			fmt.Printf("%s\t%s\t%s\t%s\n", source.ID, source.Provider, status, source.URL)
		}
		return nil
	})
}

func (cmd *SourceInspectCmd) Run(cli *CLI) error {
	return withService(cli.ConfigPath, func(ctx context.Context, service *app.Service) error {
		payload, err := service.InspectSource(ctx, cmd.Source)
		if err != nil {
			return err
		}
		return printJSON(payload)
	})
}

func (cmd *TrackInspectCmd) Run(cli *CLI) error {
	return withService(cli.ConfigPath, func(ctx context.Context, service *app.Service) error {
		payload, err := service.InspectTrack(ctx, cmd.Track)
		if err != nil {
			return err
		}
		return printJSON(payload)
	})
}

func (cmd *ReleaseInspectCmd) Run(cli *CLI) error {
	return withService(cli.ConfigPath, func(ctx context.Context, service *app.Service) error {
		payload, err := service.InspectRelease(ctx, cmd.Release)
		if err != nil {
			return err
		}
		return printJSON(payload)
	})
}

func (cmd *ArtifactInspectCmd) Run(cli *CLI) error {
	return withService(cli.ConfigPath, func(ctx context.Context, service *app.Service) error {
		payload, err := service.InspectArtifact(ctx, cmd.Artifact)
		if err != nil {
			return err
		}
		return printJSON(payload)
	})
}

func (cmd *RunInspectCmd) Run(cli *CLI) error {
	return withService(cli.ConfigPath, func(ctx context.Context, service *app.Service) error {
		payload, err := service.InspectRun(ctx, cmd.RunID)
		if err != nil {
			return err
		}
		return printJSON(payload)
	})
}

func (cmd *SupportBundleCmd) Run(cli *CLI) error {
	return withService(cli.ConfigPath, func(ctx context.Context, service *app.Service) error {
		dir, err := service.SupportBundle(ctx, cmd.RunID)
		if err != nil {
			return err
		}
		fmt.Println(dir)
		return nil
	})
}

func (cmd *RunOnceCmd) Run(cli *CLI) error {
	return withService(cli.ConfigPath, func(ctx context.Context, service *app.Service) error {
		result, err := service.RunOnce(ctx, cmd.SourceID, cmd.TargetID, "run once")
		if err != nil {
			return err
		}
		fmt.Println(app.FormatRunOnceResult(result))
		return nil
	})
}

func (cmd *PlanSyncCmd) Run(cli *CLI) error {
	return withService(cli.ConfigPath, func(ctx context.Context, service *app.Service) error {
		result, err := service.Sync(ctx, cmd.SourceID, true, "plan sync")
		if err != nil {
			return err
		}
		fmt.Println(app.FormatSyncResult(result))
		return nil
	})
}

func (cmd *SyncCmd) Run(cli *CLI) error {
	return withService(cli.ConfigPath, func(ctx context.Context, service *app.Service) error {
		result, err := service.Sync(ctx, cmd.SourceID, cmd.DryRun, "sync")
		if err != nil {
			return err
		}
		fmt.Println(app.FormatSyncResult(result))
		return nil
	})
}

func (cmd *PublishCmd) Run(cli *CLI) error {
	return withService(cli.ConfigPath, func(ctx context.Context, service *app.Service) error {
		result, err := service.Publish(ctx, cmd.SourceID, cmd.TargetID, cmd.DryRun, "publish")
		if err != nil {
			return err
		}
		fmt.Println(app.FormatPublishResult(result))
		return nil
	})
}

func (cmd *WizardCmd) Run() error {
	return app.NotImplemented("wizard")
}

func (cmd *DaemonCmd) Run(cli *CLI) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return withServiceContext(ctx, cli.ConfigPath, func(ctx context.Context, service *app.Service) error {
		interval := service.Config.Scheduler.PollInterval
		if cmd.PollInterval != "" {
			interval = cmd.PollInterval
		}
		if interval == "" {
			interval = "1h"
		}
		duration, err := time.ParseDuration(interval)
		if err != nil {
			return fmt.Errorf("invalid daemon poll interval %q: %w", interval, err)
		}
		if duration <= 0 {
			return fmt.Errorf("daemon poll interval must be positive")
		}
		for {
			result, err := service.RunOnce(ctx, cmd.SourceID, cmd.TargetID, "daemon")
			if err != nil {
				fmt.Fprintf(os.Stderr, "daemon cycle failed: %v\n", err)
			} else {
				fmt.Println(app.FormatRunOnceResult(result))
			}
			timer := time.NewTimer(duration)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
			}
		}
	})
}

func newParser(cli *CLI, stdout, stderr io.Writer, exit func(int)) (*kong.Kong, error) {
	options := []kong.Option{
		kong.Name("serial-sync"),
		kong.Description("Sync serialized releases into local artifacts and publish targets."),
		kong.UsageOnError(),
		kong.Writers(stdout, stderr),
	}
	if exit != nil {
		options = append(options, kong.Exit(exit))
	}
	return kong.New(cli, options...)
}

func withService(configPath string, fn func(context.Context, *app.Service) error) error {
	return withServiceContext(context.Background(), configPath, fn)
}

func withServiceContext(ctx context.Context, configPath string, fn func(context.Context, *app.Service) error) error {
	service, cleanup, err := bootstrap(configPath)
	if err != nil {
		return err
	}
	defer cleanup()
	return fn(ctx, service)
}

func bootstrap(configPath string) (*app.Service, func(), error) {
	cfg, roots, err := config.Load(configPath)
	if err != nil {
		return nil, nil, err
	}
	if err := config.EnsureDirs(roots, cfg); err != nil {
		return nil, nil, err
	}
	repo, err := sqlite.Open(cfg.Runtime.StoreDSN)
	if err != nil {
		return nil, nil, err
	}
	if err := repo.EnsureSchema(context.Background()); err != nil {
		_ = repo.Close()
		return nil, nil, err
	}
	registry := provider.NewRegistry(patreon.New())
	path := configPath
	if path == "" {
		path, _ = config.DefaultConfigPath()
	}
	service := app.New(cfg, roots, path, repo, registry)
	return service, func() {
		_ = repo.Close()
	}, nil
}

func printJSON(value any) error {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(payload))
	return nil
}

func normalizeHelpArgs(args []string) []string {
	if len(args) == 0 || args[0] != "help" {
		return args
	}
	if len(args) == 1 {
		return []string{"--help"}
	}
	normalized := make([]string, 0, len(args))
	normalized = append(normalized, args[1:]...)
	return append(normalized, "--help")
}

func wantsHelp(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return false
}
