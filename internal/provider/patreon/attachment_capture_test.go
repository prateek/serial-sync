package patreon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prateek/serial-sync/internal/artifact"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/provider"
)

type attachmentTransport func(*http.Request) (*http.Response, error)

func (f attachmentTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type captureHarness struct {
	client   *Client
	auth     config.AuthProfile
	source   config.SourceConfig
	requests int
	respond  func(*http.Request) (*http.Response, error)
}

func newCaptureHarness(t *testing.T) *captureHarness {
	t.Helper()
	h := &captureHarness{
		client: New(),
		auth:   config.AuthProfile{ID: "fixture", SessionPath: filepath.Join(t.TempDir(), "session.json")},
		source: config.SourceConfig{ID: "harbor", Provider: "patreon", URL: "https://fixture.invalid/posts"},
	}
	if err := os.WriteFile(h.auth.SessionPath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h.respond = func(r *http.Request) (*http.Response, error) { return attachmentResponse("original bytes"), nil }
	h.connect(t)
	return h
}

func (h *captureHarness) connect(t *testing.T) {
	t.Helper()
	profile, err := h.client.profileSessions(h.auth)
	if err != nil {
		t.Fatal(err)
	}
	profile.client.Transport = attachmentTransport(func(r *http.Request) (*http.Response, error) {
		h.requests++
		profile.budget.mu.Lock()
		inFlight := profile.budget.inFlight
		profile.budget.mu.Unlock()
		if inFlight != 1 {
			t.Fatal("attachment bypassed shared profile request budget")
		}
		return h.respond(r)
	})
}

func attachmentResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/epub+zip"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func captureDocument(revision int64) provider.ReleaseDocument {
	return provider.ReleaseDocument{Normalized: domain.NormalizedRelease{
		ProviderReleaseID: "1", EditedAt: time.Unix(revision, 0),
		Attachments: []domain.Attachment{{FileName: "chapter.epub", MIMEType: "application/epub+zip", DownloadURL: "https://fixture.invalid/chapter?token=one"}},
	}}
}

func (h *captureHarness) prepare(t *testing.T, doc provider.ReleaseDocument) domain.Attachment {
	t.Helper()
	prepared, state, err := h.client.PrepareRelease(context.Background(), h.auth, h.source, doc, domain.TrackDecision{ContentStrategy: domain.ContentStrategyAttachmentOnly})
	if err != nil {
		t.Fatal(err)
	}
	if state != domain.AuthStateAuthenticated {
		t.Fatalf("auth state = %s", state)
	}
	return prepared.Normalized.Attachments[0]
}

func checkAttachmentBytes(t *testing.T, attachment domain.Attachment, want string) {
	t.Helper()
	body, err := os.ReadFile(attachment.LocalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != want {
		t.Fatalf("capture = %q, want %q", body, want)
	}
}

func TestPrepareReleaseCorrectedAttachmentRetainsEarlierCapture(t *testing.T) {
	h := newCaptureHarness(t)
	first := h.prepare(t, captureDocument(1))
	h.respond = func(r *http.Request) (*http.Response, error) { return attachmentResponse("corrected bytes"), nil }
	second := h.prepare(t, captureDocument(2))
	checkAttachmentBytes(t, second, "corrected bytes")
	checkAttachmentBytes(t, first, "original bytes")
	if first.LocalPath == second.LocalPath || first.SHA256 == second.SHA256 || h.requests != 2 {
		t.Fatalf("correction reused earlier capture: first=%+v second=%+v requests=%d", first, second, h.requests)
	}
}

func TestPrepareReleaseReusesVerifiedRevisionAcrossCredentialRotationAndClients(t *testing.T) {
	for _, mediaID := range []string{"", "media-1"} {
		t.Run("media="+mediaID, func(t *testing.T) {
			h := newCaptureHarness(t)
			doc := captureDocument(1)
			doc.Normalized.Attachments[0].ProviderMediaID = mediaID
			first := h.prepare(t, doc)
			doc = captureDocument(1)
			doc.Normalized.Attachments[0].ProviderMediaID = mediaID
			doc.Normalized.Attachments[0].DownloadURL = "https://fixture.invalid/chapter?token=two&expires=999"
			if mediaID != "" {
				doc.Normalized.Attachments[0].DownloadURL = "https://cdn.fixture.invalid/rotated-path?token=two"
			}
			h.client = New()
			h.connect(t)
			second := h.prepare(t, doc)
			checkAttachmentBytes(t, second, "original bytes")
			if first.LocalPath != second.LocalPath || h.requests != 1 {
				t.Fatalf("unchanged revision fetched again: requests=%d", h.requests)
			}
		})
	}
}

func TestPrepareReleaseAttachmentIdentityChanges(t *testing.T) {
	for _, change := range []string{"media", "resource", "source", "release"} {
		t.Run(change, func(t *testing.T) {
			h := newCaptureHarness(t)
			first := h.prepare(t, captureDocument(1))
			doc := captureDocument(1)
			switch change {
			case "media":
				doc.Normalized.Attachments[0].ProviderMediaID = "replacement"
			case "resource":
				doc.Normalized.Attachments[0].DownloadURL = "https://fixture.invalid/replacement"
			case "source":
				h.source.ID = "another"
			case "release":
				doc.Normalized.ProviderReleaseID = "2"
			}
			second := h.prepare(t, doc)
			if h.requests != 2 || first.LocalPath == second.LocalPath {
				t.Fatal("distinct provenance reused earlier capture")
			}
		})
	}
}

func TestPrepareReleaseRepairsInvalidCapture(t *testing.T) {
	for _, damage := range []string{"same-size corruption", "truncation", "missing payload", "missing manifest", "invalid manifest"} {
		t.Run(damage, func(t *testing.T) {
			h := newCaptureHarness(t)
			first := h.prepare(t, captureDocument(1))
			manifest := filepath.Join(filepath.Dir(filepath.Dir(first.LocalPath)), "capture.json")
			var err error
			switch damage {
			case "same-size corruption":
				err = os.WriteFile(first.LocalPath, []byte("tampered bytes"), 0o600)
			case "truncation":
				err = os.Truncate(first.LocalPath, 3)
			case "missing payload":
				err = os.Remove(first.LocalPath)
			case "missing manifest":
				err = os.Remove(manifest)
			case "invalid manifest":
				err = os.WriteFile(manifest, []byte(`{}`), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			doc := captureDocument(1)
			// A caller may retry the document returned by PrepareRelease.
			doc.Normalized.Attachments[0] = first
			repaired := h.prepare(t, doc)
			checkAttachmentBytes(t, repaired, "original bytes")
			if h.requests != 2 {
				t.Fatalf("invalid capture reused: requests=%d", h.requests)
			}
		})
	}
}

type brokenAttachmentBody struct{}

func (brokenAttachmentBody) Read(p []byte) (int, error) {
	return copy(p, "partial bytes"), io.ErrUnexpectedEOF
}
func (brokenAttachmentBody) Close() error { return nil }

type cancelAttachmentBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b cancelAttachmentBody) Close() error { b.cancel(); return b.ReadCloser.Close() }

func TestPrepareReleaseFailedDownloadCannotBecomeCapture(t *testing.T) {
	for _, failure := range []string{"body error", "partial response", "declared length mismatch", "empty body", "cancelled during request", "cancelled after read", "checksum"} {
		t.Run(failure, func(t *testing.T) {
			h := newCaptureHarness(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			h.respond = func(r *http.Request) (*http.Response, error) {
				response := attachmentResponse("original bytes")
				switch failure {
				case "body error":
					response.Body = brokenAttachmentBody{}
				case "partial response":
					response.StatusCode = http.StatusPartialContent
				case "declared length mismatch":
					response.ContentLength = 1000
				case "empty body":
					response = attachmentResponse("")
				case "cancelled during request":
					cancel()
					return nil, r.Context().Err()
				case "cancelled after read":
					response.Body = cancelAttachmentBody{response.Body, cancel}
				}
				return response, nil
			}
			doc := captureDocument(1)
			if failure == "checksum" {
				doc.Normalized.Attachments[0].SHA256 = strings.Repeat("0", 64)
			}
			prepared, state, err := h.client.PrepareRelease(ctx, h.auth, h.source, doc, domain.TrackDecision{ContentStrategy: domain.ContentStrategyAttachmentOnly})
			if err == nil {
				t.Fatal("failed download accepted")
			}
			if strings.HasPrefix(failure, "cancelled") && !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v, want cancellation", err)
			}
			if failure != "body error" && failure != "cancelled during request" && state != domain.AuthStateAuthenticated {
				t.Fatalf("capture failure changed authentication state to %s", state)
			}
			if prepared.Normalized.Attachments[0].LocalPath != "" {
				t.Fatal("failed download acquired a local capture path")
			}
			manifests, err := filepath.Glob(filepath.Join(attachmentCacheRoot(h.auth), "*", "capture.json"))
			if err != nil || len(manifests) != 0 {
				t.Fatalf("failed download installed manifests: %v, %v", manifests, err)
			}
			h.respond = func(r *http.Request) (*http.Response, error) { return attachmentResponse("original bytes"), nil }
			repaired := h.prepare(t, captureDocument(1))
			checkAttachmentBytes(t, repaired, "original bytes")
			if h.requests != 2 {
				t.Fatalf("retry requests = %d, want 2", h.requests)
			}
		})
	}
}

func TestHydrateDumpReleasesExportsVerifiedGenerationsForOfflineMaterialization(t *testing.T) {
	h := newCaptureHarness(t)
	type generation struct {
		recordPath string
		want       string
	}
	hydrate := func(revision int64, want string) generation {
		t.Helper()
		fixtureDir := t.TempDir()
		docs, _, err := h.client.HydrateDumpReleases(context.Background(), h.auth, h.source, []provider.ReleaseDocument{captureDocument(revision)}, fixtureDir)
		if err != nil {
			t.Fatal(err)
		}
		attachment := docs[0].Normalized.Attachments[0]
		if attachment.LocalPath != fixtureAttachmentPath(fixtureDir, "1", "chapter.epub") {
			t.Fatal("dump attachment escaped generation")
		}
		record, err := json.Marshal(struct {
			Normalized domain.NormalizedRelease `json:"normalized"`
		}{docs[0].Normalized})
		if err != nil {
			t.Fatal(err)
		}
		recordPath := filepath.Join(fixtureDir, "posts.ndjson")
		if err := os.WriteFile(recordPath, append(record, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		return generation{recordPath: recordPath, want: want}
	}
	generations := []generation{hydrate(1, "original bytes"), hydrate(1, "original bytes")}
	if h.requests != 1 {
		t.Fatal("new generation did not reuse verified bytes")
	}
	h.respond = func(r *http.Request) (*http.Response, error) { return attachmentResponse("corrected bytes"), nil }
	generations = append(generations, hydrate(2, "corrected bytes"))
	if h.requests != 2 {
		t.Fatalf("dump requests = %d, want 2", h.requests)
	}
	if err := os.RemoveAll(attachmentCacheRoot(h.auth)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(h.auth.SessionPath); err != nil {
		t.Fatal(err)
	}
	for _, generation := range generations {
		raw, err := os.ReadFile(generation.recordPath)
		if err != nil {
			t.Fatal(err)
		}
		var record struct {
			Normalized domain.NormalizedRelease `json:"normalized"`
		}
		if err := json.Unmarshal(raw, &record); err != nil {
			t.Fatal(err)
		}
		attachment := record.Normalized.Attachments[0]
		materializer := artifact.New(t.TempDir())
		source := domain.Source{ID: h.source.ID}
		track := domain.StoryTrack{TrackKey: "harbor", TrackName: "Harbor"}
		release := domain.Release{ID: "1", ProviderReleaseID: "1", Title: "Harbor Chapter 1"}
		decision := domain.TrackDecision{ContentStrategy: domain.ContentStrategyAttachmentOnly, OutputFormat: domain.OutputFormatPreserve,
			SelectedContent: &domain.ContentReference{Kind: "attachment", FileName: attachment.FileName, SHA256: attachment.SHA256},
		}
		plan, err := materializer.Plan(context.Background(), source, track, release, record.Normalized, decision, nil)
		if err != nil {
			t.Fatal(err)
		}
		if plan.SHA256 != attachment.SHA256 {
			t.Fatal("offline reading-copy bytes do not match captured digest")
		}
		output, err := materializer.Materialize(context.Background(), source, track, release, plan)
		if err != nil {
			t.Fatal(err)
		}
		checkAttachmentBytes(t, domain.Attachment{LocalPath: output.StorageRef}, generation.want)
	}
}

func TestPrepareReleaseIgnoresUnverifiedLegacyCache(t *testing.T) {
	h := newCaptureHarness(t)
	legacy := filepath.Join(sessionCacheRoot(h.auth.SessionPath), "attachments", h.source.ID, "1", "chapter.epub")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("unverified older bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	attachment := h.prepare(t, captureDocument(1))
	checkAttachmentBytes(t, attachment, "original bytes")
	if h.requests != 1 || attachment.LocalPath == legacy {
		t.Fatal("legacy filename-only cache trusted")
	}
}

func TestPrepareReleaseManifestInstallationFailureDoesNotCommitPayload(t *testing.T) {
	h := newCaptureHarness(t)
	first := h.prepare(t, captureDocument(1))
	manifest := filepath.Join(filepath.Dir(filepath.Dir(first.LocalPath)), "capture.json")
	if err := os.Remove(manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(manifest, 0o755); err != nil {
		t.Fatal(err)
	}
	prepared, _, err := h.client.PrepareRelease(context.Background(), h.auth, h.source, captureDocument(1), domain.TrackDecision{ContentStrategy: domain.ContentStrategyAttachmentOnly})
	if err == nil || prepared.Normalized.Attachments[0].LocalPath != "" {
		t.Fatal("failed manifest installation committed capture")
	}
	if err := os.Remove(manifest); err != nil {
		t.Fatal(err)
	}
	repaired := h.prepare(t, captureDocument(1))
	checkAttachmentBytes(t, repaired, "original bytes")
	if h.requests != 3 {
		t.Fatalf("uncommitted payload reused: requests=%d, want 3", h.requests)
	}
}

func TestPrepareReleaseDownloadsOnlySelectedAttachment(t *testing.T) {
	h := newCaptureHarness(t)
	doc := captureDocument(1)
	doc.Normalized.Attachments = append(doc.Normalized.Attachments, domain.Attachment{
		FileName: "appendix.pdf", MIMEType: "application/pdf", DownloadURL: "https://fixture.invalid/appendix",
	})
	prepared, _, err := h.client.PrepareRelease(context.Background(), h.auth, h.source, doc, domain.TrackDecision{
		ContentStrategy: domain.ContentStrategyAttachmentOnly, AttachmentPriority: []string{"epub", "pdf"},
		SelectedContent: &domain.ContentReference{Kind: "attachment", AttachmentIndex: 1, FileName: "appendix.pdf"},
	})
	if err != nil {
		t.Fatal(err)
	}
	checkAttachmentBytes(t, prepared.Normalized.Attachments[1], "original bytes")
	if prepared.Normalized.Attachments[0].LocalPath != "" || h.requests != 1 {
		t.Fatal("preparation downloaded an unselected attachment")
	}
}
