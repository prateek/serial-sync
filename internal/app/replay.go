package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/prateek/serial-sync/internal/artifact"
	"github.com/prateek/serial-sync/internal/classify"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/discovery"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/rulepreview"
)

type replaySource struct {
	Creator  SourceDumpCreator
	Releases []domain.NormalizedRelease
}

type ReplayBinding struct {
	SoftwareHash      string         `json:"software_hash"`
	ConfigHash        string         `json:"config_hash"`
	CompareConfigHash string         `json:"compare_config_hash,omitempty"`
	CaptureHash       string         `json:"capture_hash"`
	SoftwareVersion   string         `json:"software_version"`
	NormalizerVersion int            `json:"normalizer_version"`
	Inventory         []CaptureEntry `json:"inventory"`
}

type CaptureEntry struct {
	Source    string `json:"source"`
	ReleaseID string `json:"release_id"`
	Hash      string `json:"hash"`
}

type ClassificationState struct {
	Series          string                  `json:"series"`
	Role            domain.ReleaseRole      `json:"role"`
	Input           string                  `json:"input"`
	Selectors       []domain.SelectorMatch  `json:"selectors,omitempty"`
	Guards          []domain.GuardResult    `json:"guards,omitempty"`
	SelectedContent domain.ContentReference `json:"selected_content"`
	Eligibility     string                  `json:"eligibility"`
}

type OutputPolicyState struct {
	SourcePresent bool                      `json:"source_present"`
	SourceEnabled bool                      `json:"source_enabled"`
	Format        domain.OutputFormat       `json:"format"`
	Preface       domain.PrefaceMode        `json:"preface"`
	Author        string                    `json:"author"`
	SeriesTitle   string                    `json:"series_title"`
	Book          string                    `json:"book,omitempty"`
	Sequence      *domain.Sequence          `json:"sequence,omitempty"`
	Output        config.SeriesOutputConfig `json:"output"`
	Books         []config.BookConfig       `json:"books,omitempty"`
	Destinations  []ReplayDestination       `json:"destinations"`
}

type ReplayDestination struct {
	ID              string `json:"id"`
	Kind            string `json:"kind"`
	Path            string `json:"path,omitempty"`
	CommandHash     string `json:"command_hash,omitempty"`
	ProtocolVersion int    `json:"protocol_version,omitempty"`
}

type ReplayChange[T any] struct {
	Source    string `json:"source"`
	ReleaseID string `json:"release_id"`
	Before    T      `json:"before"`
	After     T      `json:"after"`
}

type ReplayComparison struct {
	Classification  []ReplayChange[ClassificationState] `json:"classification"`
	OutputPolicy    []ReplayChange[OutputPolicyState]   `json:"output_policy"`
	AppliedLibrary  AppliedLibraryComparison            `json:"applied_library"`
	RequiresRebuild bool                                `json:"requires_rebuild"`
}

func (s *Service) previewCorpus(ctx context.Context, options RulesPreviewOptions, other *config.Config) ([]replaySource, *config.Config, string, error) {
	if options.Stored {
		if options.WorkspacePath != "" || options.SeriesFile != "" {
			return nil, nil, "", fmt.Errorf("--stored cannot be combined with --workspace or --series-file")
		}
		if s.Repo == nil {
			return nil, nil, "", fmt.Errorf("stored preview requires an existing read-only catalog")
		}
		sourceIDs := map[string]bool{}
		for _, cfg := range []*config.Config{s.Config, other} {
			if cfg != nil {
				for _, source := range cfg.Sources {
					sourceIDs[source.ID] = true
				}
			}
		}
		ids := make([]string, 0, len(sourceIDs))
		for id := range sourceIDs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		var corpus []replaySource
		for _, id := range ids {
			creator := SourceDumpCreator{SourceID: id}
			source, err := s.Repo.GetSource(ctx, id)
			if err != nil {
				return nil, nil, "", err
			}
			if source != nil {
				creator.CreatorName = source.CreatorName
			}
			if !matchesDumpCreatorFilters(creator, options.CreatorFilters) {
				continue
			}
			releases, err := s.storedReleases(ctx, id)
			if err != nil {
				return nil, nil, "", err
			}
			item := replaySource{Creator: creator, Releases: releases}
			corpus = append(corpus, item)
		}
		return corpus, s.Config, "", nil
	}
	root, err := resolveWorkspacePath(options.WorkspacePath)
	if err != nil {
		return nil, nil, "", err
	}
	manifest, err := loadDumpManifest(filepath.Join(root, "manifest.json"))
	if err != nil {
		return nil, nil, "", err
	}
	sources, err := config.LoadSources(manifest.SourcesFile)
	if err != nil {
		return nil, nil, "", err
	}
	cfg := s.Config
	if options.SeriesFile != "" {
		path := options.SeriesFile
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		series, err := config.LoadSeriesWithDefaults(path, sources, s.Config.Defaults)
		if err != nil {
			return nil, nil, "", err
		}
		copy := *cfg
		copy.Sources = sources
		copy.Series = series
		copy.Rules = nil
		copy.Review = nil
		copy.Overrides = nil
		cfg = &copy
	}
	var corpus []replaySource
	for _, creator := range manifest.Creators {
		if !matchesDumpCreatorFilters(creator, options.CreatorFilters) {
			continue
		}
		releases, err := loadWorkspacePosts(root, manifest.originalRoot, creator.PostsFile)
		if err != nil {
			return nil, nil, "", err
		}
		if creator.RawPostsDir != "" {
			for i := range releases {
				if releases[i].Enrichment != nil && releases[i].Enrichment.NormalizerVersion >= domain.NormalizerVersion {
					continue
				}
				raw, err := os.ReadFile(filepath.Join(creator.RawPostsDir, releases[i].ProviderReleaseID+".json"))
				if os.IsNotExist(err) {
					continue
				}
				if err != nil {
					return nil, nil, "", err
				}
				releases[i], err = s.Providers.EnrichCaptured(releases[i], raw)
				if err != nil {
					return nil, nil, "", err
				}
			}
		}
		corpus = append(corpus, replaySource{Creator: creator, Releases: releases})
	}
	return corpus, cfg, root, nil
}

