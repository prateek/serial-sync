package observe_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/prateek/serial-sync/internal/observe"
)

type ping struct {
	path, rid, body string
}

func healthcheckServer(t *testing.T, status int) (*httptest.Server, func() []ping) {
	t.Helper()
	var mu sync.Mutex
	var pings []ping
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		pings = append(pings, ping{path: r.URL.Path, rid: r.URL.Query().Get("rid"), body: string(body)})
		mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	return server, func() []ping {
		mu.Lock()
		defer mu.Unlock()
		return append([]ping(nil), pings...)
	}
}

func TestHealthcheckPingsStartAndSuccess(t *testing.T) {
	server, pings := healthcheckServer(t, http.StatusOK)
	health := observe.NewHealthcheck(server.URL + "/ping/check-uuid/")
	if err := health.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := health.Success(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := pings()
	if len(got) != 2 || got[0].path != "/ping/check-uuid/start" || got[1].path != "/ping/check-uuid" {
		t.Fatalf("pings = %+v", got)
	}
	if got[0].rid == "" || got[0].rid != got[1].rid {
		t.Fatalf("start and outcome must share one rid: %+v", got)
	}
}

func TestHealthcheckFailSendsSanitizedReason(t *testing.T) {
	server, pings := healthcheckServer(t, http.StatusOK)
	health := observe.NewHealthcheck(server.URL + "/ping/check-uuid")
	runErr := errors.New("fetch https://user:pw@www.patreon.com/api/posts?cursor=abc&access_token=xyz failed: password=hunter2\x1b[31m\n" + strings.Repeat("x", 2000))
	if err := health.Fail(context.Background(), runErr); err != nil {
		t.Fatal(err)
	}
	got := pings()
	if len(got) != 1 || got[0].path != "/ping/check-uuid/fail" {
		t.Fatalf("pings = %+v", got)
	}
	body := got[0].body
	for _, secret := range []string{"access_token", "xyz", "hunter2", "pw@", "cursor", "\x1b"} {
		if strings.Contains(body, secret) {
			t.Fatalf("fail body leaked %q: %q", secret, body)
		}
	}
	if !strings.Contains(body, "https://www.patreon.com/api/posts failed: password=[redacted]") || len(body) > 1100 {
		t.Fatalf("fail body = %q (%d bytes)", body, len(body))
	}
}

func TestHealthcheckWithoutURLDoesNothing(t *testing.T) {
	for _, health := range []*observe.Healthcheck{observe.NewHealthcheck(""), observe.NewHealthcheck("  "), nil} {
		if err := health.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := health.Success(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := health.Fail(context.Background(), errors.New("boom")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHealthcheckErrorsNeverEchoTheURL(t *testing.T) {
	server, _ := healthcheckServer(t, http.StatusNotFound)
	secret := server.URL + "/ping/secret-check-uuid"
	err := observe.NewHealthcheck(secret).Success(context.Background())
	if err == nil || strings.Contains(err.Error(), "secret-check-uuid") {
		t.Fatalf("Success() = %v, want an error without the URL", err)
	}
	server.Close()
	err = observe.NewHealthcheck(secret).Start(context.Background())
	if err == nil || strings.Contains(err.Error(), "secret-check-uuid") {
		t.Fatalf("Start() = %v, want an error without the URL", err)
	}
}
