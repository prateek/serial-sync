package publish

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/prateek/serial-sync/internal/domain"
)

// DropTarget hands finished files to a library manager that watches Path.
// The manager imports each file, then renames it and rewrites its metadata,
// so neither the path nor the bytes identify a delivery afterwards. The
// catalog's published record is the only proof of hand-off: it is keyed by
// the source and provider release ID, and the file is never touched again.
type DropTarget struct {
	ID   string
	Path string
}

func (t *DropTarget) TargetID() string {
	return t.ID
}

func (t *DropTarget) Kind() string {
	return "drop"
}

func (t *DropTarget) Capabilities() Capabilities {
	return Capabilities{PendingRecord: true, HandsOff: true}
}

func (t *DropTarget) Identity(candidate domain.PublishCandidate, records []domain.PublishRecordBundle) (DeliveryIdentity, error) {
	if candidate.Volume != nil {
		return DeliveryIdentity{}, fmt.Errorf("drop publisher %q cannot deliver volume output", t.ID)
	}
	ref := t.ref(candidate)
	key := dropKey(candidate.Source.ID, candidate.Release.ProviderReleaseID)
	identity := DeliveryIdentity{
		Ref:         ref,
		PublishHash: hashPublish(t.ID, candidate.Artifact.SHA256, key),
		DesiredKey:  key,
		Intact:      true,
	}
	for _, record := range records {
		if record.Record.Status != domain.PublishStatusPublished || dropKey(record.Source.ID, record.Release.ProviderReleaseID) != key {
			continue
		}
		if candidate.Artifact.SHA256 != "" && record.Artifact.SHA256 == candidate.Artifact.SHA256 {
			identity.Handoff = HandoffSame
			return identity, nil
		}
		identity.Handoff = HandoffRevised
	}
	return identity, nil
}

func (t *DropTarget) ref(candidate domain.PublishCandidate) string {
	return filepath.Join(t.Path, candidate.Source.ID, candidate.Track.TrackKey, candidate.Artifact.Filename)
}

func dropKey(sourceID, providerReleaseID string) string {
	return sourceID + "\x00" + providerReleaseID
}

func (t *DropTarget) DesiredKeyForRecord(record domain.PublishRecordBundle) string {
	return dropKey(record.Source.ID, record.Release.ProviderReleaseID)
}

func (t *DropTarget) MatchesDestination(domain.PublishRecordBundle, domain.PublishCandidate) bool {
	return false
}

func (t *DropTarget) Reorder(candidates []domain.PublishCandidate, _ []domain.PublishRecordBundle, _ []domain.VolumeEdition) ([]domain.PublishCandidate, map[string][]string, error) {
	return candidates, map[string][]string{}, nil
}

func (t *DropTarget) PublishingRecord(candidate domain.PublishCandidate, identity DeliveryIdentity) *domain.PublishRecord {
	return &domain.PublishRecord{
		ID:          "pub_" + uuid.NewString(),
		ArtifactID:  candidate.Artifact.ID,
		TargetID:    t.ID,
		TargetKind:  t.Kind(),
		TargetRef:   identity.Ref,
		PublishHash: identity.PublishHash,
		PublishedAt: time.Now().UTC(),
		Status:      domain.PublishStatusPublishing,
	}
}

func (t *DropTarget) CheckDestination(ref string, ownedHashes []string) (string, error) {
	return CheckFilesystemDestination(ref, ownedHashes)
}

func (t *DropTarget) Deliver(ctx context.Context, _, _ string, candidate domain.PublishCandidate, identity DeliveryIdentity, ownedHashes []string) (domain.PublishRecord, error) {
	select {
	case <-ctx.Done():
		return domain.PublishRecord{}, ctx.Err()
	default:
	}
	// A missing root usually means the library volume is not mounted; creating
	// it would hide the files from the library manager.
	if info, err := os.Stat(t.Path); err != nil || !info.IsDir() {
		return domain.PublishRecord{}, fmt.Errorf("drop publisher %q root %s is not a directory", t.ID, t.Path)
	}
	if err := os.MkdirAll(filepath.Dir(identity.Ref), 0o755); err != nil {
		return domain.PublishRecord{}, err
	}
	current, err := t.CheckDestination(identity.Ref, ownedHashes)
	if err != nil {
		return domain.PublishRecord{}, err
	}
	// copyFile stages under a dot-prefixed name, which library watchers skip,
	// and renames into place only after the bytes are synced and verified.
	if current != candidate.Artifact.SHA256 {
		if err := copyFile(candidate.Artifact.StorageRef, identity.Ref, candidate.Artifact.SHA256); err != nil {
			return domain.PublishRecord{}, err
		}
	}
	return domain.PublishRecord{
		Filename:    candidate.Artifact.Filename,
		ID:          "pub_" + uuid.NewString(),
		ArtifactID:  candidate.Artifact.ID,
		TargetID:    t.ID,
		TargetKind:  t.Kind(),
		TargetRef:   identity.Ref,
		PublishHash: identity.PublishHash,
		PublishedAt: time.Now().UTC(),
		Status:      domain.PublishStatusPublished,
	}, nil
}

func (t *DropTarget) Retire(context.Context, domain.PublishRecordBundle, []domain.PublishCandidate, []domain.PublishCandidate, []string, string) error {
	return ErrRetirementUnsupported
}
