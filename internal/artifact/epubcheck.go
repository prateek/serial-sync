package artifact

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// epubcheckCommand is installed by scripts/install-epubcheck from
// third_party/epubcheck. It validates one EPUB path per stdin line and answers
// with one JSON object per line (epubcheckResponse).
const epubcheckCommand = "serial-sync-epubcheck"

const (
	epubcheckTimeout     = 2 * time.Minute
	epubcheckIdleTimeout = 5 * time.Minute
)

// The validator JVM is shared by every validation in the process: starting it
// costs seconds, while each later file costs milliseconds. It exits after
// epubcheckIdleTimeout so a daemon does not hold its heap between cycles.
var sharedEPUBCheck epubcheckProcess

type epubcheckProcess struct {
	mu         sync.Mutex
	idleAfter  time.Duration // zero means epubcheckIdleTimeout
	path       string
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     *bufio.Reader
	stderr     *lockedBuffer
	idle       *time.Timer
	generation int
}

type epubcheckResponse struct {
	Path      string             `json:"path"`
	Pass      bool               `json:"pass"`
	Messages  []epubcheckMessage `json:"messages"`
	Exception string             `json:"exception"`
}

type epubcheckMessage struct {
	Severity string `json:"severity"`
	ID       string `json:"id"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Text     string `json:"text"`
}

type epubcheckExchange struct {
	response epubcheckResponse
	answered bool // the validator wrote part of a response before err
	err      error
}

func validateEPUBCheck(ctx context.Context, content []byte) error {
	workDir, err := os.MkdirTemp("", "serial-sync-epubcheck-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workDir)

	epubPath := filepath.Join(workDir, "book.epub")
	if err := os.WriteFile(epubPath, content, 0o644); err != nil {
		return err
	}
	return sharedEPUBCheck.validate(ctx, epubPath, epubcheckTimeout)
}

func (p *epubcheckProcess) validate(ctx context.Context, epubPath string, timeout time.Duration) error {
	if strings.ContainsAny(epubPath, "\r\n") {
		return fmt.Errorf("epub path contains a line break: %q", epubPath)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.generation++

	command, err := exec.LookPath(epubcheckCommand)
	if err != nil {
		return fmt.Errorf("%s is required for EPUB validation: %w", epubcheckCommand, err)
	}
	if p.cmd != nil && p.path != command {
		p.stop()
	}
	for attempt := 1; ; attempt++ {
		if p.cmd == nil {
			if err := p.start(command); err != nil {
				return err
			}
		}
		exchange, err := p.exchange(ctx, epubPath, timeout)
		if err != nil {
			return err
		}
		if exchange.err != nil {
			stderr := p.stderr
			p.stop()
			// A validator that died while idle fails before answering; retry once on a fresh one.
			if !exchange.answered && attempt == 1 {
				continue
			}
			return fmt.Errorf("epubcheck validator failed: %w: %s", exchange.err, stderr.String())
		}
		p.stderr.Reset()
		p.armIdle()
		return exchange.response.verdict()
	}
}

func (p *epubcheckProcess) exchange(ctx context.Context, epubPath string, timeout time.Duration) (epubcheckExchange, error) {
	stdin, stdout := p.stdin, p.stdout
	done := make(chan epubcheckExchange, 1)
	go func() { done <- roundTripEPUBCheck(stdin, stdout, epubPath) }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case exchange := <-done:
		return exchange, nil
	case <-timer.C:
		p.stop()
		return epubcheckExchange{}, fmt.Errorf("epubcheck validation timed out after %s", timeout)
	case <-ctx.Done():
		p.stop()
		return epubcheckExchange{}, ctx.Err()
	}
}

func (p *epubcheckProcess) start(command string) error {
	cmd := exec.Command(command)
	cmd.WaitDelay = time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr := &lockedBuffer{limit: 64 << 10}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", command, err)
	}
	p.path, p.cmd, p.stdin, p.stdout, p.stderr = command, cmd, stdin, bufio.NewReader(stdout), stderr
	return nil
}

func (p *epubcheckProcess) stop() {
	if p.idle != nil {
		p.idle.Stop()
		p.idle = nil
	}
	if p.cmd == nil {
		return
	}
	_ = p.stdin.Close()
	_ = p.cmd.Process.Kill()
	_ = p.cmd.Wait()
	p.path, p.cmd, p.stdin, p.stdout = "", nil, nil, nil
}

func (p *epubcheckProcess) armIdle() {
	after := p.idleAfter
	if after == 0 {
		after = epubcheckIdleTimeout
	}
	if p.idle != nil {
		p.idle.Stop()
	}
	generation := p.generation
	p.idle = time.AfterFunc(after, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.generation == generation {
			p.stop()
		}
	})
}

func roundTripEPUBCheck(stdin io.Writer, stdout *bufio.Reader, epubPath string) epubcheckExchange {
	if _, err := io.WriteString(stdin, epubPath+"\n"); err != nil {
		return epubcheckExchange{err: err}
	}
	line, err := stdout.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			err = fmt.Errorf("validator exited: %w", err)
		}
		return epubcheckExchange{answered: line != "", err: err}
	}
	var response epubcheckResponse
	if err := json.Unmarshal([]byte(line), &response); err != nil {
		return epubcheckExchange{answered: true, err: fmt.Errorf("unexpected validator response %q: %w", line, err)}
	}
	if response.Path != epubPath {
		return epubcheckExchange{answered: true, err: fmt.Errorf("validator answered for %q, want %q", response.Path, epubPath)}
	}
	return epubcheckExchange{response: response, answered: true}
}

func (r epubcheckResponse) verdict() error {
	if r.Pass {
		return nil
	}
	lines := make([]string, 0, len(r.Messages)+1)
	for _, m := range r.Messages {
		lines = append(lines, fmt.Sprintf("%s(%s): %s(%d,%d): %s", m.Severity, m.ID, m.Path, m.Line, m.Column, m.Text))
	}
	if r.Exception != "" {
		lines = append(lines, "exception: "+r.Exception)
	}
	if len(lines) == 0 {
		lines = append(lines, "no messages reported")
	}
	return fmt.Errorf("epubcheck validation failed: %s", strings.Join(lines, "\n"))
}

// lockedBuffer keeps the validator's recent stderr for error messages.
type lockedBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Write(p)
	if extra := b.buf.Len() - b.limit; extra > 0 {
		b.buf.Next(extra)
	}
	return len(p), nil
}

func (b *lockedBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
