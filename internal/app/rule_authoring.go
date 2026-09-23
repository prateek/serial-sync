package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	toml "github.com/pelletier/go-toml/v2"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/discovery"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/observe"
	"github.com/prateek/serial-sync/internal/provider"
)

type SourceDumpOptions struct {
	Path             string
	MembershipFilter string
	CreatorFilters   []string
	Force            bool
}

type SourceDumpCreator struct {
	SourceID       string `json:"source_id"`
	CreatorName    string `json:"creator_name"`
	CreatorHandle  string `json:"creator_handle"`
	MembershipKind string `json:"membership_kind"`
	PostCount      int    `json:"post_count"`
	Directory      string `json:"directory"`
	SourceFile     string `json:"source_file"`
	PostsFile      string `json:"posts_file"`
	RawPostsDir    string `json:"raw_posts_dir,omitempty"`
	AttachmentsDir string `json:"attachments_dir,omitempty"`
	Configured     bool   `json:"configured"`
	ExistingSource string `json:"existing_source_id,omitempty"`
}

type SourceDumpResult struct {
	RunID         string              `json:"run_id"`
	WorkspacePath string              `json:"workspace_path"`
	ManifestFile  string              `json:"manifest_file"`
	SourcesFile   string              `json:"sources_file"`
	SeriesFile    string              `json:"series_file"`
	Provider      string              `json:"provider"`
	AuthProfileID string              `json:"auth_profile_id"`
	Membership    string              `json:"membership"`
	TotalPosts    int                 `json:"total_posts"`
	Creators      []SourceDumpCreator `json:"creators"`
}

type RulesPreviewOptions struct {
	Suggest        bool
	Stored         bool
	CompareConfig  string
	WorkspacePath  string
	SeriesFile     string
	CreatorFilters []string
	ShowPosts      bool
}

type RulesPreviewCreator struct {
	SourceID       string                    `json:"source_id"`
	CreatorName    string                    `json:"creator_name"`
	CreatorHandle  string                    `json:"creator_handle"`
	MembershipKind string                    `json:"membership_kind"`
	PostCount      int                       `json:"post_count"`
	Preview        provider.DiscoveryPreview `json:"preview"`
}

type RulesPreviewResult struct {
	Labels          []discovery.LabelReport     `json:"labels"`
	Suggestions     string                      `json:"suggestions,omitempty"`
	SourcesFileHash string                      `json:"sources_file_hash,omitempty"`
	SeriesFileHash  string                      `json:"series_file_hash,omitempty"`
	Candidates      []domain.DiscoveryCandidate `json:"candidates"`
	Binding         ReplayBinding               `json:"binding"`
	Comparison      *ReplayComparison           `json:"comparison,omitempty"`
	Volumes         []domain.VolumePlan         `json:"volumes,omitempty"`
	RunID           string                      `json:"run_id,omitempty"`
	WorkspacePath   string                      `json:"workspace_path"`
	SeriesFile      string                      `json:"series_file"`
	TotalPosts      int                         `json:"total_posts"`
	Materializable  int                         `json:"materializable"`
	FallbackPosts   int                         `json:"fallback_posts"`
	Creators        []RulesPreviewCreator       `json:"creators"`
}

type dumpManifest struct {
	originalRoot   string
	Version        int                 `json:"version"`
	GeneratedAt    time.Time           `json:"generated_at"`
	Provider       string              `json:"provider"`
	AuthProfileID  string              `json:"auth_profile_id"`
	Membership     string              `json:"membership"`
	CreatorFilters []string            `json:"creator_filters,omitempty"`
	SourcesFile    string              `json:"sources_file"`
	SeriesFile     string              `json:"series_file"`
	Creators       []SourceDumpCreator `json:"creators"`
}

type dumpPostRecord struct {
	Normalized domain.NormalizedRelease `json:"normalized"`
}

type dumpReleaseHydrater interface {
	HydrateDumpReleases(ctx context.Context, auth config.AuthProfile, source config.SourceConfig, docs []provider.ReleaseDocument, fixtureDir string) ([]provider.ReleaseDocument, domain.AuthState, error)
}

