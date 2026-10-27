package display

import (
	"context"
	"os"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func TestOwnedDisplayLifecycle(t *testing.T) {
	if runtime.GOOS != "linux" || os.Getenv("SERIAL_SYNC_TEST_BROWSER") != "1" {
		t.Skip("set SERIAL_SYNC_TEST_BROWSER=1 in the full container")
	}
	t.Setenv("DISPLAY", "")
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		session, err := Ensure(ctx)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		if len(session.ChromeEnv()) != 1 {
			t.Fatal("missing display environment")
		}
		cancel()
		closed := make(chan error, 1)
		go func() { closed <- session.Close() }()
		select {
		case err := <-closed:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Xvfb did not close after cancellation")
		}
		if err := session.cmd.Process.Signal(syscall.Signal(0)); err == nil {
			t.Fatal("Xvfb still alive after close")
		}
		if err := session.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBorrowedDisplayRemainsOwnedByCaller(t *testing.T) {
	t.Setenv("DISPLAY", ":321")
	session, err := Ensure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if session.cmd != nil || session.ChromeEnv()[0] != "DISPLAY=:321" {
		t.Fatal("borrowed display was replaced")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
}
