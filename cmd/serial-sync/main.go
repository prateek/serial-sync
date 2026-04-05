package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/alecthomas/kong"

	"github.com/prateek/serial-sync/internal/app"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/provider"
	"github.com/prateek/serial-sync/internal/provider/patreon"
	runtimedaemon "github.com/prateek/serial-sync/internal/runtime/daemon"
	"github.com/prateek/serial-sync/internal/store/sqlite"
)

type CLI struct {
	ConfigPath string `name:"config" short:"c" help:"Path to config file."`

	Init          InitCmd          `cmd:"" help:"Write an example config file."`
	Config        ConfigCmd        `cmd:"" help:"Validate configuration."`
	Auth          AuthCmd          `cmd:"" help:"Auth bootstrap and session helpers."`
	Source        SourceCmd        `cmd:"" help:"Inspect configured sources."`
	Track         TrackCmd         `cmd:"" help:"Inspect a track."`
	Release       ReleaseCmd       `cmd:"" help:"Inspect a release."`
	Artifact      ArtifactCmd      `cmd:"" help:"Inspect an artifact."`
	PublishRecord PublishRecordCmd `cmd:"" name:"publish-record" help:"Inspect publish records."`
	Runs          RunsCmd          `cmd:"" name:"runs" help:"Inspect a prior run."`
	Support       SupportCmd       `cmd:"" help:"Support bundle helpers."`
	Plan          PlanCmd          `cmd:"" help:"Show planned sync work."`
	Run           RunCmd           `cmd:"" help:"Run end-to-end workflows."`
	Sync          SyncCmd          `cmd:"" help:"Run a sync."`
	Publish       PublishCmd       `cmd:"" help:"Publish canonical artifacts."`
	Wizard        WizardCmd        `cmd:"" help:"Interactive setup wizard."`
	Daemon        DaemonCmd        `cmd:"" help:"Background scheduler."`
}

type InitCmd struct {
	Path  string `help:"Write config to this path."`
	Force bool   `help:"Overwrite an existing config."`
}

type ConfigCmd struct {
	Validate ConfigValidateCmd `cmd:"" help:"Validate config and print the loaded counts."`
}

