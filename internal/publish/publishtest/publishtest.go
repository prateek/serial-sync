// Package publishtest provides an in-memory publish.Target for testing the
// delivery executor without a filesystem or a hook process. It is the third
// adapter at the target seam, so the seam is exercised by construction and
// tests can script delivery outcomes directly.
package publishtest

import (
	"context"
	"sync"

	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/publish"
)

// Target records every identity, delivery and retirement it is asked to
// make. Zero fields behave like a filesystem target (pending records,
// retirement supported); override them to script the executor's paths.
type Target struct {
	ID string

	// Capabilities is what the executor is told the target can do.
	Caps publish.Capabilities
	// IdentityErr makes Identity fail for the named artifact, driving the
	// plan-time blocked path.
	IdentityErr map[string]error
	// DeliverErr makes Deliver fail for the named artifact.
	DeliverErr map[string]error
	// RetireErr makes Retire fail for the named record.
	RetireErr map[string]error

	mu          sync.Mutex
	Deliveries  []domain.PublishCandidate
	Retirements []string
}

var _ publish.Target = (*Target)(nil)

func (t *Target) TargetID() string {
	if t.ID == "" {
		return "memory"
	}
	return t.ID
}

func (t *Target) Kind() string { return "memory" }

func (t *Target) Capabilities() publish.Capabilities {
	return t.Caps
}

func (t *Target) Identity(candidate domain.PublishCandidate, _ []domain.PublishRecordBundle) (publish.DeliveryIdentity, error) {
	if err := t.IdentityErr[candidate.Artifact.ID]; err != nil {
		return publish.DeliveryIdentity{Ref: "memory://" + candidate.Artifact.Filename}, err
	}
	return publish.DeliveryIdentity{
		Ref:         "memory://" + candidate.Artifact.Filename,
		PublishHash: publish.PublishHash(t.TargetID(), candidate.Artifact.SHA256, "memory://"+candidate.Artifact.Filename),
		DesiredKey:  candidate.Artifact.ID + "\x00" + candidate.Artifact.Filename,
		Intact:      true,
	}, nil
}

func (t *Target) DesiredKeyForRecord(record domain.PublishRecordBundle) string {
	return record.Artifact.ID + "\x00" + record.Record.Filename
}

func (t *Target) MatchesDestination(record domain.PublishRecordBundle, candidate domain.PublishCandidate) bool {
	return record.Record.Filename == candidate.Artifact.Filename
}

func (t *Target) Reorder(candidates []domain.PublishCandidate, _ []domain.PublishRecordBundle, _ []domain.VolumeEdition) ([]domain.PublishCandidate, map[string][]string, error) {
	return candidates, map[string][]string{}, nil
}

func (t *Target) PublishingRecord(candidate domain.PublishCandidate, identity publish.DeliveryIdentity) *domain.PublishRecord {
	if !t.Caps.PendingRecord {
		return nil
	}
	return &domain.PublishRecord{
		ID:          "pub_pending_" + candidate.Artifact.ID,
		ArtifactID:  candidate.Artifact.ID,
		TargetID:    t.TargetID(),
		TargetKind:  t.Kind(),
		TargetRef:   identity.Ref,
		PublishHash: identity.PublishHash,
		Status:      domain.PublishStatusPublishing,
	}
}

func (t *Target) CheckDestination(string, []string) (string, error) {
	return "", nil
}

func (t *Target) Deliver(_ context.Context, runID, _ string, candidate domain.PublishCandidate, identity publish.DeliveryIdentity, _ []string) (domain.PublishRecord, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.DeliverErr[candidate.Artifact.ID]; err != nil {
		return domain.PublishRecord{}, err
	}
	t.Deliveries = append(t.Deliveries, candidate)
	return domain.PublishRecord{
		ID:          "pub_" + candidate.Artifact.ID,
		ArtifactID:  candidate.Artifact.ID,
		Filename:    candidate.Artifact.Filename,
		TargetID:    t.TargetID(),
		TargetKind:  t.Kind(),
		TargetRef:   identity.Ref,
		PublishHash: identity.PublishHash,
		Status:      domain.PublishStatusPublished,
		Message:     "run " + runID,
	}, nil
}

func (t *Target) Retire(_ context.Context, old domain.PublishRecordBundle, _, _ []domain.PublishCandidate, _ []string, _ string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.RetireErr[old.Record.ID]; err != nil {
		return err
	}
	t.Retirements = append(t.Retirements, old.Record.ID)
	return nil
}
