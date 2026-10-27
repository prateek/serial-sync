package patreon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/runtime/display"
)

var emailInputSelectors = []string{
	`input[type="email"]`,
	`input[name="email"]`,
	`input[name="identifier"]`,
	`input[autocomplete="email"]`,
}

var passwordInputSelectors = []string{
	`input[type="password"]`,
	`input[name="password"]`,
	`input[autocomplete="current-password"]`,
}

var submitButtonSelectors = []string{
	`button[type="submit"]`,
	`input[type="submit"]`,
	`button[data-tag*="login"]`,
	`button[data-tag*="continue"]`,
	`button[data-testid*="login"]`,
}

var totpInputSelectors = []string{
	`input[autocomplete="one-time-code"]`,
	`input[inputmode="numeric"]`,
	`input[name*="otp"]`,
	`input[name*="code"]`,
	`input[id*="otp"]`,
	`input[id*="code"]`,
}

const (
	patreonBootstrapTimeout    = 7 * time.Minute
	patreonLoginSurfaceTimeout = 4 * time.Minute
	patreonPostSubmitTimeout   = 3 * time.Minute
)

var chromiumProfileLockFiles = []string{
	"SingletonCookie",
	"SingletonLock",
	"SingletonSocket",
}

type pageSnapshot struct {
	URL   string
	Title string
	Text  string
}

func bootstrapWithChromium(ctx context.Context, auth config.AuthProfile, source config.SourceConfig, profileDir string) (domain.AuthState, error) {
	if resolveChromiumBinary() == "" {
		return domain.AuthStateReauthRequired, browserUnavailableError()
	}
	username := strings.TrimSpace(os.Getenv(auth.UsernameEnv))
	password := strings.TrimSpace(os.Getenv(auth.PasswordEnv))
	if username == "" {
		return domain.AuthStateReauthRequired, fmt.Errorf("auth profile %q expects username in %s", auth.ID, auth.UsernameEnv)
	}
	if password == "" {
		return domain.AuthStateReauthRequired, fmt.Errorf("auth profile %q expects password in %s", auth.ID, auth.PasswordEnv)
	}
	loginCtx, loginCancel := context.WithTimeout(ctx, patreonBootstrapTimeout)
	defer loginCancel()
	browserCtx, closeBrowser, err := startChromium(loginCtx, profileDir)
	if err != nil {
		return domain.AuthStateReauthRequired, err
	}
	defer closeBrowser()
	return loginWithChromium(browserCtx, auth, source, username, password, "https://www.patreon.com/login")
}

func startChromium(ctx context.Context, profileDir string) (context.Context, func(), error) {
	chromePath := resolveChromiumBinary()
	if chromePath == "" {
		return nil, nil, browserUnavailableError()
	}
	if err := prepareChromiumProfileDir(profileDir); err != nil {
		return nil, nil, err
	}
	displaySession, err := display.Ensure(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("prepare headed browser environment: %w", err)
	}

	allocOptions := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	allocOptions = append(allocOptions,
		chromedp.UserDataDir(profileDir),
		chromedp.Flag("headless", false),
		chromedp.Flag("enable-automation", false),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.WindowSize(1366, 768),
	)
	allocOptions = append(allocOptions, chromedp.ExecPath(chromePath))
	if env := displaySession.ChromeEnv(); len(env) > 0 {
		allocOptions = append(allocOptions, chromedp.Env(env...))
	}
	if shouldDisableChromiumSandbox() {
		allocOptions = append(allocOptions, chromedp.Flag("no-sandbox", true))
	}
	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, allocOptions...)

	browserCtx, browserCancel := chromedp.NewContext(allocCtx)

	return browserCtx, sync.OnceFunc(func() {
		browserCancel()
		allocCancel()
		_ = displaySession.Close()
	}), nil
}

func browserUnavailableError() error {
	return errors.New("Chromium is unavailable; reauthenticate with the full image using setup auth and the same /config and /state mounts, or use setup auth --import-session with an exported serial-sync session bundle; then retry run in the steady image")
}

