package publish

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/filehash"
)

type FilesystemTarget struct {
	ID   string
	Path string
}

func (t *FilesystemTarget) TargetID() string {
	return t.ID
}

func (t *FilesystemTarget) Kind() string {
	return "filesystem"
}

func (t *FilesystemTarget) Capabilities() Capabilities {
	return Capabilities{PendingRecord: true, Retires: true}
}

func (t *FilesystemTarget) Identity(candidate domain.PublishCandidate, records []domain.PublishRecordBundle) (DeliveryIdentity, error) {
	ref, err := t.ref(candidate)
	if err != nil {
		return DeliveryIdentity{}, err
	}
	identity := DeliveryIdentity{
		Ref:         ref,
		PublishHash: hashPublish(t.ID, candidate.Artifact.SHA256, ref),
		DesiredKey:  ref + "\x00" + candidate.Artifact.ID + "\x00" + candidate.Artifact.Filename,
	}
	current, err := t.CheckDestination(ref, ownedHashesAt(records, ref))
	if err != nil {
		// The identity is still the candidate's own; only the intactness and
		// the delivery decision change.
		return identity, err
	}
	identity.Intact = current != "" && current == candidate.Artifact.SHA256
	return identity, nil
}

func (t *FilesystemTarget) ref(candidate domain.PublishCandidate) (string, error) {
	return filepath.Join(t.Path, candidate.Source.ID, candidate.Track.TrackKey, candidate.Artifact.Filename), nil
}

func (t *FilesystemTarget) DesiredKeyForRecord(record domain.PublishRecordBundle) string {
	return record.Record.TargetRef + "\x00" + record.Artifact.ID + "\x00" + record.Record.Filename
}

func (t *FilesystemTarget) MatchesDestination(record domain.PublishRecordBundle, candidate domain.PublishCandidate) bool {
	ref, _ := t.ref(candidate)
	return record.Record.TargetRef == ref
}

// Same-destination replacements belong here: a candidate that overwrites a
// delivered record must be ordered after the candidates covering that
// record's members, and a cyclic or non-covering set is rejected outright so
// the executor never delivers it in the wrong order.
var errCyclicReplacement = errors.New("cyclic same-path replacement; choose distinct output names for this regroup")

func IsCyclicReplacement(err error) bool {
	return errors.Is(err, errCyclicReplacement)
}

func (t *FilesystemTarget) Reorder(candidates []domain.PublishCandidate, records []domain.PublishRecordBundle, volumes []domain.VolumeEdition) ([]domain.PublishCandidate, map[string][]string, error) {
	dependencies := map[string][]string{}
	members := map[string][]string{}
	for _, edition := range volumes {
		for _, member := range edition.Members {
			members[edition.Artifact.ID] = append(members[edition.Artifact.ID], member.ReleaseID)
		}
	}
	byRelease := map[string]string{}
	byID := map[string]domain.PublishCandidate{}
	for _, candidate := range candidates {
		byID[candidate.Artifact.ID] = candidate
		if candidate.Volume == nil {
			byRelease[candidate.Release.ID] = candidate.Artifact.ID
		} else {
			for _, member := range candidate.Volume.Members {
				byRelease[member.ReleaseID] = candidate.Artifact.ID
			}
		}
	}
	for _, candidate := range candidates {
		path, _ := t.ref(candidate)
		for _, old := range records {
			if old.Record.Status != domain.PublishStatusPublished || old.Record.TargetRef != path || old.Artifact.ID == candidate.Artifact.ID {
				continue
			}
			for _, releaseID := range members[old.Artifact.ID] {
				replacementID := byRelease[releaseID]
				if replacementID == "" {
					return nil, nil, fmt.Errorf("target %s: replacement for %s does not cover chapter %s", t.ID, path, releaseID)
				}
				if replacementID != candidate.Artifact.ID {
					dependencies[candidate.Artifact.ID] = append(dependencies[candidate.Artifact.ID], replacementID)
				}
			}
		}
	}
	var ordered []domain.PublishCandidate
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visited[id] {
			return nil
		}
		if visiting[id] {
			return fmt.Errorf("target %s: %w", t.ID, errCyclicReplacement)
		}
		visiting[id] = true
		sort.Strings(dependencies[id])
		for _, dependency := range dependencies[id] {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		visited[id], visiting[id] = true, false
		ordered = append(ordered, byID[id])
		return nil
	}
	for _, candidate := range candidates {
		if err := visit(candidate.Artifact.ID); err != nil {
			return nil, nil, err
		}
	}
	return ordered, dependencies, nil
}

