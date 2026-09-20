package app_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prateek/serial-sync/internal/app"
	"github.com/prateek/serial-sync/internal/config"
)

func TestHookRestoresPreviouslyRetiredChapter(t *testing.T) {
	s, upstream := newReaderService(t)
	upstream.docs["alpha"] = upstream.docs["alpha"][:1]
	root := t.TempDir()
	script := filepath.Join(root, "hook.py")
	hook := `import hashlib, json, pathlib, shutil, sys
event = json.load(sys.stdin)
root = pathlib.Path(sys.argv[1])
seen = root / 'seen.json'
output = root / 'output'
output.mkdir(exist_ok=True)
acknowledged = json.loads(seen.read_text()) if seen.exists() else []
if event['event_id'] in acknowledged:
    sys.exit(0)
if event['action'] == 'publish':
    artifact = event['artifact']
    shutil.copyfile(artifact['storage_ref'], output / artifact['filename'])
else:
    previous = event['previous']
    path = output / previous['record']['filename']
    if path.exists() and hashlib.sha256(path.read_bytes()).hexdigest() == previous['artifact']['sha256']:
        path.unlink()
acknowledged.append(event['event_id'])
seen.write_text(json.dumps(acknowledged))
`
	if err := os.WriteFile(script, []byte(hook), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Config.Publishers = []config.PublisherConfig{{ID: "hook", Kind: "exec", Enabled: true, ProtocolVersion: 2, Command: []string{"python3", script, root}}}
	original := upstream.docs["alpha"][0].Normalized.TextHTML
	var originalOutput []byte
	for i, body := range []string{original, "<p>Corrected chapter body.</p>", original, "<p>Corrected chapter body.</p>", original} {
		upstream.docs["alpha"][0].Normalized.TextHTML = body
		if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
			t.Fatalf("revision %d: %v", i, err)
		}
		files := findFiles(t, filepath.Join(root, "output"), ".html")
		if len(files) != 1 {
			t.Fatalf("revision %d left %d readable chapters, want 1", i, len(files))
		}
		content := mustReadFile(t, files[0])
		if i == 0 {
			originalOutput = content
		} else if bytes.Equal(content, originalOutput) != (body == original) {
			t.Fatalf("revision %d did not install the requested chapter", i)
		}
	}
}

func TestCyclicRegroupRecoversWithDistinctTitle(t *testing.T) {
	s, upstream := newReaderService(t)
	upstream.docs["alpha"] = upstream.docs["alpha"][:2]
	upstream.docs["alpha"][0].Normalized.Title = "Alpha Saga Book 1 Chapter 1"
	upstream.docs["alpha"][1].Normalized.Title = "Alpha Saga Book 2 Chapter 1"
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 50}
	s.Config.Series[0].Books = []config.BookConfig{{ID: "one", Number: 1, LastChapter: 1}, {ID: "two", Number: 2, LastChapter: 1}}
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	oldFiles := map[string][]byte{}
	for _, path := range findFiles(t, s.Config.Publishers[0].Path, ".epub") {
		oldFiles[path] = mustReadFile(t, path)
	}
	s.Config.Series[0].SequenceOverrides = []config.SequenceOverride{{Source: "alpha", ReleaseID: "a1", BookID: "two"}, {Source: "alpha", ReleaseID: "a2", BookID: "one"}}
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	result, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "rebuild")
	if err == nil || len(result.Publish.Items) == 0 || !strings.Contains(result.Publish.Items[0].Message, "cyclic") {
		t.Fatalf("expected rejected cycle: %+v %v", result.Publish, err)
	}
	for path, content := range oldFiles {
		if !bytes.Equal(content, mustReadFile(t, path)) {
			t.Fatalf("rejected cycle changed %s", path)
		}
	}
	for _, title := range []string{"Alpha Recovery", "Alpha Saga"} {
		s.Config.Series[0].Title = title
		s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
		if result, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "rebuild"); err != nil {
			t.Fatalf("rebuild under %q: %+v %v", title, result.Publish, err)
		}
		files := findFiles(t, s.Config.Publishers[0].Path, ".epub")
		if len(files) != 2 {
			t.Fatalf("rebuild under %q left %d files, want 2", title, len(files))
		}
		for _, path := range files {
			if !strings.Contains(string(epubEntry(t, path, ".opf")), title) {
				t.Fatalf("rebuild under %q retained old edition %s", title, path)
			}
		}
	}
}
