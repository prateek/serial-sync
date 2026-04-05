package display

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const defaultDisplay = ":99"

type Session struct {
	display string
	cmd     *exec.Cmd
	started bool
}

func Ensure(ctx context.Context) (*Session, error) {
	if display := strings.TrimSpace(os.Getenv("DISPLAY")); display != "" {
		return &Session{display: display}, nil
	}
	if runtime.GOOS != "linux" {
		return &Session{}, nil
	}
	if _, err := exec.LookPath("Xvfb"); err != nil {
		return nil, fmt.Errorf("DISPLAY is unset and Xvfb is unavailable: %w", err)
	}
	display := defaultDisplay
	cmd := exec.CommandContext(ctx, "Xvfb", display, "-screen", "0", "1366x768x24", "-nolisten", "tcp", "-ac")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start Xvfb: %w", err)
	}
	session := &Session{
		display: display,
		cmd:     cmd,
		started: true,
	}
	if err := session.waitUntilReady(ctx, 5*time.Second); err != nil {
		_ = session.Close()
		return nil, err
	}
	return session, nil
}

func (s *Session) ChromeEnv() []string {
	if strings.TrimSpace(s.display) == "" {
		return nil
	}
	return []string{"DISPLAY=" + s.display}
}

func (s *Session) Close() error {
	if !s.started || s.cmd == nil || s.cmd.Process == nil {
		return nil
	}
	_ = s.cmd.Process.Kill()
	return s.cmd.Wait()
}

func (s *Session) waitUntilReady(ctx context.Context, timeout time.Duration) error {
	if strings.TrimSpace(s.display) == "" {
		return nil
	}
	socketPath := "/tmp/.X11-unix/X" + strings.TrimPrefix(s.display, ":")
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socketPath); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("Xvfb did not become ready on %s", s.display)
}
