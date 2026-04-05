package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/prateek/serial-sync/internal/app"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/provider"
	"github.com/prateek/serial-sync/internal/provider/patreon"
	"github.com/prateek/serial-sync/internal/store/sqlite"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	configPath, rest, err := parseGlobalArgs(args)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		printUsage()
		return nil
	}
	ctx := context.Background()
	switch rest[0] {
	case "init":
		return runInit(rest[1:])
	case "config":
		if len(rest) < 2 || rest[1] != "validate" {
			return errors.New("usage: serial-sync config validate")
		}
		service, cleanup, err := bootstrap(configPath)
		if err != nil {
			return err
		}
		defer cleanup()
		fmt.Printf("config ok: %d source(s), %d rule(s), %d publisher(s)\n", len(service.Config.Sources), len(service.Config.Rules), len(service.Config.Publishers))
		return nil
	case "source":
		return runSource(ctx, configPath, rest[1:])
	case "track":
		return runTrack(ctx, configPath, rest[1:])
	case "release":
		return runRelease(ctx, configPath, rest[1:])
	case "artifact":
		return runArtifact(ctx, configPath, rest[1:])
	case "run":
		return runRun(ctx, configPath, rest[1:])
	case "support":
		return runSupport(ctx, configPath, rest[1:])
	case "plan":
		if len(rest) < 2 || rest[1] != "sync" {
			return errors.New("usage: serial-sync plan sync [--source id]")
		}
		fs := flag.NewFlagSet("plan sync", flag.ContinueOnError)
		sourceID := fs.String("source", "", "limit planning to one source")
		if err := fs.Parse(rest[2:]); err != nil {
			return err
		}
		service, cleanup, err := bootstrap(configPath)
		if err != nil {
			return err
		}
		defer cleanup()
		result, err := service.Sync(ctx, *sourceID, true, "plan sync")
		if err != nil {
			return err
		}
		fmt.Println(app.FormatSyncResult(result))
		return nil
	case "sync":
		fs := flag.NewFlagSet("sync", flag.ContinueOnError)
		sourceID := fs.String("source", "", "limit sync to one source")
		dryRun := fs.Bool("dry-run", false, "show planned actions without mutating state")
		if err := fs.Parse(rest[1:]); err != nil {
			return err
		}
		service, cleanup, err := bootstrap(configPath)
		if err != nil {
			return err
		}
		defer cleanup()
		result, err := service.Sync(ctx, *sourceID, *dryRun, "sync")
		if err != nil {
			return err
		}
		fmt.Println(app.FormatSyncResult(result))
		return nil
	case "publish":
		fs := flag.NewFlagSet("publish", flag.ContinueOnError)
		sourceID := fs.String("source", "", "limit publish to one source")
		targetID := fs.String("target", "", "limit publish to one target")
		dryRun := fs.Bool("dry-run", false, "show planned publish actions without mutating state")
		if err := fs.Parse(rest[1:]); err != nil {
			return err
		}
		service, cleanup, err := bootstrap(configPath)
		if err != nil {
			return err
		}
		defer cleanup()
		result, err := service.Publish(ctx, *sourceID, *targetID, *dryRun, "publish")
		if err != nil {
			return err
		}
		fmt.Println(app.FormatPublishResult(result))
		return nil
	case "wizard":
		return app.NotImplemented("wizard")
	case "daemon":
		return app.NotImplemented("daemon")
	case "help", "--help", "-h":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", rest[0])
	}
}

