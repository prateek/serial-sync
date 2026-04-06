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

	Setup SetupCmd `cmd:"" help:"Bootstrap config, auth, and series-authoring workspaces."`
	Run   RunCmd   `cmd:"" help:"Run the normal sync+publish workflow, or the daemon."`
	Debug DebugCmd `cmd:"" help:"Inspect prior runs, publish records, and support bundles."`
}

type SetupCmd struct {
	Init    SetupInitCmd    `cmd:"" help:"Write an example config file."`
	Check   SetupCheckCmd   `cmd:"" help:"Validate config and print the loaded counts. Replaces 'config validate'."`
	Auth    SetupAuthCmd    `cmd:"" help:"Create, verify, or import provider session state. Replaces 'auth bootstrap' and 'auth import-session'."`
	Dump    SetupDumpCmd    `cmd:"" help:"Dump creator posts into a local series-authoring workspace. Replaces 'source dump'."`
	Preview SetupPreviewCmd `cmd:"" help:"Preview how a series file classifies a dumped workspace."`
}

type DebugCmd struct {
	Runs      DebugRunsCmd      `cmd:"" help:"List recent runs."`
	Run       DebugRunCmd       `cmd:"" help:"Summarize one run for operator forensics. Replaces 'runs explain'."`
	Events    DebugEventsCmd    `cmd:"" help:"List filtered events for a run. Replaces 'runs events'."`
	Publishes DebugPublishesCmd `cmd:"" help:"List publish records. Replaces 'publish-record list'."`
	Publish   DebugPublishCmd   `cmd:"" help:"Inspect a publish record. Replaces 'publish-record inspect'."`
	Bundle    DebugBundleCmd    `cmd:"" help:"Export a support bundle for a run. Replaces 'support bundle'."`
}

type SetupInitCmd struct {
	Path  string `help:"Write config to this path."`
	Force bool   `help:"Overwrite an existing config."`
}

type SetupCheckCmd struct{}

type SetupAuthCmd struct {
	SourceID      string `name:"source" help:"Limit auth work to one source."`
	AuthProfileID string `name:"auth-profile" help:"Limit auth work to one auth profile."`
	ImportSession string `name:"import-session" help:"Import this session bundle JSON file instead of bootstrapping auth."`
	Force         bool   `name:"force" help:"Discard any existing session and log in again during bootstrap."`
}

type SetupDumpCmd struct {
	AuthProfileID string   `name:"auth-profile" help:"Auth profile to use for Patreon discovery."`
	Path          string   `name:"path" help:"Write the dump workspace to this path."`
	Membership    string   `name:"membership" default:"paid" enum:"paid,free,trial,all" help:"Limit dumping to this membership kind."`
	Creators      []string `name:"creator" help:"Limit the dump to creator handle, source id, or creator name. Repeat the flag for multiple authors."`
	Force         bool     `name:"force" help:"Overwrite an existing workspace path."`
}

type SetupPreviewCmd struct {
	Workspace string   `name:"workspace" help:"Path to a source dump workspace."`
	SeriesFile string  `name:"series-file" help:"Path to a series TOML file. Defaults to <workspace>/series.toml."`
	Creators  []string `name:"creator" help:"Limit preview to specific dumped creators. Repeat the flag for multiple authors."`
	ShowPosts bool     `name:"show-posts" help:"Include per-post classification output in text mode."`
	Format    string   `name:"format" default:"text" enum:"text,json" help:"Output format."`
}

type DebugRunsCmd struct {
	Limit int `name:"limit" default:"20" help:"Show this many recent runs."`
}

type DebugRunCmd struct {
	RunID string `arg:"" name:"run-id" help:"Run ID to explain."`
}

type DebugEventsCmd struct {
	RunID      string `arg:"" name:"run-id" help:"Run ID to inspect."`
	Level      string `name:"level" help:"Filter events by level."`
	Component  string `name:"component" help:"Filter events by component."`
	EntityKind string `name:"entity-kind" help:"Filter events by entity kind."`
	EntityID   string `name:"entity-id" help:"Filter events by entity ID."`
	Limit      int    `name:"limit" default:"0" help:"Limit the number of events shown after filtering."`
}

type RunCmd struct {
	Exec   RunExecCmd `cmd:"" default:"withargs" hidden:""`
	Daemon DaemonCmd  `cmd:"" help:"Background scheduler that repeats the same run pipeline."`
}

type RunExecCmd struct {
	SourceID string `name:"source" help:"Limit the run to one source."`
	TargetID string `name:"target" help:"Limit publish to one target."`
	DryRun   bool   `name:"dry-run" help:"Show the sync plan without mutating state or publishing."`
}

