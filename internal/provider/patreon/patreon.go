package patreon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

var creatorPattern = regexp.MustCompile(`^https?://(?:www\.)?patreon\.com/(?:(?:c|cw)/)?[^/?#]+/posts`)

type Client struct{}

func New() *Client {
	return &Client{}
}

func (c *Client) Name() string {
	return "patreon"
}

func (c *Client) ValidateSource(source config.SourceConfig) error {
	if !creatorPattern.MatchString(source.URL) {
		return fmt.Errorf("patreon source %q must look like a creator posts feed URL", source.ID)
	}
	if source.FixtureDir == "" {
		return errors.New("live Patreon auth/discovery is not implemented yet; set fixture_dir for the current MVP")
	}
	return nil
}

func (c *Client) ListReleases(ctx context.Context, auth config.AuthProfile, source config.SourceConfig) ([]provider.ReleaseDocument, domain.AuthState, error) {
	if err := c.ValidateSource(source); err != nil {
		return nil, domain.AuthStateReauthRequired, err
	}
	postFiles, err := filepath.Glob(filepath.Join(source.FixtureDir, "posts", "*.json"))
	if err != nil {
		return nil, domain.AuthStateReauthRequired, err
	}
	sort.Strings(postFiles)
	docs := make([]provider.ReleaseDocument, 0, len(postFiles))
	for _, postPath := range postFiles {
		select {
		case <-ctx.Done():
			return nil, domain.AuthStateReauthRequired, ctx.Err()
		default:
		}
		raw, err := os.ReadFile(postPath)
		if err != nil {
			return nil, domain.AuthStateReauthRequired, err
		}
		norm, err := parsePost(raw, source.FixtureDir)
		if err != nil {
			return nil, domain.AuthStateReauthRequired, fmt.Errorf("parse %s: %w", postPath, err)
		}
		docs = append(docs, provider.ReleaseDocument{
			Normalized: norm,
			RawJSON:    append(json.RawMessage(nil), raw...),
		})
	}
	provider.SortReleaseDocuments(docs)
	return docs, domain.AuthStateAuthenticated, nil
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
