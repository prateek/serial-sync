package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupCheckDoesNotInitializeState(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SERIAL_SYNC_CONTAINER", "false")
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		t.Setenv(key, filepath.Join(root, key))
	}
	path := filepath.Join(root, "config.toml")
	if err := os.WriteFile(path, []byte(`[[sources]]
id="fictional-author"
provider="patreon"
url="https://example.invalid/posts"
`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"--config", path, "setup", "check"}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.toml" {
		t.Fatalf("check wrote state: %v", entries)
	}
}

func TestAuthoringRejectsInvalidRulesBeforeStateInitialization(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SERIAL_SYNC_CONTAINER", "false")
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		t.Setenv(key, filepath.Join(root, key))
	}
	template := `[[sources]]
id="fictional-author"
provider="patreon"
url="https://example.invalid/posts"
[[series]]
id="harbor"
title="The Glass Harbor"
[[series.inputs]]
source="fictional-author"
match_type="title_regex"
match_value="^Harbor"
release_role="extra"
content_strategy="text_post"
`
	cases := []struct{ name, old, value, field string }{
		{"regex", `match_value="^Harbor"`, `match_value="["`, "match_value"},
		{"matcher", `match_type="title_regex"`, `match_type="typo"`, "match_type"},
		{"role", `release_role="extra"`, `release_role="typo"`, "release_role"},
		{"strategy", `content_strategy="text_post"`, `content_strategy="typo"`, "content_strategy"},
		{"glob", `content_strategy="text_post"`, "content_strategy=\"text_post\"\nattachment_glob=[\"[\"]", "attachment_glob"},
		{"mixed selectors", `release_role="extra"`, "release_role=\"extra\"\ncollections=[\"Harbor\"]", "selector"},
		{"guard regex", `release_role="extra"`, "release_role=\"extra\"\nunless_title_patterns=[\"[\"]", "unless_title_patterns"},
		{"guard minimum", `release_role="extra"`, "release_role=\"extra\"\nmin_body_chars=-1", "min_body_chars"},
		{"unknown key", `release_role="extra"`, `release_rol="extra"`, "release_rol"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(root, "invalid.toml")
			if err := os.WriteFile(path, []byte(strings.Replace(template, tc.old, tc.value, 1)), 0600); err != nil {
				t.Fatal(err)
			}
			for _, command := range [][]string{{"setup", "check"}, {"setup", "preview", "--workspace", filepath.Join(root, "absent")}, {"run"}} {
				err := run(append([]string{"--config", path}, command...))
				if err == nil {
					t.Fatalf("%v accepted invalid %s", command, tc.field)
				}
				for _, want := range []string{path, tc.field, "series"} {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("%v: diagnostic %q omits %q", command, err, want)
					}
				}
			}
			for _, key := range []string{"XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
				if _, err := os.Stat(os.Getenv(key)); !os.IsNotExist(err) {
					t.Fatalf("invalid config initialized %s", key)
				}
			}
		})
	}
}

func TestPreviewValidatesStandaloneSeriesWithoutInitializingState(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SERIAL_SYNC_CONTAINER", "false")
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		t.Setenv(key, filepath.Join(root, key))
	}
	configPath := filepath.Join(root, "config.toml")
	if err := os.WriteFile(configPath, []byte(`[[sources]]
id="fictional-author"
provider="patreon"
url="https://example.invalid/posts"
`), 0600); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, "dump")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"manifest.json": `{"version":3,"provider":"patreon","sources_file":"sources.toml","series_file":"series.toml","creators":[{"source_id":"fictional-author","posts_file":"posts.ndjson"}]}`,
		"sources.toml": `[[sources]]
id="fictional-author"
provider="patreon"
url="https://example.invalid/posts"
`,
		"posts.ndjson": `{"normalized":{"provider":"patreon","provider_release_id":"1","title":"Harbor","text_html":"<p>Filler.</p>"}}`,
		"series.toml": `[[series]]
id="harbor"
title="The Glass Harbor"
[[series.inputs]]
source="fictional-author"
match_type="title_regex"
match_value="["
release_role="extra"
content_strategy="text_post"
`,
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"--config", configPath, "setup", "preview", "--workspace", workspace, "--series-file", "series.toml", "--format", "json"}
	err := run(args)
	if err == nil || !strings.Contains(err.Error(), "match_value") || !strings.Contains(err.Error(), "series.toml") {
		t.Fatalf("invalid series diagnostic: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "series.toml"), []byte(strings.Replace(files["series.toml"], `match_value="["`, `match_value="Harbor"`, 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(args); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "sources.toml"), []byte(strings.Replace(files["sources.toml"], "url=", "urll=", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, args...); err == nil || !strings.Contains(err.Error(), "sources.toml") || !strings.Contains(err.Error(), "urll") {
		t.Fatalf("invalid source diagnostic: %v", err)
	}
	for _, key := range []string{"XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		if _, err := os.Stat(os.Getenv(key)); !os.IsNotExist(err) {
			t.Fatalf("preview initialized %s", key)
		}
	}
}

