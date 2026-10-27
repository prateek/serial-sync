//go:build darwin || linux

package app_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/prateek/serial-sync/internal/app"
	"github.com/prateek/serial-sync/internal/domain"
)

func TestRunInspectionDoesNotReadPayloads(t *testing.T) {
	for _, name := range []string{"inspect", "list"} {
		t.Run(name, func(t *testing.T) {
			service, run := newRunRecordService(t)
			payloadRef := filepath.Join(service.Config.Runtime.LogRoot, "payload.fifo")
			if err := syscall.Mkfifo(payloadRef, 0o600); err != nil {
				t.Fatal(err)
			}
			fd, err := syscall.Open(payloadRef, syscall.O_RDWR|syscall.O_NONBLOCK, 0)
			if err != nil {
				t.Fatal(err)
			}
			keeper := os.NewFile(uintptr(fd), payloadRef)
			defer keeper.Close()
			// Keeping a writer open makes any payload read wait for data or EOF.
			data, err := json.Marshal(map[string]any{"timestamp": run.StartedAt, "message": "Patreon live release listing complete", "payload_ref": payloadRef})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(service.Config.Runtime.LogRoot, run.ID+".jsonl"), data, 0o600); err != nil {
				t.Fatal(err)
			}
			type result struct {
				events []domain.EventRecord
				err    error
			}
			done := make(chan result, 1)
			go func() {
				var events []domain.EventRecord
				var err error
				if name == "inspect" {
					var bundle *domain.RunBundle
					bundle, err = service.InspectRun(context.Background(), run.ID)
					if bundle != nil {
						events = bundle.Events
					}
				} else {
					var listed *app.RunEventList
					listed, err = service.ListRunEvents(context.Background(), run.ID, app.RunEventFilter{Limit: 1})
					if listed != nil {
						events = listed.Events
					}
				}
				done <- result{events, err}
			}()
			select {
			case got := <-done:
				if got.err != nil {
					t.Fatal(got.err)
				}
				if len(got.events) != 1 || got.events[0].PayloadRef != payloadRef {
					t.Fatalf("lost payload reference: %+v", got.events)
				}
			case <-time.After(5 * time.Second):
				_, _ = keeper.Write([]byte(`{}`))
				_ = keeper.Close()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("inspection did not finish after releasing payload pipe")
				}
				t.Fatal("inspection waited for payload bytes")
			}
		})
	}
}