func loginWithChromium(loginCtx context.Context, auth config.AuthProfile, source config.SourceConfig, username, password, loginURL string) (domain.AuthState, error) {
	if err := chromedp.Run(loginCtx, network.Enable()); err != nil {
		return domain.AuthStateReauthRequired, fmt.Errorf("start Chromium for Patreon auth: %w", err)
	}
	if err := chromedp.Run(loginCtx, chromedp.Navigate(loginURL)); err != nil {
		return domain.AuthStateReauthRequired, fmt.Errorf("open Patreon login page: %w", err)
	}
	emailSelector, authState, err := waitForSelectorOrAuthenticated(loginCtx, emailInputSelectors, auth.SessionPath, "email", patreonLoginSurfaceTimeout)
	if err != nil {
		return authState, err
	}
	if authState == domain.AuthStateAuthenticated {
		return domain.AuthStateAuthenticated, nil
	}
	if err := chromedp.Run(loginCtx,
		chromedp.WaitVisible(emailSelector, chromedp.ByQuery),
		waitForInputPaint(),
		chromedp.Focus(emailSelector, chromedp.ByQuery),
		chromedp.SendKeys(emailSelector, username, chromedp.ByQuery),
	); err != nil {
		return domain.AuthStateReauthRequired, fmt.Errorf("fill Patreon email field: %w", err)
	}
	passwordSelector, _ := waitForSelector(loginCtx, passwordInputSelectors, 2*time.Second)
	if passwordSelector == "" {
		submitSelector, submitErr := waitForSelector(loginCtx, submitButtonSelectors, 10*time.Second)
		if submitErr == nil && submitSelector != "" {
			if err := chromedp.Run(loginCtx, chromedp.SendKeys(submitSelector, kb.Enter, chromedp.ByQuery)); err != nil {
				reportSourceProgress(loginCtx, source.ID, "warn", "Patreon email continuation submission failed", map[string]any{"error": err.Error()})
			}
		}
		passwordSelector, authState, err = waitForSelectorOrAuthenticated(loginCtx, passwordInputSelectors, auth.SessionPath, "password", patreonLoginSurfaceTimeout)
		if err != nil {
			return authState, err
		}
		if authState == domain.AuthStateAuthenticated {
			return domain.AuthStateAuthenticated, nil
		}
	}
	if err := chromedp.Run(loginCtx,
		chromedp.WaitVisible(passwordSelector, chromedp.ByQuery),
		waitForInputPaint(),
		chromedp.Focus(passwordSelector, chromedp.ByQuery),
		chromedp.SendKeys(passwordSelector, password, chromedp.ByQuery),
	); err != nil {
		return domain.AuthStateReauthRequired, fmt.Errorf("fill Patreon password field: %w", err)
	}
	submitSelector, err := waitForSelector(loginCtx, submitButtonSelectors, 10*time.Second)
	if err != nil {
		return inferBrowserState(loginCtx, domain.AuthStateChallengeNeeded), fmt.Errorf("could not find Patreon submit button: %w", err)
	}
	if err := chromedp.Run(loginCtx, chromedp.SendKeys(submitSelector, kb.Enter, chromedp.ByQuery)); err != nil {
		return domain.AuthStateReauthRequired, fmt.Errorf("submit Patreon login form: %w", err)
	}
	deadline := time.Now().Add(patreonPostSubmitTimeout)
	for time.Now().Before(deadline) {
		completedTOTP, err := maybeCompleteTOTPChallenge(loginCtx, auth)
		if err != nil {
			return domain.AuthStateChallengeNeeded, err
		}
		if completedTOTP {
			if err := sleepWithContext(loginCtx, 2*time.Second); err != nil {
				return domain.AuthStateChallengeNeeded, err
			}
		}
		snapshot := currentPageSnapshot(loginCtx)
		if !strings.Contains(strings.ToLower(snapshot.URL), "/login") {
			if err := saveCurrentSession(loginCtx, auth.SessionPath); err == nil {
				return domain.AuthStateAuthenticated, nil
			}
		}
		switch inferSnapshotState(snapshot, "") {
		case domain.AuthStateReauthRequired:
			return domain.AuthStateReauthRequired, fmt.Errorf("Patreon rejected the provided credentials")
		}
		select {
		case <-loginCtx.Done():
			return domain.AuthStateChallengeNeeded, loginCtx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	if err := saveCurrentSession(loginCtx, auth.SessionPath); err == nil {
		return domain.AuthStateAuthenticated, nil
	}
	snapshot := currentPageSnapshot(loginCtx)
	authState = inferSnapshotState(snapshot, domain.AuthStateChallengeNeeded)
	if authState == domain.AuthStateReauthRequired {
		return authState, fmt.Errorf("Patreon rejected the provided credentials")
	}
	return authState, fmt.Errorf("Patreon login stayed on an interactive challenge or did not reach an authenticated session for source %q", source.ID)
}

func maybeCompleteTOTPChallenge(ctx context.Context, auth config.AuthProfile) (bool, error) {
	if strings.TrimSpace(auth.TOTPSecretEnv) == "" {
		return false, nil
	}
	selector, err := waitForSelector(ctx, totpInputSelectors, 2*time.Second)
	if err != nil || selector == "" {
		return false, nil
	}
	secret := strings.TrimSpace(os.Getenv(auth.TOTPSecretEnv))
	if secret == "" {
		return false, nil
	}
	code, err := generateTOTPCode(secret, time.Now().UTC())
	if err != nil {
		return false, fmt.Errorf("generate Patreon TOTP code from %s: %w", auth.TOTPSecretEnv, err)
	}
	if err := chromedp.Run(ctx,
		chromedp.WaitVisible(selector, chromedp.ByQuery),
		waitForInputPaint(),
		chromedp.Focus(selector, chromedp.ByQuery),
		chromedp.SendKeys(selector, code, chromedp.ByQuery),
	); err != nil {
		return false, fmt.Errorf("fill Patreon TOTP field: %w", err)
	}
	submitSelector, err := waitForSelector(ctx, submitButtonSelectors, 5*time.Second)
	if err == nil && submitSelector != "" {
		if submitErr := chromedp.Run(ctx, chromedp.SendKeys(submitSelector, kb.Enter, chromedp.ByQuery)); submitErr != nil {
			return false, fmt.Errorf("submit Patreon TOTP form: %w", submitErr)
		}
	}
	return true, nil
}

// generateTOTPCode accepts a bare base32 secret or an otpauth://totp URI, the
// form password managers such as 1Password export, and honors the URI's period,
// digits, and algorithm.
func generateTOTPCode(value string, now time.Time) (string, error) {
	if !strings.HasPrefix(strings.ToLower(value), "otpauth://") {
		return totp.GenerateCode(value, now)
	}
	key, err := otp.NewKeyFromURL(value)
	if err != nil {
		// url.Parse errors quote the input, which would leak the secret.
		return "", errors.New("TOTP value is not a valid otpauth:// URI")
	}
	if !strings.EqualFold(key.Type(), "totp") {
		return "", fmt.Errorf("otpauth URI type %q is not supported; use a totp URI", key.Type())
	}
	if key.Secret() == "" {
		return "", errors.New("otpauth URI has no secret parameter")
	}
	return totp.GenerateCodeCustom(key.Secret(), now, totp.ValidateOpts{
		Period:    uint(key.Period()),
		Digits:    key.Digits(),
		Algorithm: key.Algorithm(),
	})
}

func resolveChromiumBinary() string {
	for _, candidate := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome"} {
		path, err := exec.LookPath(candidate)
		if err == nil {
			return path
		}
	}
	return ""
}

func shouldDisableChromiumSandbox() bool {
	if raw := strings.TrimSpace(os.Getenv("SERIAL_SYNC_CHROME_NO_SANDBOX")); raw != "" {
		enabled, err := strconv.ParseBool(raw)
		if err == nil {
			return enabled
		}
	}
	return os.Geteuid() == 0
}

func waitForSelector(ctx context.Context, selectors []string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		selector, err := firstVisibleSelector(ctx, selectors)
		if err == nil && selector != "" {
			return selector, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return "", context.DeadlineExceeded
}

func waitForSelectorOrAuthenticated(ctx context.Context, selectors []string, sessionPath, fieldLabel string, timeout time.Duration) (string, domain.AuthState, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		selector, err := firstVisibleSelector(ctx, selectors)
		if err == nil && selector != "" {
			return selector, "", nil
		}
		snapshot := currentPageSnapshot(ctx)
		if !strings.Contains(strings.ToLower(snapshot.URL), "/login") {
			if err := saveCurrentSession(ctx, sessionPath); err == nil {
				return "", domain.AuthStateAuthenticated, nil
			}
		}
		if inferSnapshotState(snapshot, "") == domain.AuthStateReauthRequired {
			return "", domain.AuthStateReauthRequired, fmt.Errorf("Patreon rejected the provided credentials before the %s field became available", fieldLabel)
		}
		select {
		case <-ctx.Done():
			return "", domain.AuthStateChallengeNeeded, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return selectorWaitError(fieldLabel, currentPageSnapshot(ctx))
}

func selectorWaitError(fieldLabel string, snapshot pageSnapshot) (string, domain.AuthState, error) {
	authState := inferSnapshotState(snapshot, domain.AuthStateChallengeNeeded)
	switch authState {
	case domain.AuthStateChallengeNeeded:
		return "", authState, fmt.Errorf("Patreon login remained behind an interactive challenge before the %s field became available", fieldLabel)
	case domain.AuthStateReauthRequired:
		return "", authState, fmt.Errorf("Patreon rejected the provided credentials before the %s field became available", fieldLabel)
	default:
		return "", authState, fmt.Errorf("Patreon login did not expose the %s field before timeout", fieldLabel)
	}
}

func prepareChromiumProfileDir(profileDir string) error {
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		return err
	}
	for _, name := range chromiumProfileLockFiles {
		path := filepath.Join(profileDir, name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove stale Chromium lock %s: %w", path, err)
		}
	}
	return nil
}

func firstVisibleSelector(ctx context.Context, selectors []string) (string, error) {
	payload, err := json.Marshal(selectors)
	if err != nil {
		return "", err
	}
	script := fmt.Sprintf(`(() => {
		const selectors = %s;
		for (const selector of selectors) {
			const element = document.querySelector(selector);
			if (!element) continue;
			const rect = element.getBoundingClientRect();
			const style = window.getComputedStyle(element);
			if (rect.width === 0 || rect.height === 0) continue;
			if (style.display === 'none' || style.visibility === 'hidden') continue;
			return selector;
		}
		return '';
	})()`, payload)
	var selector string
	if err := chromedp.Run(ctx, chromedp.Evaluate(script, &selector)); err != nil {
		return "", err
	}
	return selector, nil
}

func saveCurrentSession(ctx context.Context, sessionPath string) error {
	var cookies []*network.Cookie
	err := chromedp.Run(ctx, chromedp.ActionFunc(func(actionCtx context.Context) error {
		var err error
		cookies, err = network.GetCookies().
			WithURLs([]string{"https://www.patreon.com", "https://www.patreon.com/home"}).
			Do(actionCtx)
		return err
	}))
	if err != nil {
		return err
	}
	if len(cookies) == 0 {
		return errors.New("no Patreon cookies available to persist")
	}
	var userAgent string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`navigator.userAgent`, &userAgent)); err != nil {
		userAgent = defaultUserAgent
	}
	bundle := sessionBundle{
		Provider:  "patreon",
		SavedAt:   time.Now().UTC(),
		UserAgent: firstNonEmpty(userAgent, defaultUserAgent),
		Cookies:   make([]sessionCookie, 0, len(cookies)),
	}
	for _, cookie := range cookies {
		sameSite := ""
		if cookie.SameSite != "" {
			sameSite = string(cookie.SameSite)
		}
		bundle.Cookies = append(bundle.Cookies, sessionCookie{
			Name:     cookie.Name,
			Value:    cookie.Value,
			Domain:   cookie.Domain,
			Path:     cookie.Path,
			Expires:  cookie.Expires,
			HTTPOnly: cookie.HTTPOnly,
			Secure:   cookie.Secure,
			SameSite: sameSite,
		})
	}
	return saveSessionBundle(sessionPath, bundle)
}

