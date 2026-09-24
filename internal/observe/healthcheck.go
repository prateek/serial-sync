package observe

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// HealthcheckURLEnv names the environment variable holding the Healthchecks.io
// ping URL. The URL is a credential: anyone holding it can report on the check.
const HealthcheckURLEnv = "SERIAL_SYNC_HEALTHCHECK_URL"

const healthcheckReasonLimit = 1000

// Healthcheck reports one run to a Healthchecks.io check: /start when the run
// begins, the bare URL on success, /fail with a short reason on failure. The
// shared rid lets Healthchecks pair the start with its outcome. A zero value
// or an empty URL makes every ping a no-op. Pings are best effort: a failed
// ping is returned for logging and never changes the run's outcome.
type Healthcheck struct {
	URL    string
	Client *http.Client
	rid    string
}

func NewHealthcheck(pingURL string) *Healthcheck {
	return &Healthcheck{URL: strings.TrimRight(strings.TrimSpace(pingURL), "/"), Client: &http.Client{Timeout: 10 * time.Second}, rid: uuid.NewString()}
}

func (h *Healthcheck) Start(ctx context.Context) error {
	return h.ping(ctx, "/start", "")
}

func (h *Healthcheck) Success(ctx context.Context) error {
	return h.ping(ctx, "", "")
}

func (h *Healthcheck) Fail(ctx context.Context, runErr error) error {
	reason := "run failed"
	if runErr != nil {
		reason = SanitizeHealthcheckReason(runErr.Error())
	}
	return h.ping(ctx, "/fail", reason)
}

func (h *Healthcheck) ping(ctx context.Context, suffix, body string) error {
	if h == nil || h.URL == "" {
		return nil
	}
	target := h.URL + suffix
	if h.rid != "" {
		target += "?rid=" + url.QueryEscape(h.rid)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(body))
	if err != nil {
		// The URL is secret, so the error names only the ping kind.
		return fmt.Errorf("healthcheck%s ping: invalid %s", suffix, HealthcheckURLEnv)
	}
	request.Header.Set("Content-Type", "text/plain; charset=utf-8")
	client := h.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("healthcheck%s ping failed", suffix)
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		return fmt.Errorf("healthcheck%s ping returned HTTP %d", suffix, response.StatusCode)
	}
	return nil
}

var (
	urlPattern     = regexp.MustCompile(`https?://[^\s"']+`)
	secretPattern  = regexp.MustCompile(`(?i)(password|passwd|token|secret|cookie|session_id|authorization|api[_-]?key)(["']?\s*[:=]\s*["']?)[^\s"',;&]+`)
	controlPattern = regexp.MustCompile(`[\x00-\x08\x0b-\x1f\x7f]`)
)

// SanitizeHealthcheckReason trims a run error to something safe to send to a
// third-party service: URLs lose their query and fragment, credential-looking
// assignments are masked, control characters are dropped and the text is
// capped.
func SanitizeHealthcheckReason(reason string) string {
	reason = urlPattern.ReplaceAllStringFunc(reason, func(raw string) string {
		parsed, err := url.Parse(raw)
		if err != nil {
			return "[url]"
		}
		parsed.User, parsed.RawQuery, parsed.Fragment = nil, "", ""
		return parsed.String()
	})
	reason = secretPattern.ReplaceAllString(reason, "${1}${2}[redacted]")
	reason = controlPattern.ReplaceAllString(reason, "")
	reason = strings.TrimSpace(reason)
	if len(reason) > healthcheckReasonLimit {
		reason = strings.ToValidUTF8(reason[:healthcheckReasonLimit], "") + "…"
	}
	return reason
}
