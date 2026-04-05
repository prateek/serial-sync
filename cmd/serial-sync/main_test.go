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
			args: []string{"--config", wantPath, "config", "validate"},
		},
		{
			name: "after command",
			args: []string{"config", "validate", "--config", wantPath},
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
			if got, want := ctx.Command(), "config validate"; got != want {
				t.Fatalf("ctx.Command() = %q, want %q", got, want)
			}
			if got, want := cli.ConfigPath, wantPath; got != want {
				t.Fatalf("cli.ConfigPath = %q, want %q", got, want)
			}
		})
	}
}

func TestPlanSyncCommandShape(t *testing.T) {
	t.Parallel()

	cli := CLI{}
	parser, err := newParser(&cli, io.Discard, io.Discard, func(int) {})
	if err != nil {
		t.Fatalf("newParser() error = %v", err)
	}

	ctx, err := parser.Parse([]string{"plan", "sync", "--source", "plum-parrot"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got, want := ctx.Command(), "plan sync"; got != want {
		t.Fatalf("ctx.Command() = %q, want %q", got, want)
	}
	if got, want := cli.Plan.Sync.SourceID, "plum-parrot"; got != want {
		t.Fatalf("cli.Plan.Sync.SourceID = %q, want %q", got, want)
	}
}

func TestRunOnceCommandShape(t *testing.T) {
	t.Parallel()

	cli := CLI{}
	parser, err := newParser(&cli, io.Discard, io.Discard, func(int) {})
	if err != nil {
		t.Fatalf("newParser() error = %v", err)
	}

	ctx, err := parser.Parse([]string{"run", "once", "--source", "plum-parrot", "--target", "local-files"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got, want := ctx.Command(), "run once"; got != want {
		t.Fatalf("ctx.Command() = %q, want %q", got, want)
	}
	if got, want := cli.Run.Once.SourceID, "plum-parrot"; got != want {
		t.Fatalf("cli.Run.Once.SourceID = %q, want %q", got, want)
	}
	if got, want := cli.Run.Once.TargetID, "local-files"; got != want {
		t.Fatalf("cli.Run.Once.TargetID = %q, want %q", got, want)
	}
}

func TestAuthBootstrapCommandShape(t *testing.T) {
	t.Parallel()

	cli := CLI{}
	parser, err := newParser(&cli, io.Discard, io.Discard, func(int) {})
	if err != nil {
		t.Fatalf("newParser() error = %v", err)
	}

	ctx, err := parser.Parse([]string{"auth", "bootstrap", "--auth-profile", "patreon-default", "--force"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got, want := ctx.Command(), "auth bootstrap"; got != want {
		t.Fatalf("ctx.Command() = %q, want %q", got, want)
	}
	if got, want := cli.Auth.Bootstrap.AuthProfileID, "patreon-default"; got != want {
		t.Fatalf("cli.Auth.Bootstrap.AuthProfileID = %q, want %q", got, want)
	}
	if !cli.Auth.Bootstrap.Force {
		t.Fatal("expected force flag to be set")
	}
}

func TestRunsInspectCommandShape(t *testing.T) {
	t.Parallel()

	cli := CLI{}
	parser, err := newParser(&cli, io.Discard, io.Discard, func(int) {})
	if err != nil {
		t.Fatalf("newParser() error = %v", err)
	}

	ctx, err := parser.Parse([]string{"runs", "inspect", "run_123"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got, want := ctx.Command(), "runs inspect <run-id>"; got != want {
		t.Fatalf("ctx.Command() = %q, want %q", got, want)
	}
	if got, want := cli.Runs.Inspect.RunID, "run_123"; got != want {
		t.Fatalf("cli.Runs.Inspect.RunID = %q, want %q", got, want)
	}
}
