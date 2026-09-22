package config

import (
	"fmt"
	"net/url"
	"path/filepath"
)

type AuthorProfileConfig struct {
	ID        string      `toml:"id"`
	Name      string      `toml:"name"`
	Biography string      `toml:"biography"`
	URL       string      `toml:"url"`
	Portrait  AssetConfig `toml:"portrait"`
}

type AssetConfig struct {
	Path      string `toml:"path"`
	SourceURL string `toml:"source_url"`
}

type PublicationConfig struct {
	Description string      `toml:"description"`
	Language    string      `toml:"language"`
	Cover       AssetConfig `toml:"cover"`
	Links       []string    `toml:"links"`
}

func (c *Config) resolveMetadataPaths(base string) {
	resolve := func(asset *AssetConfig) {
		if asset.Path != "" && !filepath.IsAbs(asset.Path) {
			asset.Path = filepath.Join(base, asset.Path)
		}
	}
	for i := range c.AuthorProfiles {
		resolve(&c.AuthorProfiles[i].Portrait)
	}
	for i := range c.Series {
		resolve(&c.Series[i].Metadata.Cover)
		for j := range c.Series[i].Books {
			resolve(&c.Series[i].Books[j].Metadata.Cover)
		}
	}
}

func (c *Config) validatePublicationMetadata() error {
	profiles := map[string]bool{}
	validURL := func(value string) bool {
		if value == "" {
			return true
		}
		u, err := url.Parse(value)
		return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil
	}
	for _, profile := range c.AuthorProfiles {
		if profile.ID == "" || profiles[profile.ID] {
			return fmt.Errorf("author profile id must be nonempty and unique: %q", profile.ID)
		}
		profiles[profile.ID] = true
		if !validURL(profile.URL) || !validURL(profile.Portrait.SourceURL) {
			return fmt.Errorf("author profile %q requires HTTP(S) source URLs", profile.ID)
		}
	}
	for _, source := range c.Sources {
		if source.AuthorProfile != "" && !profiles[source.AuthorProfile] {
			return fmt.Errorf("source %q references unknown author profile %q", source.ID, source.AuthorProfile)
		}
	}
	for _, series := range c.Series {
		for _, id := range series.AuthorProfiles {
			if !profiles[id] {
				return fmt.Errorf("series %q references unknown author profile %q", series.ID, id)
			}
		}
		metadata := []PublicationConfig{series.Metadata}
		for _, book := range series.Books {
			metadata = append(metadata, book.Metadata)
		}
		for _, entry := range metadata {
			for _, link := range append(append([]string{}, entry.Links...), entry.Cover.SourceURL) {
				if !validURL(link) {
					return fmt.Errorf("series %q requires HTTP(S) metadata links", series.ID)
				}
			}
		}
	}
	return nil
}
