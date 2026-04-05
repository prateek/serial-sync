package patreon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/provider"
)

var creatorPattern = regexp.MustCompile(`^https?://(?:www\.)?patreon\.com/(?:(?:c|cw)/)?[^/?#]+/posts(?:[/?#].*)?$`)
var collectionPattern = regexp.MustCompile(`^https?://(?:www\.)?patreon\.com/collection/[^/?#]+(?:[/?#].*)?$`)

type sessionBootstrapper func(context.Context, config.AuthProfile, config.SourceConfig, string) (domain.AuthState, error)

type Client struct {
	apiBaseURL string
	loginURL   string
	bootstrap  sessionBootstrapper
}

func New() *Client {
	return &Client{
		apiBaseURL: "https://www.patreon.com",
		loginURL:   "https://www.patreon.com/login",
		bootstrap:  bootstrapWithChromium,
	}
}

func (c *Client) Name() string {
	return "patreon"
}

func (c *Client) ValidateSource(source config.SourceConfig) error {
	if detectSourceKind(source.URL) != sourceKindUnknown && isPatreonSourceHost(source.URL) {
		return nil
	}
	return fmt.Errorf("patreon source %q must look like a creator posts feed or collection URL", source.ID)
}

func (c *Client) ValidateSession(ctx context.Context, auth config.AuthProfile, source config.SourceConfig) (domain.AuthState, error) {
	if source.FixtureDir != "" || normalizeAuthMode(auth.Mode) == "fixture" {
		return domain.AuthStateAuthenticated, nil
	}
	_, authState, err := c.resolveLiveSession(ctx, auth, source)
	return authState, err
}

func (c *Client) BootstrapAuth(ctx context.Context, auth config.AuthProfile, source config.SourceConfig, force bool) (provider.AuthBootstrapResult, error) {
	if err := c.ValidateSource(source); err != nil {
		return provider.AuthBootstrapResult{State: domain.AuthStateReauthRequired, Action: "failed"}, err
	}
	if source.FixtureDir != "" || normalizeAuthMode(auth.Mode) == "fixture" {
		return provider.AuthBootstrapResult{State: domain.AuthStateAuthenticated, Action: "fixture"}, nil
	}
	if normalizeAuthMode(auth.Mode) != "username_password" {
		return provider.AuthBootstrapResult{State: domain.AuthStateReauthRequired, Action: "failed"}, fmt.Errorf("patreon auth profile %q must use username_password mode for live bootstrap", auth.ID)
	}
	if auth.SessionPath == "" {
		return provider.AuthBootstrapResult{State: domain.AuthStateReauthRequired, Action: "failed"}, fmt.Errorf("patreon auth profile %q must define session_path", auth.ID)
	}
	if force {
		if err := os.Remove(auth.SessionPath); err != nil && !os.IsNotExist(err) {
			return provider.AuthBootstrapResult{State: domain.AuthStateReauthRequired, Action: "failed"}, fmt.Errorf("remove existing Patreon session: %w", err)
		}
	} else if _, authState, err := c.resolveLiveSession(ctx, auth, source); err == nil {
		return provider.AuthBootstrapResult{State: authState, Action: "reused"}, nil
	} else if authState == domain.AuthStateChallengeNeeded || authState == domain.AuthStateAuthenticated {
		return provider.AuthBootstrapResult{State: authState, Action: "failed"}, err
	}
	if c.bootstrap == nil {
		return provider.AuthBootstrapResult{State: domain.AuthStateReauthRequired, Action: "failed"}, fmt.Errorf("no Patreon bootstrapper configured")
	}
	authState, err := c.bootstrap(ctx, auth, source, sessionProfileDir(auth.SessionPath))
	if err != nil {
		return provider.AuthBootstrapResult{State: authState, Action: "bootstrapped"}, err
	}
	if _, err := loadSessionBundle(auth.SessionPath); err != nil {
		return provider.AuthBootstrapResult{State: domain.AuthStateReauthRequired, Action: "bootstrapped"}, fmt.Errorf("load bootstrapped Patreon session: %w", err)
	}
	return provider.AuthBootstrapResult{State: domain.AuthStateAuthenticated, Action: "bootstrapped"}, nil
}

func (c *Client) ListReleases(ctx context.Context, auth config.AuthProfile, source config.SourceConfig, storedSource *domain.Source) (provider.ListResult, error) {
	if err := c.ValidateSource(source); err != nil {
		return provider.ListResult{AuthState: domain.AuthStateReauthRequired}, err
	}
	if source.FixtureDir != "" {
		return c.listFixtureReleases(ctx, source)
	}
	return c.listLiveReleases(ctx, auth, source, storedSource)
}

