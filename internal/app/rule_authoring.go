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
	Configured     bool   `json:"configured"`
	ExistingSource string `json:"existing_source_id,omitempty"`
}

type SourceDumpResult struct {
	RunID         string              `json:"run_id"`
	WorkspacePath string              `json:"workspace_path"`
	ManifestFile  string              `json:"manifest_file"`
	SourcesFile   string              `json:"sources_file"`
	RulesFile     string              `json:"rules_file"`
	Provider      string              `json:"provider"`
	AuthProfileID string              `json:"auth_profile_id"`
	Membership    string              `json:"membership"`
	TotalPosts    int                 `json:"total_posts"`
	Creators      []SourceDumpCreator `json:"creators"`
}

type RulesPreviewOptions struct {
	WorkspacePath  string
	RulesFile      string
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
	RulesFile      string                `json:"rules_file"`
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
	RulesFile      string              `json:"rules_file"`
	Creators       []SourceDumpCreator `json:"creators"`
}

type dumpPostRecord struct {
	Normalized domain.NormalizedRelease `json:"normalized"`
}

type rulesFileConfig struct {
	Rules []config.RuleConfig `toml:"rules"`
}

const sourceDumpWorkerLimit = 2

func (s *Service) DumpSources(ctx context.Context, authFilter string, options SourceDumpOptions, command string) (SourceDumpResult, error) {
	recorder, err := observe.Start(ctx, s.Repo, command, strings.TrimSpace(authFilter), false, s.observeOptions())
	if err != nil {
		return SourceDumpResult{}, err
	}
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
	rulesFile := filepath.Join(workspacePath, "rules.toml")
	if err := os.WriteFile(rulesFile, []byte(defaultRulesScaffold), 0o644); err != nil {
		return result, err
	}
	manifest := dumpManifest{
		Version:        1,
		GeneratedAt:    time.Now().UTC(),
		Provider:       discovered.Provider,
		AuthProfileID:  auth.ID,
		Membership:     result.Membership,
		CreatorFilters: append([]string(nil), options.CreatorFilters...),
		SourcesFile:    sourcesFile,
		RulesFile:      rulesFile,
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
	result.RulesFile = rulesFile
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
	workerCount := min(len(suggestions), sourceDumpWorkerLimit)
	jobs := make(chan int)
	results := make(chan dumpCreatorResult, len(suggestions))
	var wg sync.WaitGroup
	for range workerCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				results <- s.dumpCreator(ctx, auth, client, creatorsDir, index, suggestions[index])
			}
		}()
	}
	go func() {
		for index := range len(suggestions) {
			jobs <- index
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	ordered := make([]dumpCreatorResult, len(suggestions))
	var firstErr error
	for item := range results {
		if item.Err != nil && firstErr == nil {
			firstErr = item.Err
		}
		ordered[item.Index] = item
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return ordered, nil
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
	if err := writeJSONFile(sourceFile, suggestion); err != nil {
		return dumpCreatorResult{Index: index, Err: err}
	}
	if err := writeDumpPosts(postsFile, listResult.Documents); err != nil {
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
	rulesFile := strings.TrimSpace(options.RulesFile)
	if rulesFile == "" {
		rulesFile = manifest.RulesFile
	}
	if !filepath.IsAbs(rulesFile) {
		rulesFile = filepath.Join(workspacePath, rulesFile)
	}
	rules, err := loadRulesFile(rulesFile)
	if err != nil {
		return result, err
	}
	result.WorkspacePath = workspacePath
	result.RulesFile = rulesFile
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
		fmt.Sprintf("rules=%s", result.RulesFile),
	}
	for _, creator := range result.Creators {
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s\tposts=%d", creator.SourceID, creator.CreatorName, firstNonEmpty(creator.MembershipKind, "unknown"), creator.PostCount))
	}
	return strings.Join(lines, "\n")
}

func FormatRulesPreviewResult(result RulesPreviewResult, showPosts bool) string {
	lines := []string{
		fmt.Sprintf("run_id=%s workspace=%s rules=%s creators=%d posts=%d materializable=%d fallback=%d", result.RunID, result.WorkspacePath, result.RulesFile, len(result.Creators), result.TotalPosts, result.Materializable, result.FallbackPosts),
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

func loadRulesFile(path string) ([]config.RuleConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ruleConfig rulesFileConfig
	if err := toml.Unmarshal(data, &ruleConfig); err != nil {
		return nil, err
	}
	return ruleConfig.Rules, nil
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
# serial-sync rule workspace

This directory is a local rule-authoring workspace.

- Edit rules in %s
- Preview those rules offline with:
  serial-sync rules preview --workspace %s --show-posts
- Merge the resulting sources from sources.toml into your main config when you are happy with the rules.
`, filepath.Base(filepath.Join(path, "rules.toml")), path)) + "\n"
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

const defaultRulesScaffold = `# Add [[rules]] entries here, then run:
# serial-sync rules preview --workspace <workspace> --rules-file ./rules.toml --show-posts
`
