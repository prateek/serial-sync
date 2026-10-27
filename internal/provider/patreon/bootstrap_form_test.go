package patreon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
)

func TestChromiumStagedSubmitForms(t *testing.T) {
	requireBrowserFixture(t)
	t.Setenv("SERIAL_SYNC_FIXTURE_TOTP", "JBSWY3DPEHPK3PXP")
	started := time.Now()
	var mu sync.Mutex
	var routes, warnings []string
	record := func(event string) {
		mu.Lock()
		defer mu.Unlock()
		warnings = append(warnings, fmt.Sprintf("%dms %s", time.Since(started).Milliseconds(), event))
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/favicon.ico" {
			return
		}
		mu.Lock()
		routes = append(routes, r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case "/login":
			fmt.Fprint(w, syntheticLoginForm("/login-password", `<input type="email" required>`))
		case "/login-password":
			fmt.Fprint(w, syntheticLoginForm("/login-mfa", `<input type="password" required>`))
		case "/login-mfa":
			fmt.Fprint(w, syntheticLoginForm("/home", `<input name="otp" autocomplete="one-time-code" pattern="[0-9]{6}" required>`))
		case "/home":
			if len(r.URL.Query().Get("otp")) != 6 {
				t.Error("automatic TOTP did not supply six digits")
			}
			fmt.Fprint(w, `<p>Signed in</p>`)
		default:
			http.NotFound(w, r)
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
	stopObserver := observeSyntheticForm(browser, started)
	t.Cleanup(func() { stopObserver() })

	path := filepath.Join(t.TempDir(), "session.json")
	state, err := loginWithChromium(recordBootstrapWarnings(browser, record), config.AuthProfile{SessionPath: path, TOTPSecretEnv: "SERIAL_SYNC_FIXTURE_TOTP"}, config.SourceConfig{ID: "synthetic"}, "fixture@example.invalid", "synthetic", server.URL+"/login")
	safe := stopObserver()
	mu.Lock()
	observed := append([]string(nil), routes...)
	actions := append([]string(nil), warnings...)
	mu.Unlock()
	t.Logf("staged diagnostic: login=%s error=%v routes=%v lastSnapshot=%s warnings=%v", state, err, observed, safe, actions)
	if err != nil || state != domain.AuthStateAuthenticated {
		t.Fatalf("staged login=%s error=%v routes=%v snapshot=%s warnings=%v", state, err, observed, safe, actions)
	}
	want := []string{"/login", "/login-password", "/login-mfa", "/home"}
	if !reflect.DeepEqual(observed, want) {
		t.Fatalf("routes=%v want %v", observed, want)
	}
	bundle, err := loadSessionBundle(path)
	if err != nil || bundle.Provider != "patreon" || bundle.UserAgent == "" || len(bundle.Cookies) != 1 {
		t.Fatalf("staged login session export invalid: %v", err)
	}
	t.Logf("all three selected submit controls activated trusted click handlers and native form submission: %v", observed)
}

func TestChromiumSubmitPreservesNativeValidation(t *testing.T) {
	requireBrowserFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/home" {
			t.Error("invalid form was submitted")
		}
		fmt.Fprint(w, syntheticLoginForm("/home", `<input type="email" required><input type="password" required>`))
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	browser := startFixtureBrowser(t, ctx)
	loginCtx, stop := context.WithTimeout(browser, 3*time.Second)
	defer stop()
	path := filepath.Join(t.TempDir(), "session.json")
	state, err := loginWithChromium(loginCtx, config.AuthProfile{SessionPath: path}, config.SourceConfig{ID: "synthetic"}, "invalid-email", "synthetic", server.URL+"/login")
	if state != domain.AuthStateChallengeNeeded || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("invalid form login=%s error=%v", state, err)
	}
	safe, err := syntheticMFASnapshot(browser)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Path                               string
		Valid, Clicked, Submitted, Trusted bool
		EmailLength, PasswordLength        int
	}
	if err := json.Unmarshal([]byte(safe), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Path != "/login" || snapshot.Valid || !snapshot.Clicked || snapshot.Submitted || !snapshot.Trusted || snapshot.EmailLength != len("invalid-email") || snapshot.PasswordLength != len("synthetic") {
		t.Fatalf("native validation or trusted handler lost: %s", safe)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("invalid form wrote a session: %v", err)
	}
	t.Logf("selected button's trusted click handler ran; native validation blocked submission: %s", safe)
}

