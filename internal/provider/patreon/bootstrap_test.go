package patreon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/provider"
)

func TestBrowserUnavailableGivesReauthHandoff(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	path := filepath.Join(t.TempDir(), "session.json")
	state, err := bootstrapWithChromium(context.Background(), config.AuthProfile{ID: "fixture", SessionPath: path}, config.SourceConfig{}, browserFixtureProfile(t))
	if state != domain.AuthStateReauthRequired || err == nil || !strings.Contains(err.Error(), "full image") || !strings.Contains(err.Error(), "--import-session") {
		t.Fatalf("bootstrap = %s, %v; want actionable full-image/import handoff", state, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("session changed: %v", err)
	}
}

func TestSteadySessionRecovery(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var expired atomic.Bool
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("User-Agent") != "fixture-browser-agent" {
			t.Errorf("user agent = %q", r.Header.Get("User-Agent"))
		}
		cookie, err := r.Cookie("session_id")
		if err != nil || cookie.Value != "fixture-session" {
			t.Errorf("saved cookie was not reused")
		}
		if expired.Load() {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"data":{"id":"reader"},"included":[{"id":"42","type":"campaign","attributes":{"vanity":"ExampleCreator","name":"Fixture"}}]}`)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "session.json")
	host, _ := url.Parse(server.URL)
	bundle := sessionBundle{Provider: "patreon", UserAgent: "fixture-browser-agent", Cookies: []sessionCookie{{Name: "session_id", Value: "fixture-session", Domain: host.Hostname(), Path: "/"}}}
	if err := saveSessionBundle(path, bundle); err != nil {
		t.Fatal(err)
	}
	client := New()
	client.apiBaseURL = server.URL
	auth := config.AuthProfile{ID: "fixture", Mode: "username_password", SessionPath: path}
	source := config.SourceConfig{ID: "fixture", URL: "https://www.patreon.com/c/ExampleCreator/posts"}
	if _, state, err := client.ensureLiveSession(context.Background(), auth, source); err != nil || state != domain.AuthStateAuthenticated {
		t.Fatalf("reuse: %s %v", state, err)
	}
	profile := client.profiles[auth.ID]
	expired.Store(true)
	if _, state, err := client.ensureLiveSession(context.Background(), auth, source); err == nil || state != domain.AuthStateReauthRequired || !strings.Contains(err.Error(), "full image") {
		t.Fatalf("expired: %s %v", state, err)
	}
	expired.Store(false)
	bundle.SavedAt = time.Now()
	if err := saveSessionBundle(path, bundle); err != nil {
		t.Fatal(err)
	}
	if _, state, err := client.ensureLiveSession(context.Background(), auth, source); err != nil || state != domain.AuthStateAuthenticated {
		t.Fatalf("reauth: %s %v", state, err)
	}
	if client.profiles[auth.ID].budget != profile.budget || client.profiles[auth.ID].client != profile.client {
		t.Fatal("refresh replaced shared budget/client")
	}
	if requests.Load() != 3 {
		t.Fatalf("requests = %d, want 3", requests.Load())
	}
}

func requireBrowserFixture(t *testing.T) {
	t.Helper()
	if os.Getenv("SERIAL_SYNC_TEST_BROWSER") != "1" {
		t.Skip("set SERIAL_SYNC_TEST_BROWSER=1 in the full container for synthetic browser qualification")
	}
}

func TestChromiumSyntheticHumanMFA(t *testing.T) {
	testSyntheticMFA(t, true)
}

func TestChromiumIncompleteMFAFailureCleanup(t *testing.T) {
	testSyntheticMFA(t, false)
}

func testSyntheticMFA(t *testing.T, complete bool) {
	t.Helper()
	requireBrowserFixture(t)
	started := time.Now()
	var evidenceMu sync.Mutex
	var evidence []string
	record := func(event string) {
		evidenceMu.Lock()
		defer evidenceMu.Unlock()
		evidence = append(evidence, fmt.Sprintf("%dms %s", time.Since(started).Milliseconds(), event))
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		record("route=" + r.URL.Path)
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/login":
			fmt.Fprint(w, syntheticLoginForm("/login-mfa", `<input type="email"><input type="password">`))
		case "/login-mfa":
			fmt.Fprint(w, syntheticLoginForm("/home", `<p>Verify your identity: one-time code</p><input name="otp">`))
		default:
			fmt.Fprint(w, `<p>Signed in</p>`)
		}

	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	t.Cleanup(cancel)
	browser := startFixtureBrowser(t, ctx)
	if err := chromedp.Run(browser, chromedp.ActionFunc(func(ctx context.Context) error {
		return network.SetCookie("session_id", "synthetic-only").WithDomain(".patreon.com").WithPath("/").WithSecure(true).WithHTTPOnly(true).Do(ctx)
	})); err != nil {
		t.Fatal(err)
	}
	if counts := fixtureProcessCounts(); counts["Xvfb"] != 1 {
		t.Fatalf("expected one owned or borrowed Xvfb, got %v", counts)
	}
	if os.Getenv("DISPLAY") != "" {
		t.Log("borrowed display retained one Xvfb")
	}
	t.Logf("owned runtime processes during synthetic login: %v", fixtureProcessCounts())
	stopObserver := observeSyntheticForm(browser, started)
	t.Cleanup(func() { stopObserver() })
	type humanResult struct {
		stage string
		err   error
	}
	humanDone := make(chan humanResult, 1)
	go func() {
		result := humanResult{stage: "wait-otp"}
		defer func() { record(fmt.Sprintf("human=%s error=%v", result.stage, result.err)); humanDone <- result }()
		record("human=wait-otp")
		if _, result.err = waitForSelector(browser, []string{`input[name="otp"]`}, 15*time.Second); result.err != nil {
			return
		}
		result.err = chromedp.Run(browser,
			chromedp.ActionFunc(func(ctx context.Context) error {
				result.stage = "type-otp"
				return chromedp.SendKeys(`input[name="otp"]`, "123456", chromedp.ByQuery).Do(ctx)
			}),
			chromedp.ActionFunc(func(ctx context.Context) error {
				if !complete {
					result.stage = "withheld-continue"
					return nil
				}
				result.stage = "click-continue"
				return chromedp.SendKeys(`button`, kb.Enter, chromedp.ByQuery).Do(ctx)
			}),
		)
	}()
	path := filepath.Join(t.TempDir(), "session.json")
	state, loginErr := loginWithChromium(recordBootstrapWarnings(browser, record), config.AuthProfile{SessionPath: path}, config.SourceConfig{ID: "synthetic"}, "fixture@example.invalid", "synthetic", server.URL+"/login")
	var human humanResult
	select {
	case human = <-humanDone:
	case <-time.After(time.Second):
		human = humanResult{stage: "result-not-returned", err: fmt.Errorf("human did not return after login ended")}
	}
	lastSnapshot := stopObserver()
	evidenceMu.Lock()
	captured := append([]string(nil), evidence...)
	evidenceMu.Unlock()
	t.Logf("MFA diagnostic: login=%s error=%v human=%s error=%v lastSnapshot=%s events=%v", state, loginErr, human.stage, human.err, lastSnapshot, captured)
	if !complete {
		if state != domain.AuthStateChallengeNeeded || !errors.Is(loginErr, context.DeadlineExceeded) || human.err != nil || human.stage != "withheld-continue" {
			t.Fatalf("incomplete login=%s error=%v; human=%s error=%v", state, loginErr, human.stage, human.err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("incomplete login wrote session: %v", err)
		}
		return
	}
	if loginErr != nil || state != domain.AuthStateAuthenticated || human.err != nil {
		t.Fatalf("login=%s error=%v; human=%s error=%v", state, loginErr, human.stage, human.err)
	}
	bundle, err := loadSessionBundle(path)
	if err != nil {
		t.Fatal(err)
	}
	var agent string
	if err := chromedp.Run(browser, chromedp.Evaluate(`navigator.userAgent`, &agent)); err != nil {
		t.Fatal(err)
	}
	if bundle.UserAgent != agent || len(bundle.Cookies) != 1 || bundle.Cookies[0].Value != "synthetic-only" || !bundle.Cookies[0].HTTPOnly {
		t.Fatalf("incorrect session export: agent match %v, cookie count %d", bundle.UserAgent == agent, len(bundle.Cookies))
	}
}

func TestChromiumCancelAndRestart(t *testing.T) {
	requireBrowserFixture(t)
	profile := browserFixtureProfile(t)
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		browser, closeBrowser, err := startChromium(ctx, profile)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		t.Cleanup(func() { closeBrowser(); assertNoBrowserProcesses(t) })
		if err := chromedp.Run(browser, chromedp.Navigate("about:blank")); err != nil {
			cancel()
			t.Fatal(err)
		}
		process := chromedp.FromContext(browser).Browser.Process()
		cancel()
		done := make(chan struct{})
		go func() { closeBrowser(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("browser cleanup exceeded 5s")
		}
		if err := process.Signal(syscall.Signal(0)); err == nil {
			t.Fatal("browser still alive after cleanup")
		}
		assertNoBrowserProcesses(t)
	}
}

func TestChromiumChallengeTimeout(t *testing.T) {
	requireBrowserFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `<p>Verify your identity</p>`) }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	browser := startFixtureBrowser(t, ctx)
	if err := chromedp.Run(browser, chromedp.Navigate(server.URL+"/login")); err != nil {
		t.Fatal(err)
	}
	loginCtx, stop := context.WithTimeout(browser, 200*time.Millisecond)
	defer stop()
	path := filepath.Join(t.TempDir(), "session.json")
	_, state, err := waitForSelectorOrAuthenticated(loginCtx, emailInputSelectors, path, "email", patreonLoginSurfaceTimeout)
	if err == nil || state != domain.AuthStateChallengeNeeded {
		t.Fatalf("challenge timeout = %s, %v", state, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("incomplete login saved a session: %v", err)
	}
}

func fixtureProcessCounts() map[string]int {
	counts := map[string]int{}
	files, _ := filepath.Glob("/proc/[0-9]*/comm")
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(raw))
		switch name {
		case "chrome", "chromium", "chrome_crashpad", "Xvfb", "fluxbox", "x11vnc", "websockify":
			counts[name]++
		}
	}
	return counts
}

func TestForceBootstrapPreservesSessionOnUnavailableBrowser(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	path := filepath.Join(t.TempDir(), "session.json")
	original := []byte(`{"provider":"patreon","user_agent":"fixture","cookies":[]}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := New().BootstrapAuth(context.Background(), config.AuthProfile{ID: "fixture", Mode: "username_password", SessionPath: path}, config.SourceConfig{ID: "fixture", Provider: "patreon", URL: "https://www.patreon.com/c/ExampleCreator/posts"}, true)
	if err == nil || result.State != domain.AuthStateReauthRequired || !strings.Contains(err.Error(), "full image") {
		t.Fatalf("forced bootstrap = %v, %v", result, err)
	}
	actual, err := os.ReadFile(path)
	if err != nil || string(actual) != string(original) {
		t.Fatalf("forced unavailable bootstrap damaged existing session: %v", err)
	}
}

func browserFixtureProfile(t *testing.T) string {
	t.Helper()
	// The browser UID must traverse the parent; testing.T.TempDir nests under a private root.
	path, err := os.MkdirTemp("", "serial-sync-browser-fixture-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(path); err != nil {
			t.Error(err)
		}
	})
	return path
}

