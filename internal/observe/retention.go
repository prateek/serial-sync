package observe

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/prateek/serial-sync/internal/domain"
)

// CleanupResult lists only diagnostic paths; captured content is never selected.
type CleanupResult struct {
	Apply  bool      `json:"apply"`
	Before time.Time `json:"before"`
	Paths  []string  `json:"paths"`
}

func safeID(id string) bool {
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, "/\\")
}

// Cleanup requires a catalog snapshot excluding writers for the whole call.
func Cleanup(logRoot string, before time.Time, runs []domain.RunRecord, apply bool) (CleanupResult, error) {
	result := CleanupResult{Apply: apply, Before: before, Paths: []string{}}
	payloadRoot := filepath.Join(logRoot, "event-payloads")
	if info, err := os.Lstat(payloadRoot); err == nil {
		if !info.IsDir() {
			return result, fmt.Errorf("refuse non-directory diagnostic root %s", payloadRoot)
		}
	} else if !os.IsNotExist(err) {
		return result, err
	}
	add := func(path string, directory bool) error {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.IsDir() != directory || (!directory && !info.Mode().IsRegular()) {
			return fmt.Errorf("refuse unexpected diagnostic file type %s", path)
		}
		result.Paths = append(result.Paths, path)
		return nil
	}
	for _, run := range runs {
		if run.FinishedAt == nil || !run.FinishedAt.Before(before) {
			continue
		}
		if !safeID(run.ID) {
			return result, fmt.Errorf("invalid run id %q", run.ID)
		}
		for _, path := range []string{filepath.Join(logRoot, run.ID+".log"), filepath.Join(logRoot, run.ID+".jsonl"), filepath.Join(logRoot, run.ID+".jsonl.gz"), filepath.Join(logRoot, "event-payloads", run.ID)} {
			if err := add(path, path == filepath.Join(logRoot, "event-payloads", run.ID)); err != nil {
				return result, err
			}
		}
	}
	known := map[string]bool{}
	for _, run := range runs {
		known[run.ID] = true
	}
	for _, directory := range []string{logRoot, filepath.Join(logRoot, "event-payloads")} {
		entries, err := os.ReadDir(directory)
		if err != nil && !os.IsNotExist(err) {
			return result, err
		}
		for _, entry := range entries {
			name := entry.Name()
			id := name
			payloadDirectory := directory != logRoot
			temporary := !payloadDirectory && strings.HasPrefix(name, ".retention-")
			if !temporary {
				if !payloadDirectory {
					id = ""
					for _, suffix := range []string{".jsonl.gz", ".jsonl", ".log"} {
						if strings.HasSuffix(name, suffix) {
							id = strings.TrimSuffix(name, suffix)
							break
						}
					}
				}
				if !strings.HasPrefix(id, "run_") || !safeID(id) || known[id] {
					continue
				}
			}
			info, err := entry.Info()
			if err != nil {
				return result, err
			}
			if !info.ModTime().Before(before) {
				continue
			}
			if err := add(filepath.Join(directory, name), payloadDirectory); err != nil {
				return result, err
			}
		}
	}
	if !apply {
		return result, nil
	}
	// Persist intent before unlinking: an interrupted cleanup can be retried.
	old, err := ExpiredBefore(logRoot)
	if err != nil {
		return result, err
	}
	if old.After(before) {
		before = old
	}
	if err := writeHorizon(filepath.Join(logRoot, "retention.json"), before); err != nil {
		return result, err
	}
	for _, path := range result.Paths {
		if err := os.RemoveAll(path); err != nil {
			return result, err
		}
	}
	return result, nil
}

// ExpiredBefore returns the latest committed cleanup horizon.
func ExpiredBefore(logRoot string) (time.Time, error) {
	var before time.Time
	data, err := os.ReadFile(filepath.Join(logRoot, "retention.json"))
	if os.IsNotExist(err) {
		return before, nil
	}
	if err != nil {
		return before, err
	}
	err = json.Unmarshal(data, &before)
	return before, err
}

func writeHorizon(path string, before time.Time) error {
	data, err := json.Marshal(before)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".retention-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
