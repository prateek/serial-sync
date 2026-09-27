package artifact

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func installFakeEPUBCheck(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, epubcheckCommand), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestEPUBCheckProcessReportsValidatorMessages(t *testing.T) {
	installFakeEPUBCheck(t, `while read -r path; do
  printf '{"path":"%s","pass":false,"messages":[{"severity":"ERROR","id":"RSC-005","path":"OEBPS/content.opf","line":2,"column":98,"text":"bad\\nattribute"}]}\n' "$path"
done`)
	var p epubcheckProcess
	defer p.stop()

	err := p.validate(t.Context(), "/tmp/book.epub", time.Minute)
	if err == nil || !strings.Contains(err.Error(), "epubcheck validation failed: ERROR(RSC-005): OEBPS/content.opf(2,98): bad\nattribute") {
		t.Fatalf("validate error = %v, want the validator message", err)
	}
}

func TestEPUBCheckProcessRetriesValidatorThatExitedWhileIdle(t *testing.T) {
	installFakeEPUBCheck(t, `read -r path && printf '{"path":"%s","pass":true,"messages":[]}\n' "$path"`)
	var p epubcheckProcess
	defer p.stop()

	for i := range 3 {
		if err := p.validate(t.Context(), "/tmp/book.epub", time.Minute); err != nil {
			t.Fatalf("validate %d: %v", i, err)
		}
	}
}

func TestEPUBCheckProcessReportsValidatorThatDiesAnswering(t *testing.T) {
	installFakeEPUBCheck(t, `read -r path; echo 'java.lang.OutOfMemoryError' >&2; exit 1`)
	var p epubcheckProcess
	defer p.stop()

	err := p.validate(t.Context(), "/tmp/book.epub", time.Minute)
	if err == nil || !strings.Contains(err.Error(), "epubcheck validator failed") || !strings.Contains(err.Error(), "OutOfMemoryError") {
		t.Fatalf("validate error = %v, want validator failure with its stderr", err)
	}
}

func TestEPUBCheckProcessRejectsResponseForAnotherPath(t *testing.T) {
	installFakeEPUBCheck(t, `while read -r path; do echo '{"path":"/tmp/other.epub","pass":true,"messages":[]}'; done`)
	var p epubcheckProcess
	defer p.stop()

	err := p.validate(t.Context(), "/tmp/book.epub", time.Minute)
	if err == nil || !strings.Contains(err.Error(), `validator answered for "/tmp/other.epub"`) {
		t.Fatalf("validate error = %v, want mismatched response rejected", err)
	}
}

func TestEPUBCheckProcessTimesOutAndRestarts(t *testing.T) {
	installFakeEPUBCheck(t, `while read -r path; do
  if [ -f "$path" ]; then printf '{"path":"%s","pass":true,"messages":[]}\n' "$path"; else exec sleep 60; fi
done`)
	var p epubcheckProcess
	defer p.stop()

	err := p.validate(t.Context(), filepath.Join(t.TempDir(), "missing.epub"), 200*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("validate error = %v, want timeout", err)
	}
	present := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(present, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.validate(t.Context(), present, time.Minute); err != nil {
		t.Fatalf("validate after timeout: %v", err)
	}
}

func TestEPUBCheckProcessStopsWhenContextIsCancelled(t *testing.T) {
	installFakeEPUBCheck(t, `read -r path; exec sleep 60`)
	var p epubcheckProcess
	defer p.stop()

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if err := p.validate(ctx, "/tmp/book.epub", time.Minute); err != context.DeadlineExceeded {
		t.Fatalf("validate error = %v, want context deadline", err)
	}
}

func TestEPUBCheckProcessExitsWhenIdle(t *testing.T) {
	installFakeEPUBCheck(t, `while read -r path; do printf '{"path":"%s","pass":true,"messages":[]}\n' "$path"; done`)
	p := epubcheckProcess{idleAfter: 50 * time.Millisecond}
	defer p.stop()

	if err := p.validate(t.Context(), "/tmp/book.epub", time.Minute); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		p.mu.Lock()
		running := p.cmd != nil
		p.mu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("validator still running after idle timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := p.validate(t.Context(), "/tmp/book.epub", time.Minute); err != nil {
		t.Fatalf("validate after idle exit: %v", err)
	}
}