const sourceDumpWorkerLimit = 2

func (s *Service) DumpSources(ctx context.Context, authFilter string, options SourceDumpOptions, command string) (result SourceDumpResult, err error) {
	recorder, err := observe.Start(ctx, s.Repo, command, strings.TrimSpace(authFilter), false, s.observeOptions())
	if err != nil {
		return SourceDumpResult{}, err
	}
	ctx = withRecorderProgress(ctx, recorder)
	result = SourceDumpResult{RunID: recorder.RunID()}
	defer func() {
		if err != nil {
			_ = recorder.Finish(context.WithoutCancel(ctx), domain.RunStatusFailed, err.Error())
		}
	}()

	auth, err := s.selectAuthProfile(authFilter)
	if err != nil {
		return result, err
	}
	client, ok := s.Providers.Get(auth.Provider)
	if !ok {
		return result, fmt.Errorf("no provider registered for %q", auth.Provider)
	}
	workspacePath, err := resolveWorkspacePath(options.Path)
	if err != nil {
		return result, err
	}
	capture, err := beginDumpCapture(workspacePath)
	if err != nil {
		return result, err
	}
	defer capture.close()

	discovered, err := client.DiscoverSources(ctx, auth, s.Config.Sources, provider.DiscoverOptions{
		MembershipFilter: firstNonEmpty(strings.TrimSpace(options.MembershipFilter), "paid"),
		CreatorFilters:   options.CreatorFilters,
	})
	if err != nil {
		return result, err
	}
	if len(discovered.Suggestions) == 0 {
		return result, fmt.Errorf("no creators matched the provided filters")
	}
	result.Provider = discovered.Provider
	result.AuthProfileID = auth.ID
	result.WorkspacePath = workspacePath
	result.Membership = firstNonEmpty(strings.TrimSpace(options.MembershipFilter), "paid")

	creatorsDir := filepath.Join(capture.directory, "creators")
	if err := os.MkdirAll(creatorsDir, 0o755); err != nil {
		return result, err
	}
	var sources []config.SourceConfig
	dumpCreators, err := s.dumpCreators(ctx, auth, client, workspacePath, creatorsDir, discovered.Suggestions)
	if err != nil {
		return result, err
	}
	for _, creator := range dumpCreators {
		result.TotalPosts += creator.Creator.PostCount
		result.Creators = append(result.Creators, creator.Creator)
		sources = append(sources, creator.Source)
		_ = recorder.Event(ctx, "info", "dump", fmt.Sprintf("dumped %d post(s) for %s", creator.Creator.PostCount, creator.Creator.SourceID), "source", creator.Creator.SourceID)
	}

	manifest := dumpManifest{
		GeneratedAt: time.Now().UTC(), Provider: discovered.Provider,
		AuthProfileID: auth.ID, Membership: result.Membership,
		CreatorFilters: append([]string(nil), options.CreatorFilters...), Creators: result.Creators,
	}
	if err := capture.install(ctx, manifest, sources); err != nil {
		return result, err
	}
	result.ManifestFile = filepath.Join(workspacePath, "manifest.json")
	result.SourcesFile = filepath.Join(capture.directory, "sources.toml")
	result.SeriesFile = filepath.Join(workspacePath, "series.toml")

	summary := fmt.Sprintf("creators=%d posts=%d workspace=%s", len(result.Creators), result.TotalPosts, workspacePath)
	if finishErr := recorder.Finish(ctx, domain.RunStatusSucceeded, summary); finishErr != nil {
		return result, finishErr
	}
	return result, nil
}

type dumpCreatorResult struct {
	Index   int
	Creator SourceDumpCreator
	Source  config.SourceConfig
	Err     error
}