func assertNoBrowserProcesses(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		counts := fixtureProcessCounts()
		if counts["chrome"]+counts["chromium"]+counts["chrome_crashpad"] == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("browser descendants survived cleanup: %v", counts)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestForceBootstrapReplacesSessionAfterSuccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	original := []byte(`{"provider":"patreon","user_agent":"old","cookies":[]}`)
	replacement := []byte(`{"provider":"patreon","user_agent":"new","cookies":[]}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	client := New()
	called := false
	client.bootstrap = func(_ context.Context, auth config.AuthProfile, _ config.SourceConfig, _ string) (domain.AuthState, error) {
		called = true
		prior, err := os.ReadFile(path)
		if err != nil || string(prior) != string(original) {
			t.Fatalf("old session removed before successful replacement: %v", err)
		}
		return domain.AuthStateAuthenticated, os.WriteFile(auth.SessionPath, replacement, 0600)
	}
	result, err := client.BootstrapAuth(context.Background(), config.AuthProfile{ID: "fixture", Mode: "username_password", SessionPath: path}, config.SourceConfig{ID: "fixture", Provider: "patreon", URL: "https://www.patreon.com/c/ExampleCreator/posts"}, true)
	if err != nil || result.State != domain.AuthStateAuthenticated || !called {
		t.Fatalf("force bootstrap = %v, %v; called=%v", result, err, called)
	}
	actual, err := os.ReadFile(path)
	if err != nil || string(actual) != string(replacement) {
		t.Fatalf("replacement not saved: %v", err)
	}
}

func syntheticMFASnapshot(ctx context.Context) (string, error) {
	var safe string
	err := chromedp.Run(ctx, chromedp.Evaluate(`JSON.stringify({path:location.pathname,ready:document.readyState,email:!!document.querySelector('input[type=email]'),password:!!document.querySelector('input[type=password]'),otp:!!document.querySelector('input[name=otp]'),otpLength:document.querySelector('input[name=otp]')?.value.length ?? 0,valid:document.querySelector('form')?.checkValidity() ?? false,focus:document.activeElement?.tagName,clicked:!!window.fixtureClicked,submitted:!!window.fixtureSubmitted,trusted:!!window.fixtureTrusted,enterCount:window.fixtureEnter||0,documentFocused:document.hasFocus(),emailLength:document.querySelector('input[type=email]')?.value.length ?? 0,passwordLength:document.querySelector('input[type=password]')?.value.length ?? 0,innerHeight:innerHeight,buttonTop:document.querySelector('button')?.getBoundingClientRect().top,shifted:!!window.fixtureShifted})`, &safe))
	return safe, err
}

func startFixtureBrowser(t *testing.T, ctx context.Context) context.Context {
	t.Helper()
	browser, closeBrowser, err := startChromium(ctx, browserFixtureProfile(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		t.Logf("browser cleanup before close: %v", fixtureProcessCounts())
		closeBrowser()
		t.Logf("browser cleanup after close: %v", fixtureProcessCounts())
		assertNoBrowserProcesses(t)
		t.Logf("browser cleanup verified before profile deletion: %v", fixtureProcessCounts())
	})
	if err := chromedp.Run(browser, network.Enable()); err != nil {
		t.Fatal(err)
	}
	return browser
}

func syntheticLoginForm(action, fields string) string {
	return fmt.Sprintf(`<form action="%s" onsubmit="window.fixtureSubmitted=true;if(!window.fixtureClicked||!window.fixtureTrusted)event.preventDefault()">%s<button type="submit" onclick="window.fixtureClicked=true;window.fixtureTrusted=event.isTrusted" onkeydown="if(event.key==='Enter')window.fixtureEnter=(window.fixtureEnter||0)+1">Continue</button></form>`, action, fields)
}

func observeSyntheticForm(browser context.Context, started time.Time) func() string {
	ctx, cancel := context.WithCancel(browser)
	done := make(chan struct{})
	var last string
	go func() {
		defer close(done)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			if safe, err := syntheticMFASnapshot(ctx); err == nil {
				last = fmt.Sprintf("%dms %s", time.Since(started).Milliseconds(), safe)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return sync.OnceValue(func() string { cancel(); <-done; return last })
}

func recordBootstrapWarnings(ctx context.Context, record func(string)) context.Context {
	return provider.WithProgress(ctx, provider.ProgressReporterFunc(func(_ context.Context, event provider.ProgressEvent) {
		if event.Level == "warn" {
			record(fmt.Sprintf("%s: %v", event.Message, event.Payload))
		}
	}))
}
