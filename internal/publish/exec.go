package publish

import (
	"bytes"
	"context"
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
	ID      string
	Command []string
	RunID   string
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

func PublishExec(ctx context.Context, target ExecTarget, candidate domain.PublishCandidate) (domain.PublishRecord, error) {
	if len(target.Command) == 0 {
		return domain.PublishRecord{}, fmt.Errorf("exec publisher %q requires a command", target.ID)
	}
	payload, err := json.MarshalIndent(execPayload{
		RunID:      target.RunID,
		TargetID:   target.ID,
		TargetKind: "exec",
		Command:    append([]string(nil), target.Command...),
		Source:     candidate.Source,
		Track:      candidate.Track,
		Release:    candidate.Release,
		Assignment: candidate.Assignment,
		Artifact:   candidate.Artifact,
	}, "", "  ")
	if err != nil {
		return domain.PublishRecord{}, err
	}

	cmd := exec.CommandContext(ctx, target.Command[0], target.Command[1:]...)
	cmd.Env = append(os.Environ(), execEnv(target, candidate)...)
	cmd.Stdin = bytes.NewReader(payload)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return domain.PublishRecord{}, fmt.Errorf("exec publisher %q failed: %w%s", target.ID, err, formatExecOutput(&stdout, &stderr))
	}

	return domain.PublishRecord{
		ID:          "pub_" + uuid.NewString(),
		ArtifactID:  candidate.Artifact.ID,
		TargetID:    target.ID,
		TargetKind:  "exec",
		TargetRef:   ExecTargetRef(target.Command),
		PublishHash: PublishHash(target.ID, candidate.Artifact.SHA256, ExecTargetSignature(target.Command)),
		PublishedAt: time.Now().UTC(),
		Status:      domain.PublishStatusPublished,
		Message:     combinedExecOutput(&stdout, &stderr),
	}, nil
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

func execEnv(target ExecTarget, candidate domain.PublishCandidate) []string {
	return []string{
		"SERIAL_SYNC_RUN_ID=" + target.RunID,
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