func (s *Service) dumpCreators(ctx context.Context, auth config.AuthProfile, client provider.Client, workspacePath, creatorsDir string, suggestions []provider.SourceSuggestion) ([]dumpCreatorResult, error) {
	if len(suggestions) == 0 {
		return nil, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	workerCount := min(len(suggestions), dumpWorkerLimit(client))
	jobs := make(chan int)
	results := make(chan dumpCreatorResult, len(suggestions))
	var wg sync.WaitGroup
	for range workerCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				if err := ctx.Err(); err != nil {
					return
				}
				results <- s.dumpCreator(ctx, auth, client, workspacePath, creatorsDir, index, suggestions[index])
			}
		}()
	}
	go func() {
		defer close(jobs)
		for index := range len(suggestions) {
			select {
			case <-ctx.Done():
				return
			case jobs <- index:
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()
	ordered := make([]dumpCreatorResult, len(suggestions))
	var firstErr error
	for item := range results {
		if item.Err != nil && firstErr == nil {
			firstErr = item.Err
			cancel()
		}
		ordered[item.Index] = item
	}
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return ordered, nil
}

func dumpWorkerLimit(client provider.Client) int {
	// The provider owns its dump concurrency; its request budget is the rate
	// control. A provider without the optional interface keeps the default.
	if limited, ok := client.(provider.DumpWorkerLimit); ok {
		return limited.DumpWorkerLimit()
	}
	return sourceDumpWorkerLimit
}

func (s *Service) dumpCreator(ctx context.Context, auth config.AuthProfile, client provider.Client, workspacePath, creatorsDir string, index int, suggestion provider.SourceSuggestion) dumpCreatorResult {
	listResult, err := client.ListReleases(ctx, auth, suggestion.Source, nil)
	if err != nil {
		return dumpCreatorResult{Index: index, Err: err}
	}
	sourceDir := filepath.Join(creatorsDir, suggestion.Source.ID)
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		return dumpCreatorResult{Index: index, Err: err}
	}
	sourceFile := filepath.Join(sourceDir, "source.json")
	postsFile := filepath.Join(sourceDir, "posts.ndjson")
	rawPostsDir := filepath.Join(sourceDir, "posts")
	attachmentsDir := filepath.Join(sourceDir, "attachments")
	if hydrater, ok := client.(dumpReleaseHydrater); ok {
		listResult.Documents, _, err = hydrater.HydrateDumpReleases(ctx, auth, suggestion.Source, listResult.Documents, sourceDir)
		if err != nil {
			return dumpCreatorResult{Index: index, Err: err}
		}
	}
	if err := writeJSONFile(sourceFile, suggestion); err != nil {
		return dumpCreatorResult{Index: index, Err: err}
	}
	if err := writeDumpPosts(postsFile, workspacePath, listResult.Documents); err != nil {
		return dumpCreatorResult{Index: index, Err: err}
	}
	if err := writeDumpRawPosts(rawPostsDir, listResult.Documents); err != nil {
		return dumpCreatorResult{Index: index, Err: err}
	}
	return dumpCreatorResult{
		Index:  index,
		Source: suggestion.Source,
		Creator: SourceDumpCreator{
			SourceID:       suggestion.Source.ID,
			CreatorName:    suggestion.CreatorName,
			CreatorHandle:  suggestion.CreatorHandle,
			MembershipKind: suggestion.MembershipKind,
			PostCount:      len(listResult.Documents),
			Directory:      sourceDir,
			SourceFile:     sourceFile,
			PostsFile:      postsFile,
			RawPostsDir:    rawPostsDir,
			AttachmentsDir: attachmentsDir,
			Configured:     suggestion.AlreadyConfigured,
			ExistingSource: suggestion.ExistingSourceID,
		},
	}
}

func (s *Service) PreviewRules(ctx context.Context, options RulesPreviewOptions, command string) (RulesPreviewResult, error) {
	return s.replay(ctx, options)
}

func FormatSourceDumpResult(result SourceDumpResult) string {
	lines := []string{
		fmt.Sprintf("run_id=%s workspace=%s creators=%d posts=%d membership=%s", result.RunID, result.WorkspacePath, len(result.Creators), result.TotalPosts, result.Membership),
		fmt.Sprintf("manifest=%s", result.ManifestFile),
		fmt.Sprintf("sources=%s", result.SourcesFile),
		fmt.Sprintf("series=%s", result.SeriesFile),
	}
	for _, creator := range result.Creators {
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s\tposts=%d", creator.SourceID, creator.CreatorName, firstNonEmpty(creator.MembershipKind, "unknown"), creator.PostCount))
	}
	return strings.Join(lines, "\n")
}

func FormatRulesPreviewResult(result RulesPreviewResult, showPosts bool) string {
	lines := []string{
		fmt.Sprintf("workspace=%s series=%s creators=%d posts=%d materializable=%d fallback=%d", result.WorkspacePath, result.SeriesFile, len(result.Creators), result.TotalPosts, result.Materializable, result.FallbackPosts),
	}
	for _, creator := range result.Creators {
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s\tposts=%d", creator.SourceID, creator.CreatorName, firstNonEmpty(creator.MembershipKind, "unknown"), creator.PostCount))
		for _, group := range creator.Preview.Groups {
			label := group.MatchType
			if strings.TrimSpace(group.MatchValue) != "" {
				label += ":" + group.MatchValue
			}
			lines = append(lines, fmt.Sprintf("  - %s [%s %s] posts=%d materializable=%d", group.TrackKey, label, group.ContentStrategy, group.Total, group.Materializable))
			if len(group.SampleTitles) > 0 {
				lines = append(lines, "    titles: "+strings.Join(group.SampleTitles, " | "))
			}
		}
		if showPosts {
			for _, post := range creator.Preview.Posts {
				label := post.MatchType
				if strings.TrimSpace(post.MatchValue) != "" {
					label += ":" + post.MatchValue
				}
				lines = append(lines, fmt.Sprintf("    * %s [%s %s materializable=%t] %s", post.TrackKey, label, post.ContentStrategy, post.Materializable, post.Title))
				lines = append(lines, "      eligibility: "+post.Eligibility+"; content: "+post.SelectedContent.Kind+" "+post.SelectedContent.FileName)
				if len(post.Explanation.HeldReasons) > 0 {
					lines = append(lines, "      held for review: "+strings.Join(post.Explanation.HeldReasons, ", "))
				}
				for _, conflict := range post.Explanation.Conflicts {
					lines = append(lines, fmt.Sprintf("      label/title conflict: %s / %s", conflict.LabelSeries, conflict.TitleSeries))
				}
				for _, attempt := range post.Explanation.Attempts {
					if len(attempt.Selectors) == 0 {
						continue
					}
					marker := "passed over"
					if attempt.Selected {
						marker = "selected"
					}
					lines = append(lines, fmt.Sprintf("      %s: %s %s", attempt.Input, marker, attempt.Reason))
					for _, guard := range attempt.Guards {
						lines = append(lines, fmt.Sprintf("        %s passed=%t %s", guard.Field, guard.Passed, guard.Detail))
					}
				}
				if post.Materializable {
					lines = append(lines, "      output: "+post.Filename)
					if seq := post.Sequence; seq != nil {
						lines = append(lines, fmt.Sprintf("      sequence: book=%s chapter=%d chapter_label=%q part=%q position=%d series_index=%q origin=%s matched=%q %s", seq.BookID, seq.Chapter, seq.ChapterLabel, seq.Part, seq.Position, seq.SeriesIndex, seq.Origin, seq.MatchedText, seq.Reason))
					}
				}
			}
		}
	}
	for _, report := range result.Labels {
		for _, label := range report.Labels {
			lines = append(lines, fmt.Sprintf("collection %s: %s", label.Key(), strings.Join(label.Names, " | ")))
		}
		for _, drift := range report.PossibleDrift {
			lines = append(lines, fmt.Sprintf("possible drift: %s/%s %s %q: %s (%s)", report.Source, drift.Series, drift.ID, drift.Name, drift.Detail, strings.Join(drift.Observed, ", ")))
		}
		if report.MissingIdentities > 0 {
			lines = append(lines, fmt.Sprintf("%s: %d captured posts have no collection identity enrichment", report.Source, report.MissingIdentities))
		}
	}
	lines = append(lines, FormatCandidates(result.Candidates))
	if result.Suggestions != "" {
		lines = append(lines, "Draft config (review before installing):", result.Suggestions)
	}
	if comparison := result.Comparison; comparison != nil {
		lines = append(lines, fmt.Sprintf("classification changes: %d", len(comparison.Classification)))
		for _, change := range comparison.Classification {
			lines = append(lines, fmt.Sprintf("  %s/%s: %s (%s, %s) -> %s (%s, %s)", change.Source, change.ReleaseID, change.Before.Series, change.Before.Role, change.Before.Eligibility, change.After.Series, change.After.Role, change.After.Eligibility))
		}
		lines = append(lines, fmt.Sprintf("output policy changes: %d", len(comparison.OutputPolicy)))
		for _, change := range comparison.OutputPolicy {
			lines = append(lines, fmt.Sprintf("  %s/%s: %s", change.Source, change.ReleaseID, policyChanges(change.Before, change.After)))
		}
		lines = append(lines, formatAppliedLibrary(comparison.AppliedLibrary)...)
		if comparison.RequiresRebuild {
			lines = append(lines, "Stored output needs run --rebuild after promotion; ordinary sync does not apply mapping changes to unchanged content.")
		}
	}
	lines = append(lines, fmt.Sprintf("capture=%s config=%s", result.Binding.CaptureHash, result.Binding.ConfigHash))
	for _, volume := range result.Volumes {
		lines = append(lines, formatVolumePlan(volume))
	}
	return strings.Join(lines, "\n")
}

func formatVolumePlan(plan domain.VolumePlan) string {
	return fmt.Sprintf("volume %s %s [%s] expected=%d..%d present=%v missing=%v intentional=%v output=%s %s", plan.SeriesID, plan.GroupID, plan.Status, plan.First, plan.Last, plan.Present, plan.Missing, plan.IntentionalGaps, plan.Filename, plan.Reason)
}

func resolveWorkspacePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		path = filepath.Join(cwd, "serial-sync-rule-workspace")
	}
	return filepath.Abs(path)
}

