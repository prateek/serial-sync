package publish

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prateek/serial-sync/internal/domain"
)

type ExecTarget struct {
	ProtocolVersion int
	ID              string
	Command         []string
}

type execPayload struct {
	RunID      string                   `json:"run_id"`
	TargetID   string                   `json:"target_id"`
	TargetKind string                   `json:"target_kind"`
	Command    []string                 `json:"command"`
	Source     domain.Source            `json:"source"`
	Track      domain.StoryTrack        `json:"track"`
	Release    domain.Release           `json:"release"`
	Assignment domain.ReleaseAssignment `json:"assignment"`
	Artifact   domain.Artifact          `json:"artifact"`
}

type hookEvent struct {
	Version  int    `json:"version"`
	EventID  string `json:"event_id"`
	Action   string `json:"action"`
	TargetID string `json:"target_id"`
	*domain.PublishCandidate
	Previous     *domain.PublishRecordBundle `json:"previous,omitempty"`
	Replacements []domain.PublishCandidate   `json:"replacements,omitempty"`
}

func (t *ExecTarget) TargetID() string {
	return t.ID
}

func (t *ExecTarget) Kind() string {
	return "exec"
}

func (t *ExecTarget) Identity(candidate domain.PublishCandidate, _ []domain.PublishRecordBundle) (DeliveryIdentity, error) {
	ref, err := t.ref(candidate)
	if err != nil {
		return DeliveryIdentity{}, err
	}
	return DeliveryIdentity{
		Ref:         ref,
		PublishHash: PublishHash(t.ID, candidate.Artifact.SHA256, t.execPublishSignature(candidate)),
		DesiredKey:  candidate.Artifact.ID + "\x00" + candidate.Artifact.Filename,
		// The hook owns its destination; whether it holds these bytes is the
		// hook's acknowledgement, not something the filesystem can check.
		Intact: true,
	}, nil
}

func (t *ExecTarget) ref(domain.PublishCandidate) (string, error) {
	return ExecTargetRef(t.Command), nil
}

func (t *ExecTarget) execPublishSignature(candidate domain.PublishCandidate) string {
	signature := ExecTargetSignature(t.Command)
	if t.ProtocolVersion == 2 {
		signature += "\x00v2\x00" + candidate.Artifact.Filename
	}
	return signature
}

func (t *ExecTarget) DesiredKey(candidate domain.PublishCandidate) string {
	return candidate.Artifact.ID + "\x00" + candidate.Artifact.Filename
}

func (t *ExecTarget) DesiredKeyForRecord(record domain.PublishRecordBundle) string {
	return record.Artifact.ID + "\x00" + record.Record.Filename
}

func (t *ExecTarget) MatchesDestination(record domain.PublishRecordBundle, candidate domain.PublishCandidate) bool {
	return record.Record.Filename == candidate.Artifact.Filename
}

func (t *ExecTarget) Reorder(candidates []domain.PublishCandidate, _ []domain.PublishRecordBundle, _ []domain.VolumeEdition) ([]domain.PublishCandidate, map[string][]string, error) {
	// The hook owns its destination layout; deliveries never contend on a
	// path here, so the input order stands with no dependencies.
	return candidates, map[string][]string{}, nil
}

func (t *ExecTarget) PublishingRecord(_ domain.PublishCandidate, _ DeliveryIdentity) *domain.PublishRecord {
	// The hook protocol acknowledges deliveries by event id; a failure needs
	// its own failed-status record rather than a provisional ledger entry.
	return nil
}

func (t *ExecTarget) CheckDestination(ref string, ownedHashes []string) (string, error) {
	return "", nil
}

func (t *ExecTarget) AlreadyDelivered(ref string, artifactSHA string) (bool, error) {
	return true, nil
}

func (t *ExecTarget) Deliver(ctx context.Context, runID, eventScope string, candidate domain.PublishCandidate, identity DeliveryIdentity, _ []string) (domain.PublishRecord, error) {
	if len(t.Command) == 0 {
		return domain.PublishRecord{}, fmt.Errorf("exec publisher %q requires a command", t.ID)
	}
	payload, err := json.MarshalIndent(execPayload{
		RunID:      runID,
		TargetID:   t.ID,
		TargetKind: "exec",
		Command:    append([]string(nil), t.Command...),
		Source:     candidate.Source,
		Track:      candidate.Track,
		Release:    candidate.Release,
		Assignment: candidate.Assignment,
		Artifact:   candidate.Artifact,
	}, "", "  ")
	if err != nil {
		return domain.PublishRecord{}, err
	}
	if t.ProtocolVersion == 2 {
		event := hookEvent{Version: 2, Action: "publish", TargetID: t.ID, PublishCandidate: &candidate}
		event.EventID = hookEventID(*t, eventScope, "publish", candidate.Artifact.ID, candidate.Artifact.SHA256, candidate.Artifact.Filename)
		payload, err = json.Marshal(event)
		if err != nil {
			return domain.PublishRecord{}, err
		}
	}

	cmd := exec.CommandContext(ctx, t.Command[0], t.Command[1:]...)
	cmd.Env = append(os.Environ(), execEnv(*t, runID, candidate)...)
	cmd.Stdin = bytes.NewReader(payload)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return domain.PublishRecord{}, fmt.Errorf("exec publisher %q failed: %w%s", t.ID, err, formatExecOutput(&stdout, &stderr))
	}

	return domain.PublishRecord{
		Filename:    candidate.Artifact.Filename,
		ID:          "pub_" + uuid.NewString(),
		ArtifactID:  candidate.Artifact.ID,
		TargetID:    t.ID,
		TargetKind:  "exec",
		TargetRef:   identity.Ref,
		PublishHash: identity.PublishHash,
		PublishedAt: time.Now().UTC(),
		Status:      domain.PublishStatusPublished,
		Message:     combinedExecOutput(&stdout, &stderr),
	}, nil
}

