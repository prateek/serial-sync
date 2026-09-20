package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/prateek/serial-sync/internal/app"
)

func TestRebuildPreviewAndExecutionExcludeDisabledSources(t *testing.T) {
	root := t.TempDir()
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		t.Setenv(key, filepath.Join(root, key))
	}
	fixture, err := filepath.Abs("../../testdata/fixtures/patreon/actus")
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.toml")
	writeConfig := func(revised bool) {
		t.Helper()
		body := fmt.Sprintf(`[runtime]
store_dsn=%q
artifact_root=%q
log_root=%q
support_root=%q
[[auth_profiles]]
id="fixture"
provider="patreon"
mode="fixture"
session_path=%q
[[publishers]]
id="library"
kind="filesystem"
path=%q
enabled=true
`, filepath.Join(root, "state.db"), filepath.Join(root, "artifacts"), filepath.Join(root, "logs"), filepath.Join(root, "support"), filepath.Join(root, "session.json"), filepath.Join(root, "library"))
		for _, id := range []string{"alpha", "beta"} {
			title := id
			if revised {
				title += " revised"
			}
			body += fmt.Sprintf(`[[sources]]
id=%q
provider="patreon"
url="https://www.patreon.com/c/Actus/posts"
auth_profile="fixture"
fixture_dir=%q
enabled=%t
[[series]]
id=%q
title=%q
[series.output]
format="preserve"
[[series.inputs]]
source=%q
match_type="title_regex"
match_value="^Nightmare"
release_role="chapter"
content_strategy="text_post"
`, id, fixture, !(revised && id == "beta"), id, title, id)
		}
		if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig(false)
	if output, err := captureRun(t, "--config", configPath, "run"); err != nil {
		t.Fatalf("seed library: %s %v", output, err)
	}
	betaRoot := filepath.Join(root, "library", "beta")
	betaBefore := treeHashes(t, betaRoot)
	writeConfig(true)
	before := treeHashes(t, root)
	for _, dryRun := range []bool{true, false} {
		args := []string{"--config", configPath, "run", "--rebuild"}
		if dryRun {
			args = append(args, "--dry-run")
		}
		output, err := captureRun(t, args...)
		if err != nil {
			t.Fatalf("rebuild dry_run=%t: %s %v", dryRun, output, err)
		}
		var result app.RebuildResult
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatal(err)
		}
		for _, plan := range result.Plans {
			if plan.SourceID != "alpha" {
				t.Fatalf("rebuild dry_run=%t replanned disabled source: %+v", dryRun, plan)
			}
		}
		for _, item := range result.Publish.Items {
			if strings.Contains(item.TargetRef, string(filepath.Separator)+"beta"+string(filepath.Separator)) {
				t.Fatalf("rebuild dry_run=%t included disabled source: %+v", dryRun, item)
			}
		}
		if dryRun && !reflect.DeepEqual(before, treeHashes(t, root)) {
			t.Fatal("preview changed state or the library")
		}
	}
	if !reflect.DeepEqual(betaBefore, treeHashes(t, betaRoot)) {
		t.Fatal("rebuild changed disabled source output")
	}
	if _, err := os.Stat(filepath.Join(root, "library", "alpha", "alpha", "alpha-revised-ch0017.html")); err != nil {
		t.Fatalf("enabled source was not rebuilt: %v", err)
	}
}