func TestChromiumSubmitControlLayoutShift(t *testing.T) {
	requireBrowserFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<form onsubmit="window.fixtureSubmitted=true;event.preventDefault()">
<input type="email" required><input type="password" required>
<button type="submit" onclick="window.fixtureClicked=true;window.fixtureTrusted=event.isTrusted">Continue</button>
</form><script>
document.querySelector('button').addEventListener('focus',()=>{
 const banner=document.createElement('div');banner.style.height='60px';document.body.prepend(banner);window.fixtureShifted=true;
},{once:true});
</script>`)
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	browser := startFixtureBrowser(t, ctx)
	const email = "fixture@example.invalid"
	const password = "synthetic"
	if err := chromedp.Run(browser, chromedp.Navigate(server.URL+"/login"), waitForInputPaint(), chromedp.SendKeys(`input[type=email]`, email, chromedp.ByQuery), chromedp.SendKeys(`input[type=password]`, password, chromedp.ByQuery)); err != nil {
		t.Fatal(err)
	}
	type formState struct {
		Ready, Focus                                                 string
		EmailLength, PasswordLength                                  int
		DocumentFocused, Valid, Shifted, Clicked, Submitted, Trusted bool
		ButtonTop                                                    float64
	}
	read := func() formState {
		t.Helper()
		safe, err := syntheticMFASnapshot(browser)
		if err != nil {
			t.Fatal(err)
		}
		var state formState
		if err := json.Unmarshal([]byte(safe), &state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	before := read()
	if before.Ready != "complete" || !before.DocumentFocused || !before.Valid || before.EmailLength != len(email) || before.PasswordLength != len(password) || before.Shifted || before.Focus != "INPUT" {
		t.Fatalf("probe preconditions not met: %+v", before)
	}
	t.Logf("prepared browser and populated valid fields before single activation: %+v", before)
	activation := "keyboard"
	action := chromedp.Action(chromedp.SendKeys(`button`, kb.Enter, chromedp.ByQuery))
	if os.Getenv("SERIAL_SYNC_TEST_MOUSE_SUBMIT") == "1" {
		activation = "mouse"
		action = chromedp.Click(`button`, chromedp.ByQuery)
	}
	if err := chromedp.Run(browser, action); err != nil {
		t.Fatal(err)
	}
	after := read()
	t.Logf("single %s activation result: %+v", activation, after)
	if !after.Shifted || after.ButtonTop-before.ButtonTop != 60 {
		t.Fatalf("probe did not observe required 60px shift: before=%+v after=%+v", before, after)
	}
	if !after.Clicked || !after.Submitted || !after.Trusted {
		t.Fatalf("selected submit control was not activated through its trusted handler after observed shift: %+v", after)
	}
}

func TestChromiumInputPaintWaitIsBounded(t *testing.T) {
	requireBrowserFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	browser := startFixtureBrowser(t, ctx)
	err := chromedp.Run(browser,
		chromedp.Evaluate(`window.requestAnimationFrame=()=>0`, nil),
		waitForInputPaint(),
	)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "paint readiness") {
		t.Fatalf("suppressed animation frames must fail paint readiness: %v", err)
	}
	var ready bool
	if err := chromedp.Run(browser, chromedp.Evaluate(`true`, &ready)); err != nil || !ready {
		t.Fatalf("paint timeout canceled the parent browser: ready=%v error=%v", ready, err)
	}
}