// Capabilities: a versioned hook acknowledges deliveries by event id and
// receives supersede events; a legacy single-file hook has no retirement
// event, so its prior records stay published.
func (t *ExecTarget) Capabilities() Capabilities {
	return Capabilities{PendingRecord: false, Retires: t.ProtocolVersion == 2}
}

func (t *ExecTarget) Retire(ctx context.Context, old domain.PublishRecordBundle, planned, related []domain.PublishCandidate, ownedHashes []string, eventScope string) error {
	if t.ProtocolVersion != 2 {
		return ErrRetirementUnsupported
	}
	old.Artifact.Filename = old.Record.Filename
	parts := []string{"supersede", old.Record.PublishHash, old.Artifact.ID}
	for _, replacement := range related {
		parts = append(parts, replacement.Artifact.ID, replacement.Artifact.SHA256, replacement.Artifact.Filename)
	}
	event := hookEvent{Version: 2, EventID: hookEventID(*t, eventScope, parts...), Action: "supersede", TargetID: t.ID, Previous: &old, Replacements: related}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, t.Command[0], t.Command[1:]...)
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("exec publisher %q supersede failed: %w%s", t.ID, err, formatExecOutput(&stdout, &stderr))
	}
	return nil
}

func hookEventID(target ExecTarget, eventScope string, parts ...string) string {
	if eventScope != "" {
		parts = append([]string{eventScope}, parts...)
	}
	parts = append([]string{target.ID, ExecTargetSignature(target.Command)}, parts...)
	return fmt.Sprintf("evt_%x", sha256.Sum256([]byte(strings.Join(parts, "\x00"))))
}

func ExecTargetRef(command []string) string {
	if len(command) == 0 {
		return ""
	}
	parts := make([]string, 0, len(command))
	for _, part := range command {
		parts = append(parts, strconv.Quote(part))
	}
	return strings.Join(parts, " ")
}

func ExecTargetSignature(command []string) string {
	return strings.Join(command, "\x00")
}

func execEnv(target ExecTarget, runID string, candidate domain.PublishCandidate) []string {
	return []string{
		"SERIAL_SYNC_RUN_ID=" + runID,
		"SERIAL_SYNC_TARGET_ID=" + target.ID,
		"SERIAL_SYNC_TARGET_KIND=exec",
		"SERIAL_SYNC_SOURCE_ID=" + candidate.Source.ID,
		"SERIAL_SYNC_SOURCE_URL=" + candidate.Source.SourceURL,
		"SERIAL_SYNC_TRACK_ID=" + candidate.Track.ID,
		"SERIAL_SYNC_TRACK_KEY=" + candidate.Track.TrackKey,
		"SERIAL_SYNC_TRACK_NAME=" + candidate.Track.TrackName,
		"SERIAL_SYNC_RELEASE_ID=" + candidate.Release.ID,
		"SERIAL_SYNC_RELEASE_PROVIDER_ID=" + candidate.Release.ProviderReleaseID,
		"SERIAL_SYNC_RELEASE_URL=" + candidate.Release.URL,
		"SERIAL_SYNC_RELEASE_TITLE=" + candidate.Release.Title,
		"SERIAL_SYNC_RELEASE_ROLE=" + string(candidate.Assignment.ReleaseRole),
		"SERIAL_SYNC_ARTIFACT_ID=" + candidate.Artifact.ID,
		"SERIAL_SYNC_ARTIFACT_KIND=" + candidate.Artifact.ArtifactKind,
		"SERIAL_SYNC_ARTIFACT_MIME=" + candidate.Artifact.MIMEType,
		"SERIAL_SYNC_ARTIFACT_FILENAME=" + candidate.Artifact.Filename,
		"SERIAL_SYNC_ARTIFACT_PATH=" + candidate.Artifact.StorageRef,
		"SERIAL_SYNC_METADATA_JSON_PATH=" + candidate.Artifact.MetadataRef,
		"SERIAL_SYNC_NORMALIZED_JSON_PATH=" + candidate.Artifact.NormalizedRef,
		"SERIAL_SYNC_RAW_JSON_PATH=" + candidate.Artifact.RawRef,
	}
}

func combinedExecOutput(stdout, stderr *bytes.Buffer) string {
	parts := []string{}
	if out := strings.TrimSpace(stdout.String()); out != "" {
		parts = append(parts, out)
	}
	if out := strings.TrimSpace(stderr.String()); out != "" {
		parts = append(parts, out)
	}
	return strings.Join(parts, "\n")
}

func formatExecOutput(stdout, stderr *bytes.Buffer) string {
	output := combinedExecOutput(stdout, stderr)
	if output == "" {
		return ""
	}
	return ": " + output
}
