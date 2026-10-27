package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/prateek/serial-sync/internal/domain"
)

type deliveryFiles struct{ directory string }

type deliveryReceipt struct {
	ID          string    `json:"id"`
	TargetID    string    `json:"target_id"`
	EventScope  string    `json:"event_scope"`
	RunID       string    `json:"run_id"`
	CompletedAt time.Time `json:"completed_at"`
	State       string    `json:"state"`
}

func (s *Service) deliveryFiles() deliveryFiles {
	return deliveryFiles{directory: filepath.Join(s.Config.Runtime.ArtifactRoot, "deliveries")}
}

func (f deliveryFiles) write(plan deliveryPlan) (string, error) {
	if plan.ID == "" || plan.ID == "." || plan.ID == ".." || strings.ContainsAny(plan.ID, "/\\") {
		return "", fmt.Errorf("invalid delivery id %q", plan.ID)
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(f.directory, 0700); err != nil {
		return "", err
	}
	path := filepath.Join(f.directory, plan.ID+".json")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err := syncDeliveryDirectory(f.directory); err != nil {
		return "", err
	}
	return path, nil
}

func readDeliveryPlan(pending domain.PendingPublish) (deliveryPlan, error) {
	var plan deliveryPlan
	data, err := os.ReadFile(pending.PayloadRef)
	if err == nil {
		err = json.Unmarshal(data, &plan)
	}
	plan.PayloadRef = pending.PayloadRef
	return plan, err
}

func (f deliveryFiles) compact(plan deliveryPlan, runID string) error {
	if plan.PayloadRef == "" {
		return fmt.Errorf("delivery %s has no saved path", plan.ID)
	}
	receipt := deliveryReceipt{ID: plan.ID, TargetID: plan.Target.ID, EventScope: plan.EventScope, RunID: runID, CompletedAt: time.Now().UTC(), State: "completed"}
	data, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	directory := filepath.Dir(plan.PayloadRef)
	file, err := os.CreateTemp(directory, ".retention-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(file.Name(), plan.PayloadRef); err != nil {
		return err
	}
	return syncDeliveryDirectory(directory)
}

func (f deliveryFiles) expired(before time.Time, pending []domain.PendingPublish) ([]string, error) {
	paths := []string{}
	info, err := os.Lstat(f.directory)
	if os.IsNotExist(err) {
		return paths, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("refuse non-directory delivery root %s", f.directory)
	}
	protectedIDs, protectedPaths := map[string]bool{}, map[string]bool{}
	for _, plan := range pending {
		protectedIDs[plan.ID] = true
		path, err := filepath.Abs(plan.PayloadRef)
		if err != nil {
			return nil, err
		}
		protectedPaths[path] = true
	}
	entries, err := os.ReadDir(f.directory)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") && !strings.HasPrefix(name, ".retention-") {
			continue
		}
		path := filepath.Join(f.directory, name)
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		if protectedIDs[strings.TrimSuffix(name, ".json")] || protectedPaths[absolute] {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if !info.ModTime().Before(before) {
			continue
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("refuse unexpected delivery file type %s", path)
		}
		paths = append(paths, path)
	}
	return paths, nil
}

func (f deliveryFiles) removeExpired(paths []string) error {
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func syncDeliveryDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