func replayStates(cfg *config.Config, source string, release domain.NormalizedRelease, explained classify.ExplainedDecision) (ClassificationState, OutputPolicyState) {
	d := explained.Decision
	src, present := cfg.SourceByID(source)
	classification := ClassificationState{Series: d.SeriesID, Role: d.ReleaseRole, Input: d.RuleID, Eligibility: rulepreview.Eligibility(release, d)}
	for _, attempt := range explained.Explanation.Attempts {
		if attempt.Selected {
			classification.Selectors = attempt.Selectors
			classification.Guards = attempt.Guards
		}
	}
	if classification.Eligibility == "ready" && (!present || !src.Enabled) {
		classification.Eligibility = "source_disabled"
	}
	classification.SelectedContent = classify.SelectContent(release, d)
	policy := OutputPolicyState{SourcePresent: present, SourceEnabled: src.Enabled, Format: d.OutputFormat, Preface: d.PrefaceMode, Author: d.CanonicalAuthor, SeriesTitle: d.TrackName, Book: d.BookID, Sequence: d.Sequence}
	for _, series := range cfg.Series {
		if series.ID == d.SeriesID {
			policy.Output = cfg.SeriesOutput(series)
			policy.Output.Format = string(d.OutputFormat)
			policy.Output.PrefaceMode = string(d.PrefaceMode)
			policy.Books = series.Books
			break
		}
	}
	for _, publisher := range cfg.Publishers {
		if !publisher.Enabled {
			continue
		}
		destination := ReplayDestination{ID: publisher.ID, Kind: publisher.Kind, Path: publisher.Path, ProtocolVersion: publisher.ProtocolVersion}
		if len(publisher.Command) > 0 {
			data, _ := json.Marshal(publisher.Command)
			destination.CommandHash = hashBytes(data)
		}
		policy.Destinations = append(policy.Destinations, destination)
	}
	sort.Slice(policy.Destinations, func(i, j int) bool { return policy.Destinations[i].ID < policy.Destinations[j].ID })
	return classification, policy
}

