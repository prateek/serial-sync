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
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/observe"
	"github.com/prateek/serial-sync/internal/provider"
	"github.com/prateek/serial-sync/internal/rulepreview"
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
	RunID          string                `json:"run_id"`
	WorkspacePath  string                `json:"workspace_path"`
	SeriesFile     string                `json:"series_file"`
	TotalPosts     int                   `json:"total_posts"`
	Materializable int                   `json:"materializable"`
	FallbackPosts  int                   `json:"fallback_posts"`
	Creators       []RulesPreviewCreator `json:"creators"`
}

type dumpManifest struct {
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

type seriesFileConfig struct {
	Series []config.SeriesConfig `toml:"series"`
}

type dumpReleaseHydrater interface {
	HydrateDumpReleases(ctx context.Context, auth config.AuthProfile, source config.SourceConfig, docs []provider.ReleaseDocument, fixtureDir string) ([]provider.ReleaseDocument, domain.AuthState, error)
}

const sourceDumpWorkerLimit = 2

func (s *Service) DumpSources(ctx context.Context, authFilter string, options SourceDumpOptions, command string) (SourceDumpResult, error) {
	recorder, err := observe.Start(ctx, s.Repo, command, strings.TrimSpace(authFilter), false, s.observeOptions())
	if err != nil {
		return SourceDumpResult{}, err
	}
	ctx = withRecorderProgress(ctx, recorder)
	result := SourceDumpResult{RunID: recorder.RunID()}
	defer func() {
		if err != nil {
			_ = recorder.Finish(ctx, domain.RunStatusFailed, err.Error())
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
	if err := prepareWorkspaceRoot(workspacePath, options.Force); err != nil {
		return result, err
	}

	discovered, err := client.DiscoverSources(ctx, auth, s.Config.Sources, provider.DiscoverOptions{
		MembershipFilter:  firstNonEmpty(strings.TrimSpace(options.MembershipFilter), "paid"),
		CreatorFilters:    options.CreatorFilters,
		IncludeConfigured: true,
		MetadataOnly:      true,
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

	creatorsDir := filepath.Join(workspacePath, "creators")
	if err := os.MkdirAll(creatorsDir, 0o755); err != nil {
		return result, err
	}
	sourcesSnippet := struct {
		Sources []config.SourceConfig `toml:"sources"`
	}{}
	dumpCreators, err := s.dumpCreators(ctx, auth, client, creatorsDir, discovered.Suggestions)
	if err != nil {
		return result, err
	}
	for _, creator := range dumpCreators {
		result.TotalPosts += creator.Creator.PostCount
		result.Creators = append(result.Creators, creator.Creator)
		sourcesSnippet.Sources = append(sourcesSnippet.Sources, creator.Source)
		_ = recorder.Event(ctx, "info", "dump", fmt.Sprintf("dumped %d post(s) for %s", creator.Creator.PostCount, creator.Creator.SourceID), "source", creator.Creator.SourceID)
	}

	sourcesFile := filepath.Join(workspacePath, "sources.toml")
	if err := writeTOMLFile(sourcesFile, sourcesSnippet); err != nil {
		return result, err
	}
	seriesFile := filepath.Join(workspacePath, "series.toml")
	if err := os.WriteFile(seriesFile, []byte(defaultSeriesScaffold), 0o644); err != nil {
		return result, err
	}
	manifest := dumpManifest{
		Version:        2,
		GeneratedAt:    time.Now().UTC(),
		Provider:       discovered.Provider,
		AuthProfileID:  auth.ID,
		Membership:     result.Membership,
		CreatorFilters: append([]string(nil), options.CreatorFilters...),
		SourcesFile:    sourcesFile,
		SeriesFile:     seriesFile,
		Creators:       result.Creators,
	}
	manifestFile := filepath.Join(workspacePath, "manifest.json")
	if err := writeJSONFile(manifestFile, manifest); err != nil {
		return result, err
	}
	if err := os.WriteFile(filepath.Join(workspacePath, "README.md"), []byte(workspaceReadme(workspacePath)), 0o644); err != nil {
		return result, err
	}

	result.ManifestFile = manifestFile
	result.SourcesFile = sourcesFile
	result.SeriesFile = seriesFile
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

func (s *Service) dumpCreators(ctx context.Context, auth config.AuthProfile, client provider.Client, creatorsDir string, suggestions []provider.SourceSuggestion) ([]dumpCreatorResult, error) {
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
				results <- s.dumpCreator(ctx, auth, client, creatorsDir, index, suggestions[index])
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
	return ordered, nil
}

func dumpWorkerLimit(client provider.Client) int {
	// Patreon uses per-session request budgeting inside each live client. Running
	// multiple creator dumps in parallel stacks those budgets and reintroduces
	// account-level 429s during one-shot authoring dumps, so keep creator dumps
	// serialized for that provider.
	if client != nil && client.Name() == "patreon" {
		return 1
	}
	return sourceDumpWorkerLimit
}

func (s *Service) dumpCreator(ctx context.Context, auth config.AuthProfile, client provider.Client, creatorsDir string, index int, suggestion provider.SourceSuggestion) dumpCreatorResult {
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
	if err := writeDumpPosts(postsFile, listResult.Documents); err != nil {
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
	recorder, err := observe.Start(ctx, s.Repo, command, strings.Join(options.CreatorFilters, ","), false, s.observeOptions())
	if err != nil {
		return RulesPreviewResult{}, err
	}
	ctx = withRecorderProgress(ctx, recorder)
	result := RulesPreviewResult{RunID: recorder.RunID()}
	defer func() {
		if err != nil {
			_ = recorder.Finish(ctx, domain.RunStatusFailed, err.Error())
		}
	}()

	workspacePath, err := resolveWorkspacePath(options.WorkspacePath)
	if err != nil {
		return result, err
	}
	manifest, err := loadDumpManifest(filepath.Join(workspacePath, "manifest.json"))
	if err != nil {
		return result, err
	}
	seriesFile := strings.TrimSpace(options.SeriesFile)
	if seriesFile == "" {
		seriesFile = manifest.SeriesFile
	}
	if !filepath.IsAbs(seriesFile) {
		seriesFile = filepath.Join(workspacePath, seriesFile)
	}
	rules, err := loadSeriesFile(seriesFile)
	if err != nil {
		return result, err
	}
	result.WorkspacePath = workspacePath
	result.SeriesFile = seriesFile
	for _, creator := range manifest.Creators {
		if !matchesDumpCreatorFilters(creator, options.CreatorFilters) {
			continue
		}
		releases, err := loadDumpPosts(creator.PostsFile)
		if err != nil {
			return result, err
		}
		preview := rulepreview.Build(creator.SourceID, releases, filterRulesBySource(rules, creator.SourceID), options.ShowPosts)
		result.TotalPosts += len(releases)
		result.Materializable += preview.Materializable
		result.FallbackPosts += preview.FallbackPosts
		result.Creators = append(result.Creators, RulesPreviewCreator{
			SourceID:       creator.SourceID,
			CreatorName:    creator.CreatorName,
			CreatorHandle:  creator.CreatorHandle,
			MembershipKind: creator.MembershipKind,
			PostCount:      len(releases),
			Preview:        preview,
		})
		_ = recorder.Event(ctx, "info", "rules-preview", fmt.Sprintf("previewed %d post(s) for %s", len(releases), creator.SourceID), "source", creator.SourceID)
	}
	if len(result.Creators) == 0 {
		return result, fmt.Errorf("no dumped creators matched the provided filters")
	}
	summary := fmt.Sprintf("creators=%d posts=%d materializable=%d fallback=%d", len(result.Creators), result.TotalPosts, result.Materializable, result.FallbackPosts)
	if finishErr := recorder.Finish(ctx, domain.RunStatusSucceeded, summary); finishErr != nil {
		return result, finishErr
	}
	return result, nil
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
		fmt.Sprintf("run_id=%s workspace=%s series=%s creators=%d posts=%d materializable=%d fallback=%d", result.RunID, result.WorkspacePath, result.SeriesFile, len(result.Creators), result.TotalPosts, result.Materializable, result.FallbackPosts),
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
			}
		}
	}
	return strings.Join(lines, "\n")
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

func prepareWorkspaceRoot(path string, force bool) error {
	if info, err := os.Stat(path); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("workspace path %s exists and is not a directory", path)
		}
		if !force {
			return fmt.Errorf("workspace path %s already exists; rerun with --force to overwrite", path)
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.MkdirAll(path, 0o755)
}

func writeDumpPosts(path string, docs []provider.ReleaseDocument) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	writer := bufio.NewWriter(file)
	encoder := json.NewEncoder(writer)
	for _, doc := range docs {
		if err := encoder.Encode(dumpPostRecord{Normalized: doc.Normalized}); err != nil {
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
	return manifest, nil
}

func loadSeriesFile(path string) ([]config.RuleConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var fileConfig seriesFileConfig
	if err := toml.Unmarshal(data, &fileConfig); err != nil {
		return nil, err
	}
	return config.CompileSeriesRules(fileConfig.Series), nil
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
- Inspect normalized posts in creators/<source-id>/posts.ndjson
- Raw Patreon post payloads live in creators/<source-id>/posts/
- Downloaded source attachments live in creators/<source-id>/attachments/
- Preview those series definitions offline with:
  serial-sync setup preview --workspace %s --show-posts
- Creator directories are fixture-compatible captures for later offline replay/materialization work.
- Merge the resulting sources from sources.toml and series from series.toml into your main config when you are happy with the results.
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
