package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/prateek/serial-sync/internal/domain"
)

type LifecycleEvent struct {
	Version     int                           `json:"version"`
	Action      string                        `json:"action"`
	RunID       string                        `json:"run_id"`
	TargetID    string                        `json:"target_id"`
	DeliveryID  string                        `json:"delivery_id,omitempty"`
	Maintenance bool                          `json:"maintenance"`
	Succeeded   bool                          `json:"succeeded"`
	Candidates  []domain.PublishCandidate     `json:"candidates,omitempty"`
	Previous    *[]domain.PublishRecordBundle `json:"previous,omitempty"`
}

// Lifecycle preparation is advisory. Only a publish acknowledgement grants retirement.
func RunLifecycle(ctx context.Context, command []string, event LifecycleEvent) error {
	if len(command) == 0 {
		return nil
	}
	event.Version = 1
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("publisher %s %s failed: %w%s", event.TargetID, event.Action, err, formatExecOutput(&stdout, &stderr))
	}
	return nil
}