func (c *Client) PrepareRelease(ctx context.Context, auth config.AuthProfile, source config.SourceConfig, doc provider.ReleaseDocument, decision domain.TrackDecision) (provider.ReleaseDocument, domain.AuthState, error) {
	if source.FixtureDir != "" {
		return doc, domain.AuthStateAuthenticated, nil
	}
	return c.prepareLiveRelease(ctx, auth, source, doc, decision)
}

func (c *Client) listFixtureReleases(ctx context.Context, source config.SourceConfig) (provider.ListResult, error) {
	postFiles, err := filepath.Glob(filepath.Join(source.FixtureDir, "posts", "*.json"))
	if err != nil {
		return provider.ListResult{AuthState: domain.AuthStateReauthRequired}, err
	}
	sort.Strings(postFiles)
	docs := make([]provider.ReleaseDocument, 0, len(postFiles))
	for _, postPath := range postFiles {
		select {
		case <-ctx.Done():
			return provider.ListResult{AuthState: domain.AuthStateReauthRequired}, ctx.Err()
		default:
		}
		raw, err := os.ReadFile(postPath)
		if err != nil {
			return provider.ListResult{AuthState: domain.AuthStateReauthRequired}, err
		}
		norm, err := parsePost(raw, source.FixtureDir)
		if err != nil {
			return provider.ListResult{AuthState: domain.AuthStateReauthRequired}, fmt.Errorf("parse %s: %w", postPath, err)
		}
		docs = append(docs, provider.ReleaseDocument{
			Normalized: norm,
			RawJSON:    append(json.RawMessage(nil), raw...),
		})
	}
	provider.SortReleaseDocuments(docs)
	return provider.ListResult{
		Documents:  docs,
		AuthState:  domain.AuthStateAuthenticated,
		SyncCursor: buildLiveSyncCursor(docs),
	}, nil
}

func isPatreonSourceHost(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "patreon.com" || strings.HasSuffix(host, ".patreon.com") || host == "localhost" {
		return true
	}
	return net.ParseIP(host) != nil
}

type postEnvelope struct {
	Data struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Attributes struct {
			Title              string `json:"title"`
			PostType           string `json:"post_type"`
			CurrentUserCanView bool   `json:"current_user_can_view"`
			URL                string `json:"url"`
			Content            string `json:"content"`
			ContentJSONString  string `json:"content_json_string"`
			PublishedAt        string `json:"published_at"`
			EditedAt           string `json:"edited_at"`
		} `json:"attributes"`
		Relationships struct {
			Campaign struct {
				Data struct {
					ID string `json:"id"`
				} `json:"data"`
			} `json:"campaign"`
			User struct {
				Data struct {
					ID string `json:"id"`
				} `json:"data"`
			} `json:"user"`
			Collections struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
			} `json:"collections"`
			UserDefinedTags struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
			} `json:"user_defined_tags"`
			AttachmentsMedia struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
			} `json:"attachments_media"`
		} `json:"relationships"`
	} `json:"data"`
	Included []struct {
		ID            string          `json:"id"`
		Type          string          `json:"type"`
		Attributes    json.RawMessage `json:"attributes"`
		Relationships struct {
			Creator struct {
				Data struct {
					ID string `json:"id"`
				} `json:"data"`
			} `json:"creator"`
		} `json:"relationships"`
	} `json:"included"`
}