func TestCheckValidatesExplicitLegacyRuleReferences(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SERIAL_SYNC_CONTAINER", "false")
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		t.Setenv(key, filepath.Join(root, key))
	}
	base := `[[sources]]
id="fictional-author"
provider="patreon"
url="https://example.invalid/posts"
[[rules]]
source="fictional-author"
track_key="harbor"
match_type="fallback"
release_role="extra"
content_strategy="text_post"
`
	for _, field := range []string{"series_id", "book_id"} {
		path := filepath.Join(root, "config.toml")
		if err := os.WriteFile(path, []byte(base+field+`="missing"`), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := captureRun(t, "--config", path, "setup", "check")
		if err == nil || !strings.Contains(err.Error(), field) || !strings.Contains(err.Error(), path) {
			t.Fatalf("invalid reference accepted: %v", err)
		}
		if err := os.WriteFile(path, []byte(base), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := captureRun(t, "--config", path, "setup", "check"); err != nil {
			t.Fatalf("legacy track rule rejected: %v", err)
		}
	}
}

func TestCheckAcceptsLegacyBookInTrackSeries(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SERIAL_SYNC_CONTAINER", "false")
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		t.Setenv(key, filepath.Join(root, key))
	}
	path := filepath.Join(root, "config.toml")
	data := `[[sources]]
id="fictional-author"
provider="patreon"
url="https://example.invalid/posts"
[[series]]
id="harbor"
title="The Glass Harbor"
[[series.books]]
id="one"
number=1
[[series.inputs]]
source="fictional-author"
match_type="tag"
match_value="harbor"
release_role="chapter"
content_strategy="text_post"
[[rules]]
source="fictional-author"
track_key="harbor"
book_id="one"
match_type="fallback"
release_role="chapter"
content_strategy="text_post"
`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "--config", path, "setup", "check"); err != nil {
		t.Fatal(err)
	}
}

func TestProfileOnlyMembershipsExpiredSessionIsReadOnly(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SERIAL_SYNC_CONTAINER", "false")
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		t.Setenv(key, filepath.Join(root, key))
	}
	path := filepath.Join(root, "config.toml")
	data := `[[auth_profiles]]
id="fictional"
provider="patreon"
mode="username_password"
username_env="FICTIONAL_USER"
password_env="FICTIONAL_PASSWORD"
session_path="` + filepath.Join(root, "absent-session.json") + `"
`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "--config", path, "setup", "check"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "--config", path, "setup", "memberships"); err == nil || !strings.Contains(err.Error(), "setup auth --auth-profile fictional") {
		t.Fatalf("missing reauthentication instruction: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatal("membership inspection initialized state")
	}
}

func TestBookLabelsLoadStrictlyWithoutExplicitInputs(t *testing.T) {
	root := t.TempDir()
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		t.Setenv(key, filepath.Join(root, key))
	}
	t.Setenv("SERIAL_SYNC_CONTAINER", "false")
	template := `[[sources]]
id="fictional"
provider="patreon"
url="https://example.invalid"
[[series]]
id="harbor"
title="Harbor"
source="fictional"
[[series.books]]
id="arrival"
number=1
collection={id="book-1",name="Arrival"}
[[series.books]]
id="return"
number=2
tag="Return"
`
	path := filepath.Join(root, "config.toml")
	writeReplayFile(t, path, template)
	if err := run([]string{"--config", path, "setup", "check"}); err != nil {
		t.Fatal(err)
	}
	writeReplayFile(t, path, strings.Replace(template, `collection={id="book-1",name="Arrival"}`, `collection="Arrival"`, 1))
	if err := run([]string{"--config", path, "setup", "check"}); err != nil {
		t.Fatal(err)
	}
	writeReplayFile(t, path, strings.Replace(template, "source=\"fictional\"\n", "", 1))
	if err := run([]string{"--config", path, "setup", "check"}); err == nil || !strings.Contains(err.Error(), "series.source") {
		t.Fatalf("ambiguous book source accepted: %v", err)
	}
}