func (t *FilesystemTarget) PublishingRecord(candidate domain.PublishCandidate, identity DeliveryIdentity) *domain.PublishRecord {
	// A "publishing" record is the crash-recovery ledger entry: written before
	// the file lands, so a crash between the two leaves a record whose status
	// is neither published nor failed and the retry re-delivers.
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

func (t *FilesystemTarget) CheckDestination(ref string, ownedHashes []string) (string, error) {
	return CheckFilesystemDestination(ref, ownedHashes)
}

func (t *FilesystemTarget) Deliver(ctx context.Context, runID, eventScope string, candidate domain.PublishCandidate, identity DeliveryIdentity, ownedHashes []string) (domain.PublishRecord, error) {
	select {
	case <-ctx.Done():
		return domain.PublishRecord{}, ctx.Err()
	default:
	}
	if err := os.MkdirAll(filepath.Dir(identity.Ref), 0o755); err != nil {
		return domain.PublishRecord{}, err
	}
	// Re-check ownership at delivery time: the plan's identity was computed
	// against the snapshot, and a concurrent writer could have landed bytes
	// since.
	current, err := t.CheckDestination(identity.Ref, ownedHashes)
	if err != nil {
		return domain.PublishRecord{}, err
	}
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

func (t *FilesystemTarget) Retire(ctx context.Context, old domain.PublishRecordBundle, planned, related []domain.PublishCandidate, ownedHashes []string, eventScope string) error {
	for _, candidate := range planned {
		ref, _ := t.ref(candidate)
		if ref == old.Record.TargetRef {
			// A planned replacement occupies this path; its bytes supersede the
			// old delivery in place, so there is nothing to remove.
			return nil
		}
	}
	current, err := t.CheckDestination(old.Record.TargetRef, ownedHashes)
	if err != nil {
		return fmt.Errorf("retirement ownership conflict: %s: %w", old.Record.TargetRef, err)
	}
	if current == "" {
		return nil
	}
	return os.Remove(old.Record.TargetRef)
}

func PublishHash(targetID, artifactSHA, targetPath string) string {
	return hashPublish(targetID, artifactSHA, targetPath)
}

func CheckFilesystemDestination(path string, ownedHashes []string) (string, error) {
	current, err := filehash.Hash(path)
	if err != nil {
		return "", err
	}
	if current != "" && !slices.Contains(ownedHashes, current) {
		return "", fmt.Errorf("destination ownership conflict: %s", path)
	}
	return current, nil
}

func hashPublish(targetID, artifactSHA, targetPath string) string {
	sum := sha256.Sum256([]byte(targetID + "\x00" + artifactSHA + "\x00" + targetPath))
	return hex.EncodeToString(sum[:])
}

func copyFile(src, dst, expectedHash string) error {
	from, err := os.Open(src)
	if err != nil {
		return err
	}
	defer from.Close()
	to, err := os.CreateTemp(filepath.Dir(dst), ".serial-sync-*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = to.Close(); _ = os.Remove(to.Name()) }()
	if _, err := io.Copy(to, from); err != nil {
		return err
	}
	if err := to.Sync(); err != nil {
		return err
	}
	if err := to.Close(); err != nil {
		return err
	}
	actual, err := filehash.Hash(to.Name())
	if err != nil {
		return err
	}
	if actual != expectedHash {
		return fmt.Errorf("artifact hash mismatch for %s", src)
	}
	if err := os.Chmod(to.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(to.Name(), dst)
}