func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	path := fs.String("path", "", "write config to a specific path")
	force := fs.Bool("force", false, "overwrite an existing config")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" {
		defaultPath, err := config.DefaultConfigPath()
		if err != nil {
			return err
		}
		*path = defaultPath
	}
	if !*force {
		if _, err := os.Stat(*path); err == nil {
			return fmt.Errorf("config already exists at %s; rerun with --force to overwrite", *path)
		}
	}
	if err := os.MkdirAll(filepath.Dir(*path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(*path, []byte(config.ExampleConfig()), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote example config to %s\n", *path)
	return nil
}

func runSource(ctx context.Context, configPath string, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: serial-sync source list|inspect <source>")
	}
	service, cleanup, err := bootstrap(configPath)
	if err != nil {
		return err
	}
	defer cleanup()
	switch args[0] {
	case "list":
		for _, source := range service.Config.Sources {
			status := "disabled"
			if source.Enabled {
				status = "enabled"
			}
			fmt.Printf("%s\t%s\t%s\t%s\n", source.ID, source.Provider, status, source.URL)
		}
		return nil
	case "inspect":
		if len(args) < 2 {
			return errors.New("usage: serial-sync source inspect <source>")
		}
		payload, err := service.InspectSource(ctx, args[1])
		if err != nil {
			return err
		}
		return printJSON(payload)
	default:
		return errors.New("usage: serial-sync source list|inspect <source>")
	}
}

func runTrack(ctx context.Context, configPath string, args []string) error {
	if len(args) < 2 || args[0] != "inspect" {
		return errors.New("usage: serial-sync track inspect <track>")
	}
	service, cleanup, err := bootstrap(configPath)
	if err != nil {
		return err
	}
	defer cleanup()
	payload, err := service.InspectTrack(ctx, args[1])
	if err != nil {
		return err
	}
	return printJSON(payload)
}

func runRelease(ctx context.Context, configPath string, args []string) error {
	if len(args) < 2 || args[0] != "inspect" {
		return errors.New("usage: serial-sync release inspect <release>")
	}
	service, cleanup, err := bootstrap(configPath)
	if err != nil {
		return err
	}
	defer cleanup()
	payload, err := service.InspectRelease(ctx, args[1])
	if err != nil {
		return err
	}
	return printJSON(payload)
}

func runArtifact(ctx context.Context, configPath string, args []string) error {
	if len(args) < 2 || args[0] != "inspect" {
		return errors.New("usage: serial-sync artifact inspect <artifact>")
	}
	service, cleanup, err := bootstrap(configPath)
	if err != nil {
		return err
	}
	defer cleanup()
	payload, err := service.InspectArtifact(ctx, args[1])
	if err != nil {
		return err
	}
	return printJSON(payload)
}

func runRun(ctx context.Context, configPath string, args []string) error {
	if len(args) < 2 || args[0] != "inspect" {
		return errors.New("usage: serial-sync run inspect <run-id>")
	}
	service, cleanup, err := bootstrap(configPath)
	if err != nil {
		return err
	}
	defer cleanup()
	payload, err := service.InspectRun(ctx, args[1])
	if err != nil {
		return err
	}
	return printJSON(payload)
}

func runSupport(ctx context.Context, configPath string, args []string) error {
	if len(args) < 2 || args[0] != "bundle" {
		return errors.New("usage: serial-sync support bundle <run-id>")
	}
	service, cleanup, err := bootstrap(configPath)
	if err != nil {
		return err
	}
	defer cleanup()
	dir, err := service.SupportBundle(ctx, args[1])
	if err != nil {
		return err
	}
	fmt.Println(dir)
	return nil
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

func parseGlobalArgs(args []string) (string, []string, error) {
	var configPath string
	var rest []string
	for idx := 0; idx < len(args); idx++ {
		arg := args[idx]
		switch {
		case arg == "--config":
			if idx+1 >= len(args) {
				return "", nil, errors.New("--config requires a value")
			}
			configPath = args[idx+1]
			idx++
		case strings.HasPrefix(arg, "--config="):
			configPath = strings.TrimPrefix(arg, "--config=")
		default:
			rest = append(rest, arg)
		}
	}
	return configPath, rest, nil
}

func printJSON(value any) error {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(payload))
	return nil
}

func printUsage() {
	fmt.Print(`serial-sync

Usage:
  serial-sync init [--path <config-path>] [--force]
  serial-sync config validate
  serial-sync source list
  serial-sync source inspect <source>
  serial-sync track inspect <track>
  serial-sync release inspect <release>
  serial-sync artifact inspect <artifact>
  serial-sync run inspect <run-id>
  serial-sync support bundle <run-id>
  serial-sync plan sync [--source <source>]
  serial-sync sync [--source <source>] [--dry-run]
  serial-sync publish [--source <source>] [--target <target>] [--dry-run]
  serial-sync wizard
  serial-sync daemon
`)
}
