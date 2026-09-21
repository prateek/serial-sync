package sqlite

import (
	"bytes"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"modernc.org/sqlite/vfs"
)

func OpenReadOnly(dsn string) (*Store, error) {
	path := dsn
	if strings.HasPrefix(dsn, "file:") {
		uri, err := url.Parse(dsn)
		if err != nil {
			return nil, err
		}
		path = uri.Path
		if path == "" {
			path = uri.Opaque
		}
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := requireCheckpointed(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	confirmed, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(data, confirmed) {
		return nil, fmt.Errorf("catalog changed during snapshot; stop active runs and retry preview")
	}
	if err := requireCheckpointed(path); err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	name, snapshot, err := vfs.New(catalogSnapshot{data: data, info: info})
	if err != nil {
		return nil, err
	}
	// The snapshot VFS cannot create spill files for large query sorts.
	store, err := Open("file:catalog.db?mode=ro&immutable=1&_pragma=temp_store(MEMORY)&vfs=" + url.QueryEscape(name))
	if err != nil {
		_ = snapshot.Close()
		return nil, err
	}
	store.snapshot = snapshot
	return store, nil
}

func requireCheckpointed(path string) error {
	for _, suffix := range []string{"-wal", "-journal"} {
		if info, err := os.Stat(path + suffix); err == nil && info.Size() > 0 {
			return fmt.Errorf("read-only rebuild requires a checkpointed catalog; stop active serial-sync runs before previewing")
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

type catalogSnapshot struct {
	data []byte
	info fs.FileInfo
}

func (s catalogSnapshot) Open(name string) (fs.File, error) {
	if strings.TrimPrefix(name, "/") != "catalog.db" {
		return nil, fs.ErrNotExist
	}
	return &snapshotFile{Reader: bytes.NewReader(s.data), info: s.info}, nil
}

type snapshotFile struct {
	*bytes.Reader
	info fs.FileInfo
}

func (f *snapshotFile) Close() error { return nil }
func (f *snapshotFile) Stat() (fs.FileInfo, error) {
	return snapshotInfo{FileInfo: f.info, size: f.Size()}, nil
}

type snapshotInfo struct {
	fs.FileInfo
	size int64
}

func (i snapshotInfo) Size() int64 { return i.size }
