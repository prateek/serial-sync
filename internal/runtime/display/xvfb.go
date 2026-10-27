package display

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Session struct {
	display   string
	cmd       *exec.Cmd
	done      chan struct{}
	closeOnce sync.Once
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
	ready, output, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer ready.Close()
	cmd := exec.CommandContext(ctx, "Xvfb", "-displayfd", "3", "-screen", "0", "1366x768x24", "-nolisten", "tcp", "-ac")
	cmd.ExtraFiles = []*os.File{output}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		_ = output.Close()
		return nil, fmt.Errorf("start Xvfb: %w", err)
	}
	_ = output.Close()
	session := &Session{cmd: cmd, done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(session.done) }()
	type result struct {
		number string
		err    error
	}
	results := make(chan result, 1)
	go func() {
		number, err := bufio.NewReader(ready).ReadString('\n')
		results <- result{strings.TrimSpace(number), err}
	}()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case value := <-results:
		if number, err := strconv.Atoi(value.number); value.err == nil && err == nil && number >= 0 {
			session.display = ":" + value.number
			return session, nil
		}
		err = fmt.Errorf("Xvfb exited before reporting a ready display: %v", value.err)
	case <-ctx.Done():
		err = ctx.Err()
	case <-timer.C:
		err = fmt.Errorf("Xvfb did not become ready within 5 seconds")
	}
	_ = session.Close()
	return nil, err
}

func (s *Session) ChromeEnv() []string {
	if s.display == "" {
		return nil
	}
	return []string{"DISPLAY=" + s.display}
}

func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		if s.cmd != nil {
			_ = s.cmd.Process.Kill()
			<-s.done
		}
	})
	return nil
}