func writeDumpPosts(path, workspacePath string, docs []provider.ReleaseDocument) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	writer := bufio.NewWriter(file)
	encoder := json.NewEncoder(writer)
	for _, doc := range docs {
		normalized := doc.Normalized
		if normalized.Enrichment != nil {
			data, err := json.Marshal(normalized.Enrichment)
			if err != nil {
				return err
			}
			var enrichment domain.ReleaseEnrichment
			if err := json.Unmarshal(data, &enrichment); err != nil {
				return err
			}
			normalized.Enrichment = &enrichment
			for _, asset := range enrichment.Assets() {
				if asset.Path == "" {
					continue
				}
				data, err := os.ReadFile(asset.Path)
				if err != nil {
					return err
				}
				if hashBytes(data) != asset.SHA256 {
					return fmt.Errorf("metadata image changed: %s", asset.Path)
				}
				target := filepath.Join(filepath.Dir(path), "metadata-assets", asset.SHA256)
				if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
					return err
				}
				if err := os.WriteFile(target, data, 0644); err != nil {
					return err
				}
				rel, err := filepath.Rel(workspacePath, target)
				if err != nil || !filepath.IsLocal(rel) {
					return fmt.Errorf("metadata image outside workspace: %s", target)
				}
				asset.Path = filepath.ToSlash(rel)
			}
		}
		normalized.Attachments = append([]domain.Attachment(nil), normalized.Attachments...)
		for i := range normalized.Attachments {
			attachment := &normalized.Attachments[i]
			if attachment.LocalPath == "" {
				continue
			}
			rel, err := filepath.Rel(workspacePath, attachment.LocalPath)
			if err != nil || !filepath.IsLocal(rel) {
				return fmt.Errorf("attachment path is outside workspace: %s", attachment.LocalPath)
			}
			attachment.LocalPath = filepath.ToSlash(rel)
		}
		if err := encoder.Encode(dumpPostRecord{Normalized: normalized}); err != nil {
			return err
		}
	}
	return writer.Flush()
}