type AuthCmd struct {
	Bootstrap     AuthBootstrapCmd     `cmd:"" help:"Create or verify provider session state."`
	ImportSession AuthImportSessionCmd `cmd:"" help:"Import a provider session bundle and validate it."`
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

type PublishRecordCmd struct {
	List    PublishRecordListCmd    `cmd:"" help:"List publish records."`
	Inspect PublishRecordInspectCmd `cmd:"" help:"Inspect a publish record."`
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

type AuthImportSessionCmd struct {
	SessionFile   string `arg:"" name:"session-file" help:"Path to the session bundle JSON file."`
	SourceID      string `name:"source" help:"Limit session import validation to one source."`
	AuthProfileID string `name:"auth-profile" help:"Limit session import validation to one auth profile."`
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

type PublishRecordListCmd struct {
	SourceID string `name:"source" help:"Limit records to one source."`
	TargetID string `name:"target" help:"Limit records to one publish target."`
}

type PublishRecordInspectCmd struct {
	Record string `arg:"" name:"publish-record" help:"Publish record ID to inspect."`
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

type WizardCmd struct {
	Path           string `name:"path" help:"Write the generated config to this path."`
	Force          bool   `name:"force" help:"Overwrite an existing config file."`
	SourceURL      string `name:"source-url" help:"Patreon source URL to configure."`
	SourceID       string `name:"source-id" help:"Source ID to write into the config."`
	AuthProfileID  string `name:"auth-profile" help:"Auth profile ID to write into the config."`
	PublisherID    string `name:"publisher" help:"Filesystem publisher ID to write into the config."`
	PublisherPath  string `name:"publisher-path" help:"Filesystem publish path."`
	TrackKey       string `name:"track-key" help:"Starter fallback track key."`
	TrackName      string `name:"track-name" help:"Starter fallback track name."`
	BootstrapAuth  bool   `name:"bootstrap-auth" help:"Bootstrap auth immediately after writing the config."`
	Sample         bool   `name:"sample" help:"Run a dry-run sync after writing the config."`
	NonInteractive bool   `name:"non-interactive" help:"Fail instead of prompting for missing fields."`
}

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

func (cmd *AuthImportSessionCmd) Run(cli *CLI) error {
	return withService(cli.ConfigPath, func(ctx context.Context, service *app.Service) error {
		result, err := service.ImportAuthSession(ctx, cmd.SourceID, cmd.AuthProfileID, cmd.SessionFile, "auth import-session")
		fmt.Println(app.FormatAuthImportResult(result))
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

func (cmd *PublishRecordListCmd) Run(cli *CLI) error {
	return withService(cli.ConfigPath, func(ctx context.Context, service *app.Service) error {
		records, err := service.ListPublishRecords(ctx, cmd.SourceID, cmd.TargetID)
		if err != nil {
			return err
		}
		for _, record := range records {
			fmt.Printf(
				"%s\t%s\t%s\t%s\t%s\t%s\n",
				record.Record.ID,
				record.Source.ID,
				record.Record.TargetID,
				record.Record.Status,
				record.Record.PublishedAt.Format(time.RFC3339),
				record.Artifact.ID,
			)
		}
		return nil
	})
}

func (cmd *PublishRecordInspectCmd) Run(cli *CLI) error {
	return withService(cli.ConfigPath, func(ctx context.Context, service *app.Service) error {
		payload, err := service.InspectPublishRecord(ctx, cmd.Record)
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
		leaseTTLValue := service.Config.Scheduler.LeaseTTL
		if leaseTTLValue == "" {
			leaseTTLValue = "30m"
		}
		leaseTTL, err := time.ParseDuration(leaseTTLValue)
		if err != nil {
			return fmt.Errorf("invalid daemon lease ttl %q: %w", leaseTTLValue, err)
		}
		if leaseTTL <= 0 {
			return fmt.Errorf("daemon lease ttl must be positive")
		}
		sources := enabledSources(service.Config.Sources, cmd.SourceID)
		if len(sources) == 0 {
			return fmt.Errorf("no enabled sources match %q", cmd.SourceID)
		}
		sourceIDs := make([]string, 0, len(sources))
		for _, source := range sources {
			sourceIDs = append(sourceIDs, source.ID)
		}
		holderID := daemonHolderID()
		state := runtimedaemon.NewState(holderID, duration, sourceIDs)
		server, err := runtimedaemon.Start(ctx, service.Config.Scheduler.HealthAddr, state)
		if err != nil {
			return fmt.Errorf("start daemon health server: %w", err)
		}
		defer func() {
			if server != nil {
				_ = server.Close()
			}
		}()
		for {
			for _, source := range sources {
				state.MarkRunStart(source.ID)
				acquired, leaseErr := service.Repo.AcquireLease(ctx, "source:"+source.ID, holderID, leaseTTL)
				if leaseErr != nil {
					state.MarkRunFailure(source.ID, leaseErr)
					fmt.Fprintf(os.Stderr, "daemon lease failed for %s: %v\n", source.ID, leaseErr)
					continue
				}
				if !acquired {
					state.MarkLeaseSkipped(source.ID)
					fmt.Fprintf(os.Stderr, "daemon skipped %s: lease held by another worker\n", source.ID)
					continue
				}
				result, runErr := service.RunOnce(ctx, source.ID, cmd.TargetID, "daemon")
				if releaseErr := service.Repo.ReleaseLease(context.Background(), "source:"+source.ID, holderID); releaseErr != nil {
					fmt.Fprintf(os.Stderr, "daemon lease release failed for %s: %v\n", source.ID, releaseErr)
				}
				if runErr != nil {
					state.MarkRunFailure(source.ID, runErr)
					fmt.Fprintf(os.Stderr, "daemon cycle failed for %s: %v\n", source.ID, runErr)
					continue
				}
				state.MarkRunSuccess(
					source.ID,
					result.Sync.RunID,
					result.Publish.RunID,
					result.Sync.Discovered,
					result.Sync.Changed,
					result.Sync.MaterializedArtifacts,
					result.Publish.Published,
					result.Publish.Failed,
				)
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

func daemonHolderID() string {
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "serial-sync"
	}
	return hostname + "-" + strconv.Itoa(os.Getpid())
}

func enabledSources(all []config.SourceConfig, sourceFilter string) []config.SourceConfig {
	items := make([]config.SourceConfig, 0, len(all))
	for _, source := range all {
		if !source.Enabled {
			continue
		}
		if strings.TrimSpace(sourceFilter) != "" && source.ID != sourceFilter {
			continue
		}
		items = append(items, source)
	}
	return items
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