func (s *Service) replay(ctx context.Context, options RulesPreviewOptions) (RulesPreviewResult, error) {
	result := RulesPreviewResult{}
	var other *config.Config
	if options.CompareConfig != "" {
		if options.SeriesFile != "" {
			return result, fmt.Errorf("--compare uses complete configs; omit --series-file")
		}
		var err error
		other, _, err = config.Load(options.CompareConfig)
		if err != nil {
			return result, err
		}
		result.Comparison = &ReplayComparison{Classification: []ReplayChange[ClassificationState]{}, OutputPolicy: []ReplayChange[OutputPolicyState]{}}
	}
	corpus, cfg, root, err := s.previewCorpus(ctx, options, other)
	if err != nil {
		return result, err
	}
	if len(corpus) == 0 {
		return result, fmt.Errorf("no captured sources match the provided filters")
	}
	if options.SeriesFile != "" {
		seriesPath := options.SeriesFile
		if !filepath.IsAbs(seriesPath) {
			seriesPath = filepath.Join(root, seriesPath)
		}
		data, err := os.ReadFile(seriesPath)
		if err != nil {
			return result, err
		}
		result.SeriesFileHash = hashBytes(data)
		manifest, err := loadDumpManifest(filepath.Join(root, "manifest.json"))
		if err != nil {
			return result, err
		}
		sourcesData, err := os.ReadFile(manifest.SourcesFile)
		if err != nil {
			return result, err
		}
		result.SourcesFileHash = hashBytes(sourcesData)
	}
	result.WorkspacePath = root
	result.SeriesFile = options.SeriesFile
	result.Binding = ReplayBinding{ConfigHash: cfg.Fingerprint(), NormalizerVersion: domain.NormalizerVersion, SoftwareVersion: softwareVersion()}
	if executable, err := os.Executable(); err == nil {
		if binary, err := os.ReadFile(executable); err == nil {
			result.Binding.SoftwareHash = hashBytes(binary)
		}
	}
	if other != nil {
		result.Binding.CompareConfigHash = other.Fingerprint()
	}
	var labelHistory []domain.LabelReference
	if s.Repo != nil {
		labelHistory, err = s.Repo.ListLabelObservations(ctx)
		if err != nil {
			return result, err
		}
	}
	var candidates []domain.PublishCandidate
	inputs := map[string]domain.NormalizedRelease{}
	histories := map[string]map[string]classify.ExplainedDecision{}
	for _, item := range corpus {
		source := item.Creator.SourceID
		histories[source] = map[string]classify.ExplainedDecision{}
		sort.SliceStable(item.Releases, func(i, j int) bool {
			a, b := item.Releases[i], item.Releases[j]
			if a.PublishedAt.Equal(b.PublishedAt) {
				return a.ProviderReleaseID < b.ProviderReleaseID
			}
			return a.PublishedAt.Before(b.PublishedAt)
		})
		var previous []domain.DiscoveryCandidate
		if s.Repo != nil {
			previous, err = s.Repo.ListDiscoveryCandidates(ctx, source)
			if err != nil {
				return result, err
			}
		}
		// Replay drives the same decider the sync uses; its corpus is dump
		// files rather than the store, so the releases and previous candidates
		// come from the dump workspace and the configured repositories.
		decided, decidedCandidates := decideReleases(source, item.Releases, nil, cfg, previous, time.Now().UTC())
		histories[source] = decided
		result.Candidates = append(result.Candidates, decidedCandidates...)
		result.Labels = append(result.Labels, discovery.Labels(source, item.Releases, cfg.Compiled().ForSource(source), decided, labelHistory))
		var baselineDecisions map[string]classify.ExplainedDecision
		if other != nil {
			baselineDecisions, _ = decideReleases(source, item.Releases, nil, other, nil, time.Time{})
		}
		decisions := make([]classify.ExplainedDecision, len(item.Releases))
		for i, release := range item.Releases {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			decisions[i] = decided[release.ProviderReleaseID]
			decisions[i].Decision.Publication, err = publicationMetadataFor(cfg, source, release, decisions[i].Decision)
			if err != nil {
				return result, err
			}
			capture := hashableNormalizedRelease(release)
			capture.Enrichment = release.Enrichment
			content, _ := json.Marshal(capture)
			result.Binding.Inventory = append(result.Binding.Inventory, CaptureEntry{Source: source, ReleaseID: release.ProviderReleaseID, Hash: hashBytes(content)})
			if other != nil {
				beforeClass, beforePolicy := replayStates(other, source, release, baselineDecisions[release.ProviderReleaseID])
				afterClass, afterPolicy := replayStates(cfg, source, release, decisions[i])
				if afterPolicy.SourcePresent && afterPolicy.SourceEnabled && len(selectPublishers(cfg.Publishers, "")) > 0 && (!reflect.DeepEqual(beforeClass, afterClass) || !reflect.DeepEqual(beforePolicy, afterPolicy)) {
					result.Comparison.RequiresRebuild = true
				}
				if !reflect.DeepEqual(beforeClass, afterClass) {
					result.Comparison.Classification = append(result.Comparison.Classification, ReplayChange[ClassificationState]{source, release.ProviderReleaseID, beforeClass, afterClass})
				}
				if !reflect.DeepEqual(beforePolicy, afterPolicy) {
					result.Comparison.OutputPolicy = append(result.Comparison.OutputPolicy, ReplayChange[OutputPolicyState]{source, release.ProviderReleaseID, beforePolicy, afterPolicy})
				}
			}
		}
		preview := rulepreview.BuildDecisions(item.Releases, decisions, options.ShowPosts)
		for i, release := range item.Releases {
			decision := decisions[i].Decision
			track := domain.StoryTrack{ID: source + "/" + decision.TrackKey, TrackKey: decision.TrackKey, TrackName: decision.TrackName, CanonicalAuthor: decision.CanonicalAuthor}
			stored := domain.Release{ID: source + "/" + release.ProviderReleaseID, ProviderReleaseID: release.ProviderReleaseID, PublishedAt: release.PublishedAt, Title: release.Title}
			description := artifact.Describe(track, stored, release, decision)
			filename := artifact.PreviewFilenameFor(track, stored, release, decision, description)
			classification, _ := replayStates(cfg, source, release, decisions[i])
			if options.ShowPosts {
				preview.Posts[i].Sequence = decision.Sequence
				preview.Posts[i].Filename = filename
				preview.Posts[i].Eligibility = classification.Eligibility
			}
			if classify.CanMaterialize(release, decision) {
				candidates = append(candidates, domain.PublishCandidate{Source: domain.Source{ID: source}, Track: track, Release: stored, Assignment: domain.ReleaseAssignment{ReleaseRole: decision.ReleaseRole}, Artifact: domain.Artifact{Filename: filename, MIMEType: description.MIMEType}})
				inputs[stored.ID] = release
			}
		}
		result.TotalPosts += len(item.Releases)
		result.Materializable += preview.Materializable
		result.FallbackPosts += preview.FallbackPosts
		result.Creators = append(result.Creators, RulesPreviewCreator{SourceID: source, CreatorName: item.Creator.CreatorName, CreatorHandle: item.Creator.CreatorHandle, MembershipKind: item.Creator.MembershipKind, PostCount: len(item.Releases), Preview: preview})
	}
	if options.Suggest {
		bySource := map[string][]domain.NormalizedRelease{}
		for _, item := range corpus {
			bySource[item.Creator.SourceID] = item.Releases
		}
		result.Suggestions = discovery.Suggestions(result.Candidates, bySource)
	}
	inventory, _ := json.Marshal(result.Binding.Inventory)
	result.Binding.CaptureHash = hashBytes(inventory)
	if result.Comparison != nil {
		result.Comparison.AppliedLibrary = s.compareAppliedLibrary(ctx, other, cfg, corpus)
		if plan := result.Comparison.AppliedLibrary.Candidate; plan != nil && len(plan.Actions) > 0 {
			result.Comparison.RequiresRebuild = true
		}
	}
	result.Volumes, _, err = s.evaluateVolumes(ctx, volumeEvaluation{candidates: candidates, inputs: inputs, histories: histories, series: cfg.Series, config: cfg}, false)
	return result, err
}

func softwareVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	parts := []string{info.Main.Path, info.Main.Version, info.GoVersion}
	for _, setting := range info.Settings {
		if strings.HasPrefix(setting.Key, "vcs.") {
			parts = append(parts, setting.Key+"="+setting.Value)
		}
	}
	return strings.Join(parts, " ")
}

func policyChanges(before, after OutputPolicyState) string {
	var changes []string
	fields := []struct {
		name          string
		before, after any
	}{
		{"source present", before.SourcePresent, after.SourcePresent}, {"source enabled", before.SourceEnabled, after.SourceEnabled},
		{"format", before.Format, after.Format}, {"preface", before.Preface, after.Preface}, {"author", before.Author, after.Author},
		{"series title", before.SeriesTitle, after.SeriesTitle}, {"book", before.Book, after.Book}, {"sequence", before.Sequence, after.Sequence},
		{"output", before.Output, after.Output}, {"books", before.Books, after.Books}, {"destinations", before.Destinations, after.Destinations},
	}
	for _, field := range fields {
		if !reflect.DeepEqual(field.before, field.after) {
			a, _ := json.Marshal(field.before)
			b, _ := json.Marshal(field.after)
			changes = append(changes, fmt.Sprintf("%s: %s -> %s", field.name, a, b))
		}
	}
	return strings.Join(changes, "; ")
}

func (s *Service) storedReleases(ctx context.Context, source string) ([]domain.NormalizedRelease, error) {
	stored, err := s.Repo.ListReleases(ctx, source)
	if err != nil {
		return nil, err
	}
	releases := make([]domain.NormalizedRelease, 0, len(stored))
	for _, release := range stored {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		normalized, err := s.loadStoredNormalized(ctx, release)
		if err != nil {
			return nil, fmt.Errorf("stored release %s/%s: %w", source, release.ProviderReleaseID, err)
		}

		releases = append(releases, normalized)
	}
	return releases, nil
}
