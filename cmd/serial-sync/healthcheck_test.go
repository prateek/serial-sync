package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/prateek/serial-sync/internal/observe"
)

func writeDropRunConfig(t *testing.T, library string) string {
	t.Helper()
	dir := t.TempDir()
	fixtures, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fixtures", "patreon", "plum-parrot"))
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(dir, "state")
	config := fmt.Sprintf(`[runtime]
store_driver = "sqlite"
store_dsn = %q
artifact_root = %q
support_root = %q
log_root = %q

[[auth_profiles]]
id = "patreon-default"
provider = "patreon"
mode = "fixture"
session_path = %q

[[publishers]]
id = "library"
kind = "drop"
path = %q
enabled = true

[[sources]]
id = "plum-parrot"
provider = "patreon"
url = "https://www.patreon.com/c/PlumParrot/posts"
auth_profile = "patreon-default"
fixture_dir = %q
enabled = true

[[series]]
id = "andy-again-3"
title = "Andy, Again 3"
authors = ["Plum Parrot"]

  [series.output]
  format = "preserve"

  [[series.inputs]]
  source = "plum-parrot"
  priority = 10
  match_type = "tag"
  match_value = "AA3"
  release_role = "chapter"
  content_strategy = "attachment_preferred"
  attachment_glob = ["*.epub", "*.pdf"]
  attachment_priority = ["epub", "pdf"]
`, filepath.Join(state, "state.db"), filepath.Join(state, "artifacts"), filepath.Join(state, "support"), filepath.Join(state, "logs"), filepath.Join(state, "sessions", "patreon-default.json"), library, fixtures)
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func recordPings(t *testing.T) (string, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
	}))
	t.Cleanup(server.Close)
	return server.URL + "/ping/check", func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), paths...)
	}
}

func TestRunPingsHealthcheck(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mounted bool
		want    string
	}{
		{"success", true, "/ping/check/start,/ping/check"},
		{"failure", false, "/ping/check/start,/ping/check/fail"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			library := filepath.Join(t.TempDir(), "library")
			if tc.mounted {
				if err := os.MkdirAll(library, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			url, pings := recordPings(t)
			t.Setenv(observe.HealthcheckURLEnv, url)
			err := (&RunExecCmd{}).Run(&CLI{ConfigPath: writeDropRunConfig(t, library)})
			if (err == nil) != tc.mounted {
				t.Fatalf("Run() error = %v", err)
			}
			if got := strings.Join(pings(), ","); got != tc.want {
				t.Fatalf("pings = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestDryRunDoesNotPingHealthcheck(t *testing.T) {
	url, pings := recordPings(t)
	t.Setenv(observe.HealthcheckURLEnv, url)
	library := t.TempDir()
	if err := (&RunExecCmd{DryRun: true}).Run(&CLI{ConfigPath: writeDropRunConfig(t, library)}); err != nil {
		t.Fatal(err)
	}
	if got := pings(); len(got) != 0 {
		t.Fatalf("dry run pinged %v", got)
	}
}
