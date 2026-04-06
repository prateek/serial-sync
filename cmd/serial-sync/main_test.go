package main

import (
	"io"
	"path/filepath"
	"testing"
)

func TestGlobalConfigFlagParsesBeforeOrAfterCommand(t *testing.T) {
	t.Parallel()

	wantPath, err := filepath.Abs(filepath.Join("..", "..", "examples", "config.demo.toml"))
	if err != nil {
		t.Fatalf("filepath.Abs() error = %v", err)
	}

	testCases := []struct {
		name string
		args []string
	}{
		{
			name: "before command",
			args: []string{"--config", wantPath, "setup", "check"},
		},
		{
			name: "after command",
			args: []string{"setup", "check", "--config", wantPath},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cli := CLI{}
			parser, err := newParser(&cli, io.Discard, io.Discard, func(int) {})
			if err != nil {
				t.Fatalf("newParser() error = %v", err)
			}

			ctx, err := parser.Parse(tc.args)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if got, want := ctx.Command(), "setup check"; got != want {
				t.Fatalf("ctx.Command() = %q, want %q", got, want)
			}
			if got, want := cli.ConfigPath, wantPath; got != want {
				t.Fatalf("cli.ConfigPath = %q, want %q", got, want)
			}
		})
	}
}

func TestRunCommandShape(t *testing.T) {
	t.Parallel()

	cli := CLI{}
	parser, err := newParser(&cli, io.Discard, io.Discard, func(int) {})
	if err != nil {
		t.Fatalf("newParser() error = %v", err)
	}

	ctx, err := parser.Parse([]string{"run", "--source", "plum-parrot", "--target", "local-files"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got, want := ctx.Command(), "run exec"; got != want {
		t.Fatalf("ctx.Command() = %q, want %q", got, want)
	}
	if got, want := cli.Run.Exec.SourceID, "plum-parrot"; got != want {
		t.Fatalf("cli.Run.Exec.SourceID = %q, want %q", got, want)
	}
	if got, want := cli.Run.Exec.TargetID, "local-files"; got != want {
		t.Fatalf("cli.Run.Exec.TargetID = %q, want %q", got, want)
	}
}

func TestRunDryRunCommandShape(t *testing.T) {
	t.Parallel()

	cli := CLI{}
	parser, err := newParser(&cli, io.Discard, io.Discard, func(int) {})
	if err != nil {
		t.Fatalf("newParser() error = %v", err)
	}

	ctx, err := parser.Parse([]string{"run", "--source", "plum-parrot", "--dry-run"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got, want := ctx.Command(), "run exec"; got != want {
		t.Fatalf("ctx.Command() = %q, want %q", got, want)
	}
	if got, want := cli.Run.Exec.SourceID, "plum-parrot"; got != want {
		t.Fatalf("cli.Run.Exec.SourceID = %q, want %q", got, want)
	}
	if !cli.Run.Exec.DryRun {
		t.Fatal("expected dry-run flag to be set")
	}
}

func TestRunDaemonCommandShape(t *testing.T) {
	t.Parallel()

	cli := CLI{}
	parser, err := newParser(&cli, io.Discard, io.Discard, func(int) {})
	if err != nil {
		t.Fatalf("newParser() error = %v", err)
	}

	ctx, err := parser.Parse([]string{"run", "daemon", "--source", "plum-parrot", "--target", "local-files", "--poll-interval", "5m"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got, want := ctx.Command(), "run daemon"; got != want {
		t.Fatalf("ctx.Command() = %q, want %q", got, want)
	}
	if got, want := cli.Run.Daemon.SourceID, "plum-parrot"; got != want {
		t.Fatalf("cli.Run.Daemon.SourceID = %q, want %q", got, want)
	}
	if got, want := cli.Run.Daemon.TargetID, "local-files"; got != want {
		t.Fatalf("cli.Run.Daemon.TargetID = %q, want %q", got, want)
	}
	if got, want := cli.Run.Daemon.PollInterval, "5m"; got != want {
		t.Fatalf("cli.Run.Daemon.PollInterval = %q, want %q", got, want)
	}
}

func TestSetupAuthBootstrapCommandShape(t *testing.T) {
	t.Parallel()

	cli := CLI{}
	parser, err := newParser(&cli, io.Discard, io.Discard, func(int) {})
	if err != nil {
		t.Fatalf("newParser() error = %v", err)
	}

	ctx, err := parser.Parse([]string{"setup", "auth", "--auth-profile", "patreon-default", "--force"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got, want := ctx.Command(), "setup auth"; got != want {
		t.Fatalf("ctx.Command() = %q, want %q", got, want)
	}
	if got, want := cli.Setup.Auth.AuthProfileID, "patreon-default"; got != want {
		t.Fatalf("cli.Setup.Auth.AuthProfileID = %q, want %q", got, want)
	}
	if !cli.Setup.Auth.Force {
		t.Fatal("expected force flag to be set")
	}
}

func TestSetupAuthImportCommandShape(t *testing.T) {
	t.Parallel()

	cli := CLI{}
	parser, err := newParser(&cli, io.Discard, io.Discard, func(int) {})
	if err != nil {
		t.Fatalf("newParser() error = %v", err)
	}

	ctx, err := parser.Parse([]string{"setup", "auth", "--import-session", "/tmp/patreon.json", "--auth-profile", "patreon-default"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got, want := ctx.Command(), "setup auth"; got != want {
		t.Fatalf("ctx.Command() = %q, want %q", got, want)
	}
	if got, want := cli.Setup.Auth.ImportSession, "/tmp/patreon.json"; got != want {
		t.Fatalf("cli.Setup.Auth.ImportSession = %q, want %q", got, want)
	}
}

func TestSetupDumpCommandShape(t *testing.T) {
	t.Parallel()

	cli := CLI{}
	parser, err := newParser(&cli, io.Discard, io.Discard, func(int) {})
	if err != nil {
		t.Fatalf("newParser() error = %v", err)
	}

	ctx, err := parser.Parse([]string{"setup", "dump", "--auth-profile", "patreon-default", "--membership", "paid", "--creator", "actus", "--path", "./workspace", "--force"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got, want := ctx.Command(), "setup dump"; got != want {
		t.Fatalf("ctx.Command() = %q, want %q", got, want)
	}
	if got, want := cli.Setup.Dump.AuthProfileID, "patreon-default"; got != want {
		t.Fatalf("cli.Setup.Dump.AuthProfileID = %q, want %q", got, want)
	}
	if got, want := cli.Setup.Dump.Path, "./workspace"; got != want {
		t.Fatalf("cli.Setup.Dump.Path = %q, want %q", got, want)
	}
	if !cli.Setup.Dump.Force {
		t.Fatal("expected force flag to be set")
	}
}

func TestSetupPreviewCommandShape(t *testing.T) {
	t.Parallel()

	cli := CLI{}
	parser, err := newParser(&cli, io.Discard, io.Discard, func(int) {})
	if err != nil {
		t.Fatalf("newParser() error = %v", err)
	}

	ctx, err := parser.Parse([]string{"setup", "preview", "--workspace", "./workspace", "--series-file", "./series.toml", "--creator", "actus", "--show-posts", "--format", "json"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got, want := ctx.Command(), "setup preview"; got != want {
		t.Fatalf("ctx.Command() = %q, want %q", got, want)
	}
	if got, want := cli.Setup.Preview.Workspace, "./workspace"; got != want {
		t.Fatalf("cli.Setup.Preview.Workspace = %q, want %q", got, want)
	}
	if got, want := cli.Setup.Preview.SeriesFile, "./series.toml"; got != want {
		t.Fatalf("cli.Setup.Preview.SeriesFile = %q, want %q", got, want)
	}
	if got, want := len(cli.Setup.Preview.Creators), 1; got != want {
		t.Fatalf("len(cli.Setup.Preview.Creators) = %d, want %d", got, want)
	}
	if !cli.Setup.Preview.ShowPosts {
		t.Fatal("expected show-posts flag to be set")
	}
	if got, want := cli.Setup.Preview.Format, "json"; got != want {
		t.Fatalf("cli.Setup.Preview.Format = %q, want %q", got, want)
	}
}

func TestDebugCommandShapes(t *testing.T) {
	t.Parallel()

	cli := CLI{}
	parser, err := newParser(&cli, io.Discard, io.Discard, func(int) {})
	if err != nil {
		t.Fatalf("newParser() error = %v", err)
	}

	runsCtx, err := parser.Parse([]string{"debug", "runs", "--limit", "5"})
	if err != nil {
		t.Fatalf("Parse(runs) error = %v", err)
	}
	if got, want := runsCtx.Command(), "debug runs"; got != want {
		t.Fatalf("runsCtx.Command() = %q, want %q", got, want)
	}
	if got, want := cli.Debug.Runs.Limit, 5; got != want {
		t.Fatalf("cli.Debug.Runs.Limit = %d, want %d", got, want)
	}

	runCtx, err := parser.Parse([]string{"debug", "run", "run_123"})
	if err != nil {
		t.Fatalf("Parse(run) error = %v", err)
	}
	if got, want := runCtx.Command(), "debug run <run-id>"; got != want {
		t.Fatalf("runCtx.Command() = %q, want %q", got, want)
	}
	if got, want := cli.Debug.Run.RunID, "run_123"; got != want {
		t.Fatalf("cli.Debug.Run.RunID = %q, want %q", got, want)
	}

	eventsCtx, err := parser.Parse([]string{"debug", "events", "run_123", "--level", "error", "--component", "publish"})
	if err != nil {
		t.Fatalf("Parse(events) error = %v", err)
	}
	if got, want := eventsCtx.Command(), "debug events <run-id>"; got != want {
		t.Fatalf("eventsCtx.Command() = %q, want %q", got, want)
	}
	if got, want := cli.Debug.Events.RunID, "run_123"; got != want {
		t.Fatalf("cli.Debug.Events.RunID = %q, want %q", got, want)
	}
	if got, want := cli.Debug.Events.Component, "publish"; got != want {
		t.Fatalf("cli.Debug.Events.Component = %q, want %q", got, want)
	}

	publishesCtx, err := parser.Parse([]string{"debug", "publishes", "--source", "plum-parrot", "--target", "local-files"})
	if err != nil {
		t.Fatalf("Parse(publishes) error = %v", err)
	}
	if got, want := publishesCtx.Command(), "debug publishes"; got != want {
		t.Fatalf("publishesCtx.Command() = %q, want %q", got, want)
	}
	if got, want := cli.Debug.Publishes.SourceID, "plum-parrot"; got != want {
		t.Fatalf("cli.Debug.Publishes.SourceID = %q, want %q", got, want)
	}

	publishCtx, err := parser.Parse([]string{"debug", "publish", "pub_123"})
	if err != nil {
		t.Fatalf("Parse(publish) error = %v", err)
	}
	if got, want := publishCtx.Command(), "debug publish <publish-record>"; got != want {
		t.Fatalf("publishCtx.Command() = %q, want %q", got, want)
	}
	if got, want := cli.Debug.Publish.Record, "pub_123"; got != want {
		t.Fatalf("cli.Debug.Publish.Record = %q, want %q", got, want)
	}

	bundleCtx, err := parser.Parse([]string{"debug", "bundle", "run_123"})
	if err != nil {
		t.Fatalf("Parse(bundle) error = %v", err)
	}
	if got, want := bundleCtx.Command(), "debug bundle <run-id>"; got != want {
		t.Fatalf("bundleCtx.Command() = %q, want %q", got, want)
	}
	if got, want := cli.Debug.Bundle.RunID, "run_123"; got != want {
		t.Fatalf("cli.Debug.Bundle.RunID = %q, want %q", got, want)
	}
}