func currentPageSnapshot(ctx context.Context) pageSnapshot {
	snapshot := pageSnapshot{}
	_ = chromedp.Run(ctx,
		chromedp.Location(&snapshot.URL),
		chromedp.Title(&snapshot.Title),
		chromedp.Evaluate(`document.body ? document.body.innerText : ''`, &snapshot.Text),
	)
	return snapshot
}

func inferBrowserState(ctx context.Context, fallback domain.AuthState) domain.AuthState {
	return inferSnapshotState(currentPageSnapshot(ctx), fallback)
}

func inferSnapshotState(snapshot pageSnapshot, fallback domain.AuthState) domain.AuthState {
	blob := strings.ToLower(strings.Join([]string{snapshot.URL, snapshot.Title, snapshot.Text}, "\n"))
	switch {
	case strings.Contains(blob, "captcha"),
		strings.Contains(blob, "just a moment"),
		strings.Contains(blob, "security check"),
		strings.Contains(blob, "verify it"),
		strings.Contains(blob, "verify your identity"),
		strings.Contains(blob, "two-factor"),
		strings.Contains(blob, "two factor"),
		strings.Contains(blob, "one-time code"),
		strings.Contains(blob, "authenticator app"):
		return domain.AuthStateChallengeNeeded
	case strings.Contains(blob, "incorrect password"),
		strings.Contains(blob, "incorrect email"),
		strings.Contains(blob, "unable to log you in"),
		strings.Contains(blob, "could not log you in"),
		strings.Contains(blob, "invalid email"),
		strings.Contains(blob, "invalid password"):
		return domain.AuthStateReauthRequired
	default:
		return fallback
	}
}

func waitForInputPaint() chromedp.Action {
	// DOM visibility was insufficient for reliable input on cold browser startup.
	return chromedp.ActionFunc(func(ctx context.Context) error {
		paintCtx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		err := chromedp.Evaluate(`new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve(true))))`, nil, func(p *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams { return p.WithAwaitPromise(true) }).Do(paintCtx)
		if err != nil {
			return fmt.Errorf("wait for Patreon input paint readiness: %w", err)
		}
		return nil
	})
}