func parsePost(raw []byte, fixtureDir string) (domain.NormalizedRelease, error) {
	var envelope postEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return domain.NormalizedRelease{}, err
	}
	includedByID := map[string]postEnvelopeIncluded{}
	for _, item := range envelope.Included {
		includedByID[item.ID] = postEnvelopeIncluded{
			Type:       item.Type,
			Attributes: item.Attributes,
			CreatorID:  item.Relationships.Creator.Data.ID,
		}
	}
	publishedAt, _ := parseTime(envelope.Data.Attributes.PublishedAt)
	editedAt, _ := parseTime(envelope.Data.Attributes.EditedAt)
	textHTML, textPlain := extractContent(envelope.Data.Attributes.Content, envelope.Data.Attributes.ContentJSONString)
	tags := make([]string, 0, len(envelope.Data.Relationships.UserDefinedTags.Data))
	for _, tagRef := range envelope.Data.Relationships.UserDefinedTags.Data {
		var attrs struct {
			Value string `json:"value"`
		}
		if item, ok := includedByID[tagRef.ID]; ok {
			_ = json.Unmarshal(item.Attributes, &attrs)
			if attrs.Value != "" {
				tags = append(tags, attrs.Value)
			}
		}
	}
	collections := make([]string, 0, len(envelope.Data.Relationships.Collections.Data))
	for _, collectionRef := range envelope.Data.Relationships.Collections.Data {
		var attrs struct {
			Title string `json:"title"`
		}
		if item, ok := includedByID[collectionRef.ID]; ok {
			_ = json.Unmarshal(item.Attributes, &attrs)
			if attrs.Title != "" {
				collections = append(collections, attrs.Title)
			}
		}
	}
	attachments := make([]domain.Attachment, 0, len(envelope.Data.Relationships.AttachmentsMedia.Data))
	for _, mediaRef := range envelope.Data.Relationships.AttachmentsMedia.Data {
		var attrs struct {
			FileName    string `json:"file_name"`
			MIMEType    string `json:"mimetype"`
			DownloadURL string `json:"download_url"`
		}
		if item, ok := includedByID[mediaRef.ID]; ok {
			_ = json.Unmarshal(item.Attributes, &attrs)
			if attrs.FileName == "" {
				continue
			}
			localPath := filepath.Join(fixtureDir, "attachments", envelope.Data.ID, attrs.FileName)
			if _, err := os.Stat(localPath); err != nil {
				localPath = ""
			}
			attachments = append(attachments, domain.Attachment{
				FileName:    attrs.FileName,
				MIMEType:    attrs.MIMEType,
				DownloadURL: attrs.DownloadURL,
				LocalPath:   localPath,
			})
		}
	}
	var creatorID, creatorName string
	if user, ok := includedByID[envelope.Data.Relationships.User.Data.ID]; ok {
		var attrs struct {
			FullName string `json:"full_name"`
			Vanity   string `json:"vanity"`
		}
		_ = json.Unmarshal(user.Attributes, &attrs)
		creatorID = envelope.Data.Relationships.User.Data.ID
		creatorName = firstNonEmpty(attrs.FullName, attrs.Vanity, creatorID)
	}
	if creatorName == "" {
		if campaign, ok := includedByID[envelope.Data.Relationships.Campaign.Data.ID]; ok {
			var attrs struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(campaign.Attributes, &attrs)
			creatorName = attrs.Name
			if creatorID == "" {
				creatorID = campaign.CreatorID
			}
		}
	}
	return domain.NormalizedRelease{
		Provider:          "patreon",
		ProviderReleaseID: envelope.Data.ID,
		URL:               envelope.Data.Attributes.URL,
		Title:             firstNonEmpty(envelope.Data.Attributes.Title, "Patreon Post "+envelope.Data.ID),
		PublishedAt:       publishedAt,
		EditedAt:          editedAt,
		PostType:          firstNonEmpty(envelope.Data.Attributes.PostType, "post"),
		VisibilityState:   visibilityLabel(envelope.Data.Attributes.CurrentUserCanView),
		TextHTML:          textHTML,
		TextPlain:         textPlain,
		Tags:              tags,
		Collections:       collections,
		Attachments:       attachments,
		CreatorID:         creatorID,
		CreatorName:       creatorName,
		SourceType:        "creator_feed",
	}, nil
}

type postEnvelopeIncluded struct {
	Type       string
	Attributes json.RawMessage
	CreatorID  string
}

func extractContent(content string, contentJSON string) (string, string) {
	if strings.TrimSpace(content) != "" {
		return content, stripTags(content)
	}
	if strings.TrimSpace(contentJSON) == "" {
		return "", ""
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(contentJSON), &doc); err != nil {
		return "", ""
	}
	var plainParts []string
	var htmlParts []string
	var walk func(node any)
	walk = func(node any) {
		switch typed := node.(type) {
		case map[string]any:
			nodeType, _ := typed["type"].(string)
			switch nodeType {
			case "paragraph", "heading", "listItem":
				beforePlain := len(plainParts)
				children, _ := typed["content"].([]any)
				for _, child := range children {
					walk(child)
				}
				if len(plainParts) > beforePlain {
					last := strings.Join(plainParts[beforePlain:], "")
					plainParts = append(plainParts[:beforePlain], last, "\n\n")
					htmlParts = append(htmlParts, "<p>"+escapeHTML(last)+"</p>")
				}
				return
			case "text":
				text, _ := typed["text"].(string)
				plainParts = append(plainParts, text)
				return
			default:
				children, _ := typed["content"].([]any)
				for _, child := range children {
					walk(child)
				}
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(doc)
	return strings.Join(htmlParts, "\n"), strings.TrimSpace(strings.Join(plainParts, ""))
}

func parseTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, value)
}

func visibilityLabel(canView bool) string {
	if canView {
		return "visible"
	}
	return "locked"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func stripTags(input string) string {
	replacer := strings.NewReplacer("<p>", "", "</p>", "\n\n", "<br>", "\n", "<br/>", "\n", "<br />", "\n")
	cleaned := replacer.Replace(input)
	for strings.Contains(cleaned, "<") && strings.Contains(cleaned, ">") {
		start := strings.Index(cleaned, "<")
		end := strings.Index(cleaned[start:], ">")
		if start < 0 || end < 0 {
			break
		}
		cleaned = cleaned[:start] + cleaned[start+end+1:]
	}
	return strings.TrimSpace(cleaned)
}

func escapeHTML(input string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&#39;",
	)
	return replacer.Replace(input)
}
