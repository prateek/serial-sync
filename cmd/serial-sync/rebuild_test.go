package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRunHelpExplainsOfflineRebuildScope(t *testing.T) {
	output, err := os.Create(filepath.Join(t.TempDir(), "help.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	previous := os.Stdout
	os.Stdout = output
	defer func() { os.Stdout = previous }()
	if err := run([]string{"run", "--help"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"--rebuild", "--series", "offline", "read-only"} {
		if !strings.Contains(string(data), required) {
			t.Fatalf("run help missing %s: %s", required, data)
		}
	}
}

func TestDeployedLibraryChangesOnlyAfterExplicitRebuild(t *testing.T) {
	for _, status := range []string{"published", "publishing", "legacy_epub"} {
		t.Run(status, func(t *testing.T) {
			root := t.TempDir()
			for _, key := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
				t.Setenv(key, filepath.Join(root, key))
			}
			fixtureName := "library-7728815.tar.gz"
			if status == "legacy_epub" {
				fixtureName = "epub-7728815.tar.gz"
			}
			archive, err := os.Open(filepath.Join("../../testdata/fixtures/legacy-reader", fixtureName))
			if err != nil {
				t.Fatal(err)
			}
			defer archive.Close()
			gz, err := gzip.NewReader(archive)
			if err != nil {
				t.Fatal(err)
			}
			defer gz.Close()
			tarReader := tar.NewReader(gz)
			var seed string
			for {
				header, err := tarReader.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(tarReader)
				if err != nil {
					t.Fatal(err)
				}
				data = []byte(strings.ReplaceAll(string(data), "@ROOT@", root))
				if header.Name == "catalog.sql" {
					seed = string(data)
					continue
				}
				path := filepath.Join(root, header.Name)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			db, err := sql.Open("sqlite", filepath.Join(root, "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			if status == "publishing" {
				seed = strings.ReplaceAll(seed, "'published',''", "'publishing',''")
			}
			if _, err := db.Exec(seed); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			fixture, err := filepath.Abs("../../testdata/fixtures/patreon/actus")
			if err != nil {
				t.Fatal(err)
			}
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
[[sources]]
id="actus"
provider="patreon"
url="https://www.patreon.com/c/Actus/posts"
auth_profile="fixture"
fixture_dir=%q
enabled=true
[[publishers]]
id="library"
kind="filesystem"
path=%q
enabled=true
[[series]]
id="nightmare"
title="Nightmare"
authors=["Actus"]
[series.output]
format="preserve"
[[series.inputs]]
source="actus"
match_type="title_regex"
match_value="^Nightmare"
release_role="chapter"
content_strategy="text_post"
`, filepath.Join(root, "state.db"), filepath.Join(root, "artifacts"), filepath.Join(root, "logs"), filepath.Join(root, "support"), filepath.Join(root, "session.json"), fixture, filepath.Join(root, "library"))
			path := filepath.Join(root, "config.toml")
			if status == "legacy_epub" {
				body = strings.ReplaceAll(body, `format="preserve"`, "format=\"epub\"\nbundling=\"volume\"\nchapters_per_volume=2")
				body += "\n[[series.sequence_overrides]]\nsource=\"actus\"\nrelease_id=\"154807035\"\nchapter=1\n[[series.sequence_overrides]]\nsource=\"actus\"\nrelease_id=\"154807100\"\nchapter=2\n"
			}
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			before := treeHashes(t, filepath.Join(root, "library"))
			stateBefore := treeHashes(t, root)
			if err := run([]string{"--config", path, "run", "--rebuild", "--dry-run"}); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(stateBefore, treeHashes(t, root)) {
				t.Fatal("preview migrated the old catalog")
			}
			if err := run([]string{"--config", path, "run"}); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, treeHashes(t, filepath.Join(root, "library"))) {
				t.Fatal("ordinary upgrade migrated legacy output without explicit rebuild")
			}
			if err := run([]string{"--config", path, "run", "--rebuild"}); err != nil {
				t.Fatal(err)
			}
			entries, err := os.ReadDir(filepath.Join(root, "library", "actus", "nightmare"))
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, entry := range entries {
				names = append(names, entry.Name())
			}
			want := []string{"nightmare-ch0017.html", "nightmare-ch0042.html"}
			if status == "legacy_epub" {
				want = []string{"nightmare-vol01.epub"}
			}
			if !reflect.DeepEqual(names, want) {
				t.Fatalf("explicit migration paths: %v", names)
			}
			if status == "legacy_epub" {
				listing, err := captureRun(t, "--config", path, "debug", "publishes")
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, line := range strings.Split(listing, "\n") {
					fields := strings.Fields(line)
					if len(fields) != 6 || !strings.HasPrefix(fields[5], "volart_") {
						continue
					}
					found = true
					inspection, err := captureRun(t, "--config", path, "debug", "publish", fields[0])
					if err != nil || !strings.Contains(inspection, "nightmare-vol01.epub") {
						t.Fatalf("published volume cannot be inspected: %s %v", inspection, err)
					}
				}
				if !found {
					t.Fatal("volume missing from publish records")
				}
			}
		})
	}
}

func TestCheckRejectsInvalidReaderOutputBeforeCreatingState(t *testing.T) {
	for _, tc := range []struct{ name, output, extra, want string }{
		{"format", `format="preserve"` + "\n" + `bundling="volume"`, "", "epub"},
		{"mode", `bundling="anthology"`, "", "bundling"},
		{"size", `chapters_per_volume=-2`, "", "chapters_per_volume"},
		{"zero_size", `chapters_per_volume=0`, "", "chapters_per_volume"},
		{"endpoint", `final_chapter=-1`, "", "final_chapter"},
		{"gap", `intentional_gaps=[{chapter=2,reason=""}]`, "", "reason"},
		{"book", "", "[[series.books]]\nid=\"one\"\nnumber=1\nfirst_chapter=3\nlast_chapter=2\n", "range"},
		{"book_duplicate", "", "[[series.books]]\nid=\"one\"\nnumber=1\n[[series.books]]\nid=\"two\"\nnumber=1\n", "number"},
		{"open_book_overlap", "", "[[series.books]]\nid=\"one\"\nnumber=1\n[[series.books]]\nid=\"two\"\nnumber=2\nlast_chapter=1\nseries_position_start=1\n", "overlaps"},
		{"unknown_override", "", "[[series.sequence_overrides]]\nsource=\"absent\"\nrelease_id=\"a1\"\nchapter=1\n", "source"},
		{"legacy_true", "", "[[rules]]\nsource=\"alpha\"\ntrack_key=\"alpha\"\nmatch_type=\"fallback\"\nrelease_role=\"chapter\"\ncontent_strategy=\"text_post\"\nanthology_mode=true\n", "anthology_mode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, key := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
				t.Setenv(key, filepath.Join(root, key))
			}
			path := filepath.Join(root, "config.toml")
			body := "[[sources]]\nid=\"alpha\"\nprovider=\"patreon\"\nurl=\"https://example.test/alpha\"\n[[series]]\nid=\"alpha\"\ntitle=\"Alpha\"\n[[series.inputs]]\nsource=\"alpha\"\nmatch_type=\"title_regex\"\nmatch_value=\"Alpha\"\nrelease_role=\"chapter\"\ncontent_strategy=\"text_post\"\n[series.output]\n" + tc.output + "\n" + tc.extra
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			before := treeHashes(t, root)
			if err := run([]string{"--config", path, "setup", "check"}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %s error, got %v", tc.want, err)
			}
			if !reflect.DeepEqual(before, treeHashes(t, root)) {
				t.Fatal("invalid config created state")
			}
		})
	}
}

func TestRebuildDryRunDoesNotWriteStateOrLibrary(t *testing.T) {
	tmp := t.TempDir()
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		t.Setenv(key, filepath.Join(tmp, key))
	}
	fixture, err := filepath.Abs("../../testdata/fixtures/patreon/actus")
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(tmp, "config.toml")
	body := fmt.Sprintf(`[runtime]
store_driver = "sqlite"
store_dsn = %q
artifact_root = %q
log_root = %q
support_root = %q
[[auth_profiles]]
id = "fixture"
provider = "patreon"
mode = "fixture"
session_path = %q
[[publishers]]
id = "library"
kind = "filesystem"
path = %q
enabled = true
[[sources]]
id = "actus"
provider = "patreon"
url = "https://www.patreon.com/c/Actus/posts"
auth_profile = "fixture"
fixture_dir = %q
enabled = true
[[series]]
id = "nightmare"
title = "Nightmare"
[series.output]
format = "preserve"
[[series.inputs]]
source = "actus"
match_type = "title_regex"
match_value = "^Nightmare"
release_role = "chapter"
content_strategy = "text_post"
`, filepath.Join(tmp, "state.db"), filepath.Join(tmp, "artifacts"), filepath.Join(tmp, "logs"), filepath.Join(tmp, "support"), filepath.Join(tmp, "session.json"), filepath.Join(tmp, "library"), fixture)
	if err := os.WriteFile(configPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"--config", configPath, "run"}); err != nil {
		t.Fatal(err)
	}
	before := treeHashes(t, tmp)
	if err := run([]string{"--config", configPath, "run", "--rebuild", "--dry-run", "--series", "nightmare"}); err != nil {
		t.Fatal(err)
	}
	if after := treeHashes(t, tmp); !reflect.DeepEqual(before, after) {
		t.Fatal("rebuild dry-run changed state or library files")
	}
	conflict := filepath.Join(tmp, "library", "actus", "nightmare", "nightmare-ch0017.html")
	if err := os.WriteFile(conflict, []byte("reader notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	output, err := os.Create(filepath.Join(tmp, "failure-output"))
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = output
	runErr := run([]string{"--config", configPath, "run"})
	os.Stdout = previous
	output.Close()
	summary, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	if runErr == nil || !strings.Contains(string(summary), "failed=1") || !strings.Contains(string(summary), "library") {
		t.Fatalf("CLI hid failed target summary: %s %v", summary, runErr)
	}
	writer, err := sql.Open("sqlite", filepath.Join(tmp, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := writer.Exec("PRAGMA journal_mode=WAL; CREATE TABLE preview_writer_fixture (value TEXT);"); err != nil {
		t.Fatal(err)
	}
	before = treeHashes(t, tmp)
	if err := run([]string{"--config", configPath, "run", "--rebuild", "--dry-run"}); err == nil || !strings.Contains(err.Error(), "checkpointed") {
		t.Fatalf("live writer must block read-only preview: %v", err)
	}
	if !reflect.DeepEqual(before, treeHashes(t, tmp)) {
		t.Fatal("blocked preview changed live state")
	}
}

func TestCheckWarnsOnlyWhenDeprecatedAnthologyFlagIsPresent(t *testing.T) {
	for _, location := range []string{"absent", "series_input", "legacy_rule"} {
		t.Run(location, func(t *testing.T) {
			root := t.TempDir()
			for _, key := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
				t.Setenv(key, filepath.Join(root, key))
			}
			body := "[[sources]]\nid=\"alpha\"\nprovider=\"patreon\"\nurl=\"https://example.test/alpha\"\n"
			if location == "series_input" {
				body += "[[series]]\nid=\"alpha\"\ntitle=\"Alpha\"\n[[series.inputs]]\nsource=\"alpha\"\nmatch_type=\"title_regex\"\nrelease_role=\"chapter\"\ncontent_strategy=\"text_post\"\nanthology_mode=false\n"
			}
			if location == "legacy_rule" {
				body += "[[rules]]\nsource=\"alpha\"\ntrack_key=\"alpha\"\nmatch_type=\"fallback\"\nrelease_role=\"chapter\"\ncontent_strategy=\"text_post\"\nanthology_mode=false\n"
			}
			path := filepath.Join(root, "config.toml")
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			log, err := os.Create(filepath.Join(root, "stderr"))
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			previous := os.Stderr
			os.Stderr = log
			defer func() { os.Stderr = previous }()
			if err := run([]string{"--config", path, "setup", "check"}); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(log.Name())
			if err != nil {
				t.Fatal(err)
			}
			warned := strings.Contains(string(data), "anthology_mode") && strings.Contains(string(data), "deprecated")
			if warned != (location != "absent") {
				t.Fatalf("unexpected diagnostic: %s", data)
			}
		})
	}
}

func treeHashes(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	result := map[string][32]byte{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			result[path] = [32]byte{}
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result[path] = sha256.Sum256(content)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func captureRun(t *testing.T, args ...string) (string, error) {
	t.Helper()
	output, err := os.Create(filepath.Join(t.TempDir(), "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	previous := os.Stdout
	os.Stdout = output
	defer func() { os.Stdout = previous }()
	runErr := run(args)
	data, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(data), runErr
}
