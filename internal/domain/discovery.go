package domain

import "time"

type CandidateEvidence struct {
	Kind  string `json:"kind"`
	Value string `json:"value,omitempty"`
}

type DiscoveryCandidate struct {
	MemberFingerprints      map[string]CandidateMemberFingerprint `json:"member_fingerprints,omitempty"`
	UncapturedMembers       []string                              `json:"uncaptured_members,omitempty"`
	ID                      string                                `json:"id"`
	Source                  string                                `json:"source"`
	Kind                    string                                `json:"kind"`
	CorrelationKey          string                                `json:"correlation_key"`
	MemberReleaseIDs        []string                              `json:"member_release_ids"`
	FirstObserved           time.Time                             `json:"first_observed"`
	LastEvidenceChange      time.Time                             `json:"last_evidence_change"`
	EvidenceFingerprint     string                                `json:"evidence_fingerprint"`
	ExtractorVersion        int                                   `json:"extractor_version"`
	Status                  string                                `json:"status"`
	DismissalReason         string                                `json:"dismissal_reason,omitempty"`
	DismissedFingerprint    string                                `json:"dismissed_fingerprint,omitempty"`
	LastReportedFingerprint string                                `json:"last_reported_fingerprint,omitempty"`
	Evidence                []CandidateEvidence                   `json:"evidence"`
	ResolvedMembers         []string                              `json:"resolved_members,omitempty"`
	Changed                 bool                                  `json:"changed"`
}

type CandidateMemberFingerprint struct {
	Capture  string `json:"capture"`
	Evidence string `json:"evidence"`
}