func writeDumpRawPosts(path string, docs []provider.ReleaseDocument) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	for _, doc := range docs {
		postID := strings.TrimSpace(doc.Normalized.ProviderReleaseID)
		if postID == "" {
			return fmt.Errorf("dump post is missing provider_release_id")
		}
		if len(doc.RawJSON) == 0 {
			return fmt.Errorf("dump post %s is missing raw JSON", postID)
		}
		postPath := filepath.Join(path, postID+".json")
		if err := os.WriteFile(postPath, doc.RawJSON, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func loadDumpPosts(path string) ([]domain.NormalizedRelease, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	releases := []domain.NormalizedRelease{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var record dumpPostRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return nil, err
		}
		releases = append(releases, record.Normalized)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return releases, nil
}

func loadDumpManifest(path string) (dumpManifest, error) {
	var manifest dumpManifest
	data, err := os.ReadFile(path)
	if err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, err
	}
	return relocateManifest(filepath.Dir(path), manifest)
}

func filterRulesBySource(rules []config.RuleConfig, sourceID string) []config.RuleConfig {
	filtered := make([]config.RuleConfig, 0, len(rules))
	for _, rule := range rules {
		if strings.TrimSpace(rule.Source) != sourceID {
			continue
		}
		filtered = append(filtered, rule)
	}
	return filtered
}

func matchesDumpCreatorFilters(creator SourceDumpCreator, filters []string) bool {
	if len(filters) == 0 {
		return true
	}
	candidates := []string{
		strings.ToLower(strings.TrimSpace(creator.SourceID)),
		strings.ToLower(strings.TrimSpace(creator.CreatorHandle)),
		strings.ToLower(strings.TrimSpace(creator.CreatorName)),
	}
	for _, raw := range filters {
		filter := strings.ToLower(strings.TrimSpace(raw))
		if filter == "" {
			continue
		}
		for _, candidate := range candidates {
			if candidate == "" {
				continue
			}
			if candidate == filter || strings.Contains(candidate, filter) {
				return true
			}
		}
	}
	return false
}

func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func writeTOMLFile(path string, value any) error {
	data, err := toml.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func workspaceReadme(path string) string {
	return strings.TrimSpace(fmt.Sprintf(`
# serial-sync series workspace

This directory is a local series-authoring workspace and full Patreon dump.

- Edit series in %s
- Inspect normalized posts in captures/<generation>/creators/<source-id>/posts.ndjson
- Raw Patreon post payloads live in captures/<generation>/creators/<source-id>/posts/
- Downloaded source attachments live in captures/<generation>/creators/<source-id>/attachments/
- Preview those series definitions offline with:
  serial-sync setup preview --workspace %s --show-posts
- Creator directories are fixture-compatible captures for later offline replay/materialization work.
- Read manifest.json for the current capture paths. Refreshes preserve authored files.
- Merge the resulting sources from the reported sources file and series from series.toml into your main config when you are happy with the results.
`, filepath.Base(filepath.Join(path, "series.toml")), path)) + "\n"
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

const defaultSeriesScaffold = `# Add [[series]] entries here, then run:
# serial-sync setup preview --workspace <workspace> --series-file ./series.toml --show-posts
#
# [[series]]
# id = "main-series"
# title = "Main Series"
# authors = ["Author Name"]
#
#   [series.output]
#   format = "epub"
#   preface_mode = "prepend_post"
#
#   [[series.inputs]]
#   source = "creator-id"
#   priority = 10
#   match_type = "title_regex"
#   match_value = "^Main Series"
#   release_role = "chapter"
#   content_strategy = "attachment_preferred"
#   attachment_glob = ["*.epub", "*.pdf"]
#   attachment_priority = ["epub", "pdf"]
`