type DebugPublishesCmd struct {
	SourceID string `name:"source" help:"Limit records to one source."`
	TargetID string `name:"target" help:"Limit records to one publish target."`
}

type DebugPublishCmd struct {
	Record string `arg:"" name:"publish-record" help:"Publish record ID to inspect."`
}

type DebugBundleCmd struct {
	RunID string `arg:"" name:"run-id" help:"Run ID to export."`
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
		printRootHelp(os.Stdout)
		return nil
	}
	if wantsRunHelp(args) {
		printRunHelp(os.Stdout)
		return nil
	}
	if wantsHelp(args) {
		if isRootHelp(args) {
			printRootHelp(os.Stdout)
			return nil
		}
		_, _ = parser.Parse(args)
		return nil
	}
	ctx, err := parser.Parse(args)
	if err != nil {
		return err
	}
	return ctx.Run(&cli)
}

func (cmd *SetupInitCmd) Run() error {
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

func (cmd *SetupCheckCmd) Run(cli *CLI) error {
	return withService(cli.ConfigPath, func(_ context.Context, service *app.Service) error {
		fmt.Printf(
			"config ok: %d source(s), %d series, %d publisher(s)\n",
			len(service.Config.Sources),
			len(service.Config.Series),
			len(service.Config.Publishers),
		)
		return nil
	})
}

func (cmd *SetupAuthCmd) Run(cli *CLI) error {
	return withService(cli.ConfigPath, func(ctx context.Context, service *app.Service) error {
		if strings.TrimSpace(cmd.ImportSession) != "" {
			result, err := service.ImportAuthSession(ctx, cmd.SourceID, cmd.AuthProfileID, cmd.ImportSession, "setup auth --import-session")
			fmt.Println(app.FormatAuthImportResult(result))
			return err
		}
		result, err := service.BootstrapAuth(ctx, cmd.SourceID, cmd.AuthProfileID, cmd.Force, "setup auth")
		fmt.Println(app.FormatAuthBootstrapResult(result))
		return err
	})
}

func (cmd *SetupDumpCmd) Run(cli *CLI) error {
	return withService(cli.ConfigPath, func(ctx context.Context, service *app.Service) error {
		result, err := service.DumpSources(ctx, cmd.AuthProfileID, app.SourceDumpOptions{
			Path:             cmd.Path,
			MembershipFilter: cmd.Membership,
			CreatorFilters:   trimStrings(cmd.Creators),
			Force:            cmd.Force,
		}, "setup dump")
		if err != nil {
			return err
		}
		fmt.Println(app.FormatSourceDumpResult(result))
		return nil
	})
}

func (cmd *SetupPreviewCmd) Run(cli *CLI) error {
	return withService(cli.ConfigPath, func(ctx context.Context, service *app.Service) error {
		result, err := service.PreviewRules(ctx, app.RulesPreviewOptions{
			WorkspacePath:  cmd.Workspace,
			SeriesFile:     cmd.SeriesFile,
			CreatorFilters: trimStrings(cmd.Creators),
			ShowPosts:      cmd.ShowPosts,
		}, "setup preview")
		if err != nil {
			return err
		}
		switch strings.ToLower(strings.TrimSpace(cmd.Format)) {
		case "json":
			return printJSON(result)
		default:
			fmt.Println(app.FormatRulesPreviewResult(result, cmd.ShowPosts))
			return nil
		}
	})
}

func (cmd *DebugPublishesCmd) Run(cli *CLI) error {
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

func (cmd *DebugPublishCmd) Run(cli *CLI) error {
	return withService(cli.ConfigPath, func(ctx context.Context, service *app.Service) error {
		payload, err := service.InspectPublishRecord(ctx, cmd.Record)
		if err != nil {
			return err
		}
		return printJSON(payload)
	})
}

func (cmd *DebugRunsCmd) Run(cli *CLI) error {
	return withService(cli.ConfigPath, func(ctx context.Context, service *app.Service) error {
		runs, err := service.ListRuns(ctx, cmd.Limit)
		if err != nil {
			return err
		}
		for _, run := range runs {
			finishedAt := ""
			if run.FinishedAt != nil {
				finishedAt = run.FinishedAt.Format(time.RFC3339)
			}
			fmt.Printf("%s\t%s\t%s\t%s\t%s\t%t\t%s\n", run.ID, run.Status, run.Command, run.StartedAt.Format(time.RFC3339), finishedAt, run.DryRun, run.Summary)
		}
		return nil
	})
}

func (cmd *DebugEventsCmd) Run(cli *CLI) error {
	return withService(cli.ConfigPath, func(ctx context.Context, service *app.Service) error {
		payload, err := service.ListRunEvents(ctx, cmd.RunID, app.RunEventFilter{
			Level:      cmd.Level,
			Component:  cmd.Component,
			EntityKind: cmd.EntityKind,
			EntityID:   cmd.EntityID,
			Limit:      cmd.Limit,
		})
		if err != nil {
			return err
		}
		return printJSON(payload)
	})
}

func (cmd *DebugRunCmd) Run(cli *CLI) error {
	return withService(cli.ConfigPath, func(ctx context.Context, service *app.Service) error {
		payload, err := service.ExplainRun(ctx, cmd.RunID)
		if err != nil {
			return err
		}
		fmt.Println(app.FormatRunForensics(*payload))
		return nil
	})
}

func (cmd *DebugBundleCmd) Run(cli *CLI) error {
	return withService(cli.ConfigPath, func(ctx context.Context, service *app.Service) error {
		dir, err := service.SupportBundle(ctx, cmd.RunID)
		if err != nil {
			return err
		}
		fmt.Println(dir)
		return nil
	})
}

func (cmd *RunExecCmd) Run(cli *CLI) error {
	return withService(cli.ConfigPath, func(ctx context.Context, service *app.Service) error {
		if cmd.DryRun {
			result, err := service.Sync(ctx, cmd.SourceID, true, "run --dry-run")
			if err != nil {
				return err
			}
			fmt.Println(app.FormatSyncResult(result))
			fmt.Println("publish skipped because --dry-run only previews sync classification and materialization")
			return nil
		}
		result, err := service.RunOnce(ctx, cmd.SourceID, cmd.TargetID, "run")
		if err != nil {
			return err
		}
		fmt.Println(app.FormatRunOnceResult(result))
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

func trimStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			out = append(out, part)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func newParser(cli *CLI, stdout, stderr io.Writer, exit func(int)) (*kong.Kong, error) {
	options := []kong.Option{
		kong.Name("serial-sync"),
		kong.Description("Use `setup` to bootstrap config/auth/series, `run` for execution, and `debug` for forensics."),
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

func isRootHelp(args []string) bool {
	return len(args) == 1 && (args[0] == "--help" || args[0] == "-h")
}

func wantsRunHelp(args []string) bool {
	return len(args) >= 1 && args[0] == "run" && wantsHelp(args[1:])
}

func printRootHelp(w io.Writer) {
	fmt.Fprint(w, `Usage: serial-sync <command> [flags]

Use `+"`setup`"+` to bootstrap config/auth/series, `+"`run`"+` for execution, and `+"`debug`"+` for
forensics.

Flags:
  -h, --help             Show context-sensitive help.
  -c, --config=STRING    Path to config file.

Commands:
  setup init [flags]
    Write an example config file.

  setup check
    Validate config and print the loaded counts. Replaces 'config validate'.

  setup auth [flags]
    Create, verify, or import provider session state. Replaces 'auth bootstrap'
    and 'auth import-session'.

  setup dump [flags]
    Dump creator posts into a local series-authoring workspace. Replaces 'source
    dump'.

  setup preview [flags]
    Preview how a series file classifies a dumped workspace.

  run [flags]
    Run the normal sync-plus-publish workflow.

  run daemon [flags]
    Background scheduler that repeats the same run pipeline.

  debug runs [flags]
    List recent runs.

  debug run <run-id>
    Summarize one run for operator forensics. Replaces 'runs explain'.

  debug events <run-id> [flags]
    List filtered events for a run. Replaces 'runs events'.

  debug publishes [flags]
    List publish records. Replaces 'publish-record list'.

  debug publish <publish-record>
    Inspect a publish record. Replaces 'publish-record inspect'.

  debug bundle <run-id>
    Export a support bundle for a run. Replaces 'support bundle'.

Run "serial-sync <command> --help" for more information on a command.
`)
}

func printRunHelp(w io.Writer) {
	fmt.Fprint(w, `Usage: serial-sync run [flags]
       serial-sync run daemon [flags]

Run the normal sync-plus-publish workflow by default. Use `+"`--dry-run`"+` to preview
the sync classification/materialization step without mutating state or
publishing.

Flags:
  -h, --help             Show context-sensitive help.
      --source=STRING    Limit the run to one source.
      --target=STRING    Limit publish to one target.
      --dry-run          Show the sync plan without mutating state or publishing.

Commands:
  run daemon [flags]
    Background scheduler that repeats the same run pipeline.
`)
}
