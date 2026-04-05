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
