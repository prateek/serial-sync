package app_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/prateek/serial-sync/internal/app"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/provider"
	"github.com/prateek/serial-sync/internal/provider/patreon"
	"github.com/prateek/serial-sync/internal/store/sqlite"
)

func TestSyncAndPublishLifecycle(t *testing.T) {
	t.Parallel()

	repoRoot := findRepoRoot(t)
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	configBody := `[runtime]
log_level = "info"
log_format = "text"
store_driver = "sqlite"
store_dsn = "` + filepath.Join(tmp, "state.db") + `"
artifact_root = "` + filepath.Join(tmp, "artifacts") + `"
support_root = "` + filepath.Join(tmp, "support") + `"

[[auth_profiles]]
id = "patreon-default"
provider = "patreon"
mode = "fixture"
session_path = "` + filepath.Join(tmp, "sessions", "patreon-default.json") + `"

[[publishers]]
id = "local-files"
kind = "filesystem"
path = "` + filepath.Join(tmp, "publish") + `"
enabled = true

[[sources]]
id = "plum-parrot"
provider = "patreon"
url = "https://www.patreon.com/c/PlumParrot/posts"
auth_profile = "patreon-default"
fixture_dir = "` + filepath.Join(repoRoot, "testdata", "fixtures", "patreon", "plum-parrot") + `"
enabled = true

[[sources]]
id = "actus"
provider = "patreon"
url = "https://www.patreon.com/c/Actus/posts"
auth_profile = "patreon-default"
fixture_dir = "` + filepath.Join(repoRoot, "testdata", "fixtures", "patreon", "actus") + `"
enabled = true

[[rules]]
source = "plum-parrot"
priority = 10
match_type = "tag"
match_value = "AA3"
track_key = "andy-again-3"
track_name = "Andy, Again 3"
release_role = "chapter"
content_strategy = "attachment_preferred"
attachment_glob = ["*.epub", "*.pdf"]
attachment_priority = ["epub", "pdf"]

[[rules]]
source = "plum-parrot"
priority = 20
match_type = "tag"
match_value = "AO2"
track_key = "aura-overload-2"
track_name = "Aura Overload 2"
release_role = "chapter"
content_strategy = "attachment_preferred"
attachment_glob = ["*.pdf"]
attachment_priority = ["pdf"]

[[rules]]
source = "actus"
priority = 10
match_type = "title_regex"
match_value = "^Nightmare Realm Summoner\\s+-\\s+Chapter\\s+"
track_key = "nightmare-realm-summoner"
track_name = "Nightmare Realm Summoner"
release_role = "chapter"
content_strategy = "text_post"
`
	if err := os.WriteFile(configPath, []byte(configBody), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, roots, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.EnsureDirs(roots, cfg); err != nil {
		t.Fatal(err)
	}
	repo, err := sqlite.Open(cfg.Runtime.StoreDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	service := app.New(cfg, roots, configPath, repo, provider.NewRegistry(patreon.New()))

	plan, err := service.Sync(context.Background(), "", true, "plan sync")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Discovered != 6 || plan.MaterializedArtifacts != 4 {
		t.Fatalf("unexpected dry-run summary: %#v", plan)
	}

	firstSync, err := service.Sync(context.Background(), "", false, "sync")
	if err != nil {
		t.Fatal(err)
	}
	if firstSync.Changed != 6 || firstSync.MaterializedArtifacts != 4 {
		t.Fatalf("unexpected sync summary: %#v", firstSync)
	}

	secondSync, err := service.Sync(context.Background(), "", false, "sync")
	if err != nil {
		t.Fatal(err)
	}
	if secondSync.Changed != 0 || secondSync.Unchanged != 6 {
		t.Fatalf("expected a no-op second sync, got %#v", secondSync)
	}

	firstPublish, err := service.Publish(context.Background(), "", "", false, "publish")
	if err != nil {
		t.Fatal(err)
	}
	if firstPublish.Published != 4 || firstPublish.Failed != 0 {
		t.Fatalf("unexpected first publish result: %#v", firstPublish)
	}

	secondPublish, err := service.Publish(context.Background(), "", "", false, "publish")
	if err != nil {
		t.Fatal(err)
	}
	if secondPublish.Published != 0 || secondPublish.Skipped != 4 {
		t.Fatalf("unexpected second publish result: %#v", secondPublish)
	}
}

func TestExecPublishLifecycle(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("exec publisher test uses a POSIX shell script")
	}

	repoRoot := findRepoRoot(t)
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "hook.log")
	scriptPath := filepath.Join(tmp, "publish-hook.sh")
	scriptBody := `#!/bin/sh
set -eu
[ -f "$SERIAL_SYNC_ARTIFACT_PATH" ]
[ -f "$SERIAL_SYNC_METADATA_JSON_PATH" ]
printf '%s|%s|%s|%s\n' \
  "$SERIAL_SYNC_TARGET_ID" \
  "$SERIAL_SYNC_TARGET_KIND" \
  "$SERIAL_SYNC_ARTIFACT_ID" \
  "$SERIAL_SYNC_ARTIFACT_FILENAME" >> "$1"
cat >/dev/null
`
	if err := os.WriteFile(scriptPath, []byte(scriptBody), 0o755); err != nil {
		t.Fatal(err)
	}

	configPath := filepath.Join(tmp, "config.toml")
	configBody := `[runtime]
log_level = "info"
log_format = "text"
store_driver = "sqlite"
store_dsn = "` + filepath.Join(tmp, "state.db") + `"
artifact_root = "` + filepath.Join(tmp, "artifacts") + `"
support_root = "` + filepath.Join(tmp, "support") + `"

[[auth_profiles]]
id = "patreon-default"
provider = "patreon"
mode = "fixture"
session_path = "` + filepath.Join(tmp, "sessions", "patreon-default.json") + `"

[[publishers]]
id = "post-publish-hook"
kind = "exec"
command = ["` + scriptPath + `", "` + logPath + `"]
enabled = true

[[sources]]
id = "plum-parrot"
provider = "patreon"
url = "https://www.patreon.com/c/PlumParrot/posts"
auth_profile = "patreon-default"
fixture_dir = "` + filepath.Join(repoRoot, "testdata", "fixtures", "patreon", "plum-parrot") + `"
enabled = true

[[sources]]
id = "actus"
provider = "patreon"
url = "https://www.patreon.com/c/Actus/posts"
auth_profile = "patreon-default"
fixture_dir = "` + filepath.Join(repoRoot, "testdata", "fixtures", "patreon", "actus") + `"
enabled = true

[[rules]]
source = "plum-parrot"
priority = 10
match_type = "tag"
match_value = "AA3"
track_key = "andy-again-3"
track_name = "Andy, Again 3"
release_role = "chapter"
content_strategy = "attachment_preferred"
attachment_glob = ["*.epub", "*.pdf"]
attachment_priority = ["epub", "pdf"]

[[rules]]
source = "plum-parrot"
priority = 20
match_type = "tag"
match_value = "AO2"
track_key = "aura-overload-2"
track_name = "Aura Overload 2"
release_role = "chapter"
content_strategy = "attachment_preferred"
attachment_glob = ["*.pdf"]
attachment_priority = ["pdf"]

[[rules]]
source = "actus"
priority = 10
match_type = "title_regex"
match_value = "^Nightmare Realm Summoner\\s+-\\s+Chapter\\s+"
track_key = "nightmare-realm-summoner"
track_name = "Nightmare Realm Summoner"
release_role = "chapter"
content_strategy = "text_post"
`
	if err := os.WriteFile(configPath, []byte(configBody), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, roots, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.EnsureDirs(roots, cfg); err != nil {
		t.Fatal(err)
	}
	repo, err := sqlite.Open(cfg.Runtime.StoreDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	service := app.New(cfg, roots, configPath, repo, provider.NewRegistry(patreon.New()))

	if _, err := service.Sync(context.Background(), "", false, "sync"); err != nil {
		t.Fatal(err)
	}

	dryRun, err := service.Publish(context.Background(), "", "", true, "publish")
	if err != nil {
		t.Fatal(err)
	}
	if dryRun.Published != 4 || len(dryRun.Items) != 4 {
		t.Fatalf("unexpected exec dry-run result: %#v", dryRun)
	}
	for _, item := range dryRun.Items {
		if item.Action != "planned" || item.TargetKind != "exec" {
			t.Fatalf("unexpected dry-run item: %#v", item)
		}
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not invoke the exec hook, stat err = %v", err)
	}

	firstPublish, err := service.Publish(context.Background(), "", "", false, "publish")
	if err != nil {
		t.Fatal(err)
	}
	if firstPublish.Published != 4 || firstPublish.Failed != 0 || len(firstPublish.Items) != 4 {
		t.Fatalf("unexpected exec publish result: %#v", firstPublish)
	}

	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(logData)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) != 4 {
		t.Fatalf("expected 4 exec hook invocations, got %d (%q)", len(lines), string(logData))
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "post-publish-hook|exec|art_") {
			t.Fatalf("unexpected exec hook line %q", line)
		}
	}

	secondPublish, err := service.Publish(context.Background(), "", "", false, "publish")
	if err != nil {
		t.Fatal(err)
	}
	if secondPublish.Published != 0 || secondPublish.Skipped != 4 {
		t.Fatalf("unexpected second exec publish result: %#v", secondPublish)
	}

	logDataAfter, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(logDataAfter) != string(logData) {
		t.Fatalf("expected second publish to skip exec hook replays")
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}
