package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
)

func workspaceFile(root, originalRoot, path string) (string, error) {
	if path == "" {
		return "", nil
	}
	if filepath.IsAbs(path) {
		if originalRoot == "" {
			return "", fmt.Errorf("workspace reference must be relative: %s", path)
		}
		rel, err := filepath.Rel(originalRoot, path)
		if err != nil {
			return "", err
		}
		path = rel
	}
	if !filepath.IsLocal(path) {
		return "", fmt.Errorf("workspace reference escapes capture: %s", path)
	}
	return filepath.Join(root, path), nil
}

func relocateManifest(root string, manifest dumpManifest) (dumpManifest, error) {
	if manifest.Version < 1 || manifest.Version > 3 || manifest.SourcesFile == "" || manifest.SeriesFile == "" {
		return manifest, fmt.Errorf("%s is not a supported dump workspace", root)
	}
	if manifest.Version < 3 && filepath.IsAbs(manifest.SourcesFile) {
		manifest.originalRoot = filepath.Dir(manifest.SourcesFile)
	}
	for _, path := range manifest.paths() {
		resolved, err := workspaceFile(root, manifest.originalRoot, *path)
		if err != nil {
			return manifest, err
		}
		*path = resolved
	}
	return manifest, nil
}

func (manifest *dumpManifest) paths() []*string {
	paths := []*string{&manifest.SourcesFile, &manifest.SeriesFile}
	for i := range manifest.Creators {
		c := &manifest.Creators[i]
		paths = append(paths, &c.Directory, &c.SourceFile, &c.PostsFile, &c.RawPostsDir, &c.AttachmentsDir)
	}
	return paths
}

func loadWorkspacePosts(root, originalRoot, path string) ([]domain.NormalizedRelease, error) {
	releases, err := loadDumpPosts(path)
	if err != nil {
		return nil, err
	}
	for i := range releases {
		id := releases[i].ProviderReleaseID
		if !filepath.IsLocal(id) || filepath.Base(id) != id || id == "." {
			return nil, fmt.Errorf("invalid captured release ID %q", id)
		}
		for j := range releases[i].Attachments {
			attachment := &releases[i].Attachments[j]
			attachment.LocalPath, err = workspaceFile(root, originalRoot, attachment.LocalPath)
			if err != nil {
				return nil, fmt.Errorf("post %s attachment: %w", releases[i].ProviderReleaseID, err)
			}
		}
		for _, asset := range releases[i].Enrichment.Assets() {
			asset.Path, err = workspaceFile(root, originalRoot, asset.Path)
			if err != nil {
				return nil, fmt.Errorf("post %s metadata: %w", id, err)
			}
		}
	}
	return releases, nil
}

type dumpCapture struct {
	root      string
	directory string
	installed bool
}

func beginDumpCapture(root string) (*dumpCapture, error) {
	info, err := os.Lstat(root)
	switch {
	case os.IsNotExist(err):
		if err := os.MkdirAll(root, 0755); err != nil {
			return nil, err
		}
		if err := createAuthoredFile(filepath.Join(root, ".serial-sync-workspace"), []byte("serial-sync dump workspace v1\n")); err != nil {
			_ = os.Remove(root)
			return nil, err
		}
	case err != nil:
		return nil, err
	case !info.IsDir():
		return nil, fmt.Errorf("workspace %s must be a directory, not a file or symlink", root)
	default:
		marker, _ := os.ReadFile(filepath.Join(root, ".serial-sync-workspace"))
		if string(marker) != "serial-sync dump workspace v1\n" {
			if _, err := loadDumpManifest(filepath.Join(root, "manifest.json")); err != nil {
				return nil, fmt.Errorf("refusing to refresh %s: not a serial-sync dump workspace", root)
			}
		}
	}
	captures := filepath.Join(root, "captures")
	if err := os.MkdirAll(captures, 0755); err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp(captures, "generation-")
	if err != nil {
		return nil, err
	}
	return &dumpCapture{root: root, directory: directory}, nil
}

func (capture *dumpCapture) close() {
	if !capture.installed {
		_ = os.RemoveAll(capture.directory)
	}
}

func (capture *dumpCapture) install(ctx context.Context, manifest dumpManifest, sources []config.SourceConfig) error {
	manifest.Version = 3
	manifest.SourcesFile = filepath.Join(capture.directory, "sources.toml")
	manifest.SeriesFile = filepath.Join(capture.root, "series.toml")
	if err := writeTOMLFile(manifest.SourcesFile, struct {
		Sources []config.SourceConfig `toml:"sources"`
	}{sources}); err != nil {
		return err
	}
	if err := createAuthoredFile(manifest.SeriesFile, []byte(defaultSeriesScaffold)); err != nil {
		return err
	}
	if err := createAuthoredFile(filepath.Join(capture.root, "README.md"), []byte(workspaceReadme(capture.root))); err != nil {
		return err
	}
	manifest.Creators = append([]SourceDumpCreator(nil), manifest.Creators...)
	for _, path := range manifest.paths() {
		rel, err := filepath.Rel(capture.root, *path)
		if err != nil || !filepath.IsLocal(rel) {
			return fmt.Errorf("capture path is outside workspace: %s", *path)
		}
		*path = filepath.ToSlash(rel)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(capture.root, ".manifest-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := syncCapture(capture.directory); err != nil {
		return err
	}
	if err := syncPath(filepath.Dir(capture.directory)); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), filepath.Join(capture.root, "manifest.json")); err != nil {
		return err
	}
	capture.installed = true
	return syncPath(capture.root)
}

func createAuthoredFile(path string, data []byte) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("authored path %s must be a regular file", path)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".authored-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0644); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	// Link publishes the complete file without replacing an operator's concurrent edit.
	if err := os.Link(file.Name(), path); err != nil && !os.IsExist(err) {
		return err
	}
	return syncPath(filepath.Dir(path))
}

func syncCapture(root string) error {
	var directories []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			directories = append(directories, path)
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("capture contains non-regular file: %s", path)
		}
		return syncPath(path)
	})
	if err != nil {
		return err
	}
	for i := len(directories) - 1; i >= 0; i-- {
		if err := syncPath(directories[i]); err != nil {
			return err
		}
	}
	return nil
}

func syncPath(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}
