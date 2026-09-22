package publish

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
)

// Target is the seam behind a downstream publisher destination. Two adapters
// exist, filesystem and exec, so the seam is real: the app executor asks a
// Target for identity, delivery checks, publishing and retirement instead of
// branching on the configured kind. The destination path formula, the
// ownership rules and the exec supersede hook live here beside their adapters.
type Target interface {
	TargetID() string
	Kind() string
	// Capabilities states what the adapter can do at delivery time so the
	// planner and executor never infer it from the kind or probe a method.
	Capabilities() Capabilities
	Ref(candidate domain.PublishCandidate) (string, error)
	PublishHashInput(candidate domain.PublishCandidate) (string, error)
	// DesiredKey identifies a new delivery in the planned set; the executor
	// skips retirement of a record whose DesiredKeyForRecord is still desired.
	// Both combine path and artifact identity so that a correction at the same
	// destination is never mistaken for a still-current delivery.
	DesiredKey(candidate domain.PublishCandidate) string
	DesiredKeyForRecord(record domain.PublishRecordBundle) string
	// MatchesDestination reports whether a record's prior delivery occupied
	// the same destination as the candidate (replace instead of add).
	MatchesDestination(record domain.PublishRecordBundle, candidate domain.PublishCandidate) bool
	// Reorder returns the candidates in their delivery order plus the
	// same-destination dependency map. Filesystem delivery orders candidates
	// whose files overwrite each other; the exec hook's destination is the
	// command itself, so it returns the input order with no dependencies.
	Reorder(candidates []domain.PublishCandidate, records []domain.PublishRecordBundle, volumes []domain.VolumeEdition) ([]domain.PublishCandidate, map[string][]string, error)
	// PublishingRecord returns the crash-recoverable "publishing" record the
	// executor must persist before the delivery, or nil when the target's own
	// protocol already tolerates failures. The value also decides whether a
	// failed delivery needs a separate failed-status record: filesystem
	// delivery re-reads the publishing record on retry, while the exec hook
	// records its failure explicitly.
	PublishingRecord(targetID, targetKind string, candidate domain.PublishCandidate, ref, publishHash string) *domain.PublishRecord
	// CheckDestination verifies that a destination is absent or owned,
	// returning the current content hash ("" when absent).
	CheckDestination(ref string, ownedHashes []string) (string, error)
	// AlreadyDelivered verifies that a delivered artifact still matches on disk.
	AlreadyDelivered(ref string, artifactSHA string) (bool, error)
	// Publish delivers the candidate and returns the final published record.
	// In filesystem delivery, ownedHashes guards the destination against
	// overwriting unowned bytes; the exec adapter ignores them.
	Publish(ctx context.Context, runID string, eventScope string, candidate domain.PublishCandidate, ownedHashes []string) (domain.PublishRecord, error)
	// Retire removes or supersedes a previous delivery once its replacement is
	// confirmed. related lists the delivered replacements that cover it and
	// planned all candidates of the current plan; a filesystem adapter uses
	// planned to recognise a replacement that occupies the same path and must
	// not touch the now-replaced bytes. The executor records superseded status
	// itself after Retire returns.
	Retire(ctx context.Context, old domain.PublishRecordBundle, planned, related []domain.PublishCandidate, ownedHashes []string, eventScope string) error
}

// Capabilities describes a target's delivery abilities.
type Capabilities struct {
	// PendingRecord: delivery writes a crash-recoverable "publishing" record
	// before the bytes land; a failed delivery without one records a failed
	// status instead.
	PendingRecord bool
	// Retires: the target can retire or supersede a previous delivery. A
	// target without it keeps prior records published; the planner emits no
	// retirements for it.
	Retires bool
}

// ErrRetirementUnsupported is returned by Retire on a target whose
// Capabilities report no retirement support.
var ErrRetirementUnsupported = errors.New("publish target cannot retire previous deliveries")

func TargetFor(target config.PublisherConfig) (Target, error) {
	switch NormalizedPublisherKind(target.Kind) {
	case "filesystem":
		return &FilesystemTarget{ID: target.ID, Path: target.Path}, nil
	case "exec":
		return &ExecTarget{ProtocolVersion: target.ProtocolVersion, ID: target.ID, Command: target.Command}, nil
	default:
		return nil, fmt.Errorf("unsupported publisher kind %q", target.Kind)
	}
}

func NormalizedPublisherKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "command", "exec":
		return "exec"
	default:
		return strings.ToLower(strings.TrimSpace(kind))
	}
}
