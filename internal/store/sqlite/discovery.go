package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/prateek/serial-sync/internal/domain"
	sqldb "github.com/prateek/serial-sync/internal/store/sqlite/db"
)

func (s *Store) ListDiscoveryCandidates(ctx context.Context, sourceID string) ([]domain.DiscoveryCandidate, error) {
	if !s.hasDiscoverySchema {
		return nil, nil
	}
	rows, err := s.queries.ListDiscoveryCandidates(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	candidates := make([]domain.DiscoveryCandidate, 0, len(rows))
	for _, row := range rows {
		candidate := domain.DiscoveryCandidate{ID: row.ID, Source: row.SourceID, Kind: row.Kind, CorrelationKey: row.CorrelationKey, FirstObserved: parseTime(row.FirstObserved), LastEvidenceChange: parseTime(row.LastEvidenceChange), EvidenceFingerprint: row.EvidenceFingerprint, ExtractorVersion: int(row.ExtractorVersion), Status: row.Status, DismissalReason: row.DismissalReason, DismissedFingerprint: row.DismissedFingerprint, LastReportedFingerprint: row.LastReportedFingerprint}
		if err := json.Unmarshal([]byte(row.MemberReleaseIds), &candidate.MemberReleaseIDs); err != nil {
			return nil, fmt.Errorf("candidate %s members: %w", row.ID, err)
		}
		if err := json.Unmarshal([]byte(row.MemberFingerprints), &candidate.MemberFingerprints); err != nil {
			return nil, fmt.Errorf("candidate %s member fingerprints: %w", row.ID, err)
		}
		if err := json.Unmarshal([]byte(row.Evidence), &candidate.Evidence); err != nil {
			return nil, fmt.Errorf("candidate %s evidence: %w", row.ID, err)
		}
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

func (s *Store) SaveDiscoveryCandidates(ctx context.Context, candidates []domain.DiscoveryCandidate) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	queries := s.queries.WithTx(tx)
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if seen[candidate.ID] {
			return fmt.Errorf("duplicate discovery candidate identity %s", candidate.ID)
		}
		seen[candidate.ID] = true
		fingerprints, err := json.Marshal(candidate.MemberFingerprints)
		if err != nil {
			return err
		}
		members, err := json.Marshal(candidate.MemberReleaseIDs)
		if err != nil {
			return err
		}
		evidence, err := json.Marshal(candidate.Evidence)
		if err != nil {
			return err
		}
		err = queries.UpsertDiscoveryCandidate(ctx, sqldb.UpsertDiscoveryCandidateParams{ID: candidate.ID, SourceID: candidate.Source, Kind: candidate.Kind, CorrelationKey: candidate.CorrelationKey, MemberReleaseIds: string(members), MemberFingerprints: string(fingerprints), FirstObserved: formatTime(candidate.FirstObserved), LastEvidenceChange: formatTime(candidate.LastEvidenceChange), EvidenceFingerprint: candidate.EvidenceFingerprint, ExtractorVersion: int64(candidate.ExtractorVersion), Status: candidate.Status, DismissalReason: candidate.DismissalReason, DismissedFingerprint: candidate.DismissedFingerprint, LastReportedFingerprint: candidate.LastReportedFingerprint, Evidence: string(evidence)})
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) DismissDiscoveryCandidate(ctx context.Context, id, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return fmt.Errorf("candidate dismissal requires --reason")
	}
	count, err := s.queries.DismissDiscoveryCandidate(ctx, sqldb.DismissDiscoveryCandidateParams{ID: id, DismissalReason: reason})
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("candidate %q does not exist; list setup candidates first", id)
	}
	return nil
}
