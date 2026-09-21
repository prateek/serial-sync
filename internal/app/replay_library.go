package app

import (
	"context"
	"fmt"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
)

type AppliedLibraryComparison struct {
	Status    string              `json:"status"`
	Scope     []string            `json:"sources,omitempty"`
	Notice    string              `json:"notice"`
	Baseline  *AppliedLibraryPlan `json:"baseline,omitempty"`
	Candidate *AppliedLibraryPlan `json:"candidate,omitempty"`
}

type AppliedLibraryPlan struct {
	Actions []domain.PublishItemResult `json:"actions"`
	Volumes []domain.VolumePlan        `json:"volumes,omitempty"`
	Blocked []string                   `json:"blocked,omitempty"`
	Notices []string                   `json:"notices,omitempty"`
	Pending []deliveryPlan             `json:"pending_deliveries,omitempty"`
}

func (s *Service) compareAppliedLibrary(ctx context.Context, before, after *config.Config, corpus []replaySource) AppliedLibraryComparison {
	result := AppliedLibraryComparison{Status: "unavailable", Notice: "Workspace replay has no applied catalog; use --stored --compare to plan library changes."}
	if s.Repo == nil {
		return result
	}
	result.Status = "evaluated"
	result.Notice = "Both configs are planned against the current config's read-only catalog and mounted destinations. Actions require run --rebuild after promotion. Disabled or removed sources and targets retain delivered files. Plans do not execute conversion or publisher hooks."
	for _, item := range corpus {
		result.Scope = append(result.Scope, item.Creator.SourceID)
	}
	result.Baseline = s.appliedLibraryPlan(ctx, before, result.Scope)
	result.Candidate = s.appliedLibraryPlan(ctx, after, result.Scope)
	if len(result.Baseline.Blocked)+len(result.Candidate.Blocked) > 0 {
		result.Status = "blocked"
	}
	return result
}

func (s *Service) appliedLibraryPlan(ctx context.Context, cfg *config.Config, scope []string) *AppliedLibraryPlan {
	result := &AppliedLibraryPlan{Actions: []domain.PublishItemResult{}}
	scoped := *cfg
	scoped.Sources = append([]config.SourceConfig(nil), cfg.Sources...)
	included := map[string]bool{}
	for _, source := range scope {
		included[source] = true
	}
	for i := range scoped.Sources {
		scoped.Sources[i].Enabled = scoped.Sources[i].Enabled && included[scoped.Sources[i].ID]
	}
	if len(selectSources(scoped.Sources, "")) == 0 || len(selectPublishers(scoped.Publishers, "")) == 0 {
		result.Notices = []string{"No enabled sources or destinations in this scope; existing delivered files are retained."}
		return result
	}
	planner := *s
	planner.Config = &scoped
	plan, err := planner.Rebuild(ctx, RebuildOptions{DryRun: true}, "setup preview compare")
	result.Blocked, result.Notices, result.Pending = plan.Blocked, plan.Notices, plan.Pending
	if err != nil && len(result.Blocked) == 0 {
		result.Blocked = []string{err.Error()}
	}
	result.Volumes = plan.Publish.Volumes
	for _, item := range plan.Publish.Items {
		if item.Action != "unchanged" {
			result.Actions = append(result.Actions, item)
		}
	}
	return result
}

func formatAppliedLibrary(comparison AppliedLibraryComparison) []string {
	lines := []string{"applied library: " + comparison.Status + "; " + comparison.Notice}
	for _, entry := range []struct {
		name string
		plan *AppliedLibraryPlan
	}{{"baseline", comparison.Baseline}, {"candidate", comparison.Candidate}} {
		if entry.plan == nil {
			continue
		}
		lines = append(lines, fmt.Sprintf("  %s: %d action(s), %d blocked", entry.name, len(entry.plan.Actions), len(entry.plan.Blocked)))
		for _, item := range entry.plan.Actions {
			lines = append(lines, fmt.Sprintf("    %s %s: %s", item.Action, item.TargetID, item.TargetRef))
		}
		for _, blocked := range entry.plan.Blocked {
			lines = append(lines, "    blocked: "+blocked)
		}
		for _, notice := range entry.plan.Notices {
			lines = append(lines, "    "+notice)
		}
	}
	return lines
}
