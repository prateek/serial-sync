package app_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prateek/serial-sync/internal/app"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/publish"
)

func TestLifecycleFailureDoesNotLoseDeliveryAcknowledgements(t *testing.T) {
	for _, phase := range []string{"prepare", "complete"} {
		t.Run(phase, func(t *testing.T) {
			s, _ := newReaderService(t)
			root := t.TempDir()
			script, log, fail := filepath.Join(root, "hook.py"), filepath.Join(root, "events.ndjson"), filepath.Join(root, "fail")
			body := "import json,sys,pathlib\ne=json.load(sys.stdin)\nwith open(sys.argv[1],'a') as f:f.write(json.dumps(e)+'\\n')\np=pathlib.Path(sys.argv[2])\nif p.exists() and p.read_text()==e['action']:sys.exit(1)\n"
			if err := os.WriteFile(script, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(fail, []byte(phase), 0600); err != nil {
				t.Fatal(err)
			}
			command := []string{"python3", script, log, fail}
			s.Config.Publishers = []config.PublisherConfig{{ID: "reader", Kind: "exec", Enabled: true, ProtocolVersion: 2, Command: command, LifecycleCommand: command}}
			result, err := s.RunOnce(context.Background(), "", "", "run")
			if err == nil {
				t.Fatal("hook failure was hidden")
			}
			if phase == "prepare" && result.Publish.Published != 0 {
				t.Fatal("failed preparation acknowledged a file")
			}
			if phase == "complete" && result.Publish.Published != 2 {
				t.Fatalf("notification failure undid chapter delivery: %+v", result)
			}
			before := mustReadFile(t, log)
			if _, err := s.Rebuild(context.Background(), app.RebuildOptions{DryRun: true}, "preview"); err != nil {
				t.Fatal(err)
			}
			if string(before) != string(mustReadFile(t, log)) {
				t.Fatal("preview invoked lifecycle command")
			}
			if err := os.Remove(fail); err != nil {
				t.Fatal(err)
			}
			if _, err := s.RunOnce(context.Background(), "", "", "retry"); err != nil {
				t.Fatal(err)
			}
			published := 0
			for _, line := range strings.Split(strings.TrimSpace(string(mustReadFile(t, log))), "\n") {
				var event struct {
					Action string `json:"action"`
				}
				if err := json.Unmarshal([]byte(line), &event); err != nil {
					t.Fatal(err)
				}
				if event.Action == "publish" {
					published++
				}
			}
			if published != 2 {
				t.Fatalf("retry replayed acknowledged publications: %d", published)
			}
		})
	}
}

func TestLifecycleMaintenanceIncludesPublishedPredecessors(t *testing.T) {
	s, _ := newReaderService(t)
	root := t.TempDir()
	script, log := filepath.Join(root, "hook.py"), filepath.Join(root, "events.ndjson")
	body := "import json,sys\ne=json.load(sys.stdin)\nwith open(sys.argv[1],'a') as f:f.write(json.dumps(e)+'\\n')\n"
	if err := os.WriteFile(script, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	command := []string{"python3", script, log}
	s.Config.Publishers = []config.PublisherConfig{{ID: "reader", Kind: "exec", Enabled: true, ProtocolVersion: 2, Command: command, LifecycleCommand: command}}
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "rebuild"); err != nil {
		t.Fatal(err)
	}
	maintenance := 0
	for _, line := range strings.Split(strings.TrimSpace(string(mustReadFile(t, log))), "\n") {
		var event publish.LifecycleEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if event.Action != "prepare" || !event.Maintenance {
			if event.Previous != nil {
				t.Fatal("ordinary delivery unexpectedly authorized maintenance replacement")
			}
			continue
		}
		maintenance++
		if event.Previous == nil || len(*event.Previous) != 2 {
			t.Fatalf("maintenance lost its published predecessor proof: %+v", event.Previous)
		}
		for _, previous := range *event.Previous {
			if previous.Record.Filename == "" || previous.Artifact.SHA256 == "" || previous.Release.ID == "" || previous.Source.ID == "" || previous.Track.TrackKey == "" {
				t.Fatalf("maintenance predecessor lacks identity: %+v", previous)
			}
		}
	}
	if maintenance != 1 {
		t.Fatalf("wanted one maintenance preparation, got %d", maintenance)
	}
}
