package publish

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
)

// DeliveryIdentity is one candidate's destination as computed by the adapter
// that owns the destination rules: the reference, the durable publish hash,
// the desired-set key, and whether the destination already holds these
// bytes. The planner computes it once per candidate against the snapshot and
// the executor reuses it, so the ref/hash/key formula lives in exactly one
// place per adapter instead of being recomputed at three call sites.
type DeliveryIdentity struct {
	Ref         string
	PublishHash string
	DesiredKey  string
	Intact      bool
}

// Target is the seam behind a downstream publisher destination. Two adapters
// exist, filesystem and exec, so the seam is real: the app executor asks a
// Target for identity, delivery and retirement instead of branching on the
// configured kind. The destination path formula, the ownership rules and the
// exec hook protocol live here beside their adapters.
type Target interface {
	TargetID() string
	Kind() string
	// Capabilities states what the adapter can do at delivery time so the
	// planner and executor never infer it from the kind or a protocol version.
	Capabilities() Capabilities
	// Identity computes the candidate's destination reference, publish hash
	// and desired-set key against the delivered snapshot, and reports whether
	// the destination already holds the artifact's bytes. Bytes at the
	// reference owned by no record in the snapshot are an error, so the
	// planner marks the candidate blocked instead of delivering over them.
	Identity(candidate domain.PublishCandidate, records []domain.PublishRecordBundle) (DeliveryIdentity, error)
	// DesiredKeyForRecord is a delivered record's side of the desired-set
	// key: a record whose key is not in the plan's desired set is retired.
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
	// protocol already tolerates failures.
	PublishingRecord(candidate domain.PublishCandidate, identity DeliveryIdentity) *domain.PublishRecord
	// CheckDestination verifies that a reference is absent or owned by one of
	// ownedHashes, returning the current content hash ("" when absent). The
	// retirement path asks it about a delivered record's reference, which is
	// not a candidate and so cannot go through Identity.
	CheckDestination(ref string, ownedHashes []string) (string, error)
	// Deliver publishes the candidate at its identity and returns the final
	// published record. In filesystem delivery, ownedHashes guard the
	// destination against overwriting unowned bytes; the exec adapter ignores
	// them.
	Deliver(ctx context.Context, runID string, eventScope string, candidate domain.PublishCandidate, identity DeliveryIdentity, ownedHashes []string) (domain.PublishRecord, error)
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
	// retirements for it, and volume or rename output needs it.
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

// ownedHashesAt collects the artifact hashes the snapshot entitles to occupy
// ref: published or publishing records delivered to exactly that reference.
func ownedHashesAt(records []domain.PublishRecordBundle, ref string) []string {
	var hashes []string
	for _, record := range records {
		if record.Record.TargetRef == ref && (record.Record.Status == domain.PublishStatusPublished || record.Record.Status == domain.PublishStatusPublishing) {
			hashes = append(hashes, record.Artifact.SHA256)
		}
	}
	return hashes
}
