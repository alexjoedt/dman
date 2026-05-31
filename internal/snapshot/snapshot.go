package snapshot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/alexjoedt/blobfs"
	"github.com/alexjoedt/dman/internal/hash"
)

// Index is the top-level index persisted as index.json in the snapshot directory.
type Index struct {
	Snapshots []Meta `json:"snapshots"`
}

// Meta holds lightweight metadata about a single snapshot.
type Meta struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	Message   string    `json:"message,omitempty"`
	FileCount int       `json:"fileCount"`
}

// Manifest lists every file captured in a snapshot.
type Manifest struct {
	ID    string `json:"id"`
	Files []File `json:"files"`
}

// File represents a single file entry in a snapshot manifest.
type File struct {
	Path     string      `json:"path"`
	Checksum string      `json:"checksum"`
	Size     int64       `json:"size"`
	Mode     fs.FileMode `json:"mode"`
}

// Store manages snapshot storage on disk using blobfs for blob deduplication.
type Store struct {
	dir     string
	storage *blobfs.Storage
}

const indexFile = "index.json"

// NewStore creates dir if necessary, initialises a blobfs storage under dir/blobs
// with Zstd compression and read-time integrity verification.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create snapshot dir %s: %w", dir, err)
	}
	storage, err := blobfs.NewStorage(
		filepath.Join(dir, "blobs"),
		blobfs.WithCompression(blobfs.CodecZstd),
		blobfs.WithVerifyOnRead(true),
		blobfs.WithFileMode(0o600),
		blobfs.WithDirMode(0o750),
	)
	if err != nil {
		return nil, fmt.Errorf("init blobfs: %w", err)
	}
	return &Store{dir: dir, storage: storage}, nil
}

func (s *Store) loadIndex() (*Index, error) {
	p := filepath.Join(s.dir, indexFile)
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return &Index{}, nil
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, fmt.Errorf("open index: %w", err)
	}
	defer func() { _ = f.Close() }()
	var idx Index
	if err := json.NewDecoder(f).Decode(&idx); err != nil {
		return nil, fmt.Errorf("decode index: %w", err)
	}
	return &idx, nil
}

func (s *Store) saveIndex(idx *Index) (err error) {
	p := filepath.Join(s.dir, indexFile)
	f, err := os.Create(p)
	if err != nil {
		return fmt.Errorf("write index: %w", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close index: %w", cerr)
		}
	}()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(idx)
}

func (s *Store) manifestPath(id string) string {
	return filepath.Join(s.dir, id+".json")
}

func (s *Store) loadManifest(id string) (*Manifest, error) {
	f, err := os.Open(s.manifestPath(id))
	if err != nil {
		return nil, fmt.Errorf("open manifest %s: %w", id, err)
	}
	defer func() { _ = f.Close() }()
	var m Manifest
	if err := json.NewDecoder(f).Decode(&m); err != nil {
		return nil, fmt.Errorf("decode manifest %s: %w", id, err)
	}
	return &m, nil
}

func (s *Store) saveManifest(m *Manifest) (err error) {
	f, err := os.Create(s.manifestPath(m.ID))
	if err != nil {
		return fmt.Errorf("write manifest %s: %w", m.ID, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close manifest %s: %w", m.ID, cerr)
		}
	}()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(m)
}

// Create takes a point-in-time snapshot of the given absolute file paths.
// homeDir is used to compute home-relative paths stored in the manifest.
// Each file is keyed by its SHA-256 checksum, enabling deduplication across snapshots.
func (s *Store) Create(ctx context.Context, homeDir string, files []string, message string) (Meta, error) {
	id := time.Now().UTC().Format("20060102-150405.000000000")
	manifest := Manifest{ID: id}

	for _, abs := range files {
		checksum, err := hash.GetHash(abs)
		if err != nil {
			return Meta{}, fmt.Errorf("hash %s: %w", abs, err)
		}

		info, err := os.Stat(abs)
		if err != nil {
			return Meta{}, fmt.Errorf("stat %s: %w", abs, err)
		}

		rel, err := filepath.Rel(homeDir, abs)
		if err != nil {
			return Meta{}, fmt.Errorf("rel path %s: %w", abs, err)
		}

		if exists, err := s.storage.Exists(ctx, checksum); err != nil {
			return Meta{}, fmt.Errorf("exists check %s: %w", checksum, err)
		} else if !exists {
			f, err := os.Open(abs)
			if err != nil {
				return Meta{}, fmt.Errorf("open %s: %w", abs, err)
			}
			putErr := s.storage.Put(ctx, checksum, f)
			f.Close()
			if putErr != nil {
				return Meta{}, fmt.Errorf("store %s: %w", abs, putErr)
			}
		}

		manifest.Files = append(manifest.Files, File{
			Path:     rel,
			Checksum: checksum,
			Size:     info.Size(),
			Mode:     info.Mode(),
		})
	}

	if err := s.saveManifest(&manifest); err != nil {
		return Meta{}, err
	}

	meta := Meta{
		ID:        id,
		CreatedAt: time.Now().UTC(),
		Message:   message,
		FileCount: len(manifest.Files),
	}

	idx, err := s.loadIndex()
	if err != nil {
		return Meta{}, err
	}
	idx.Snapshots = append(idx.Snapshots, meta)
	if err := s.saveIndex(idx); err != nil {
		return Meta{}, err
	}

	return meta, nil
}

// List returns all snapshot metadata entries from the index.
func (s *Store) List() ([]Meta, error) {
	idx, err := s.loadIndex()
	if err != nil {
		return nil, err
	}
	return idx.Snapshots, nil
}

// Files returns the file list for the given snapshot ID.
func (s *Store) Files(id string) ([]File, error) {
	m, err := s.loadManifest(id)
	if err != nil {
		return nil, err
	}
	return m.Files, nil
}

// Cat retrieves the blob for the given SHA-256 checksum (or unambiguous prefix) as a ReadCloser.
// The caller is responsible for closing the returned reader.
func (s *Store) Cat(ctx context.Context, checksum string) (io.ReadCloser, error) {
	full, err := s.resolveChecksum(ctx, checksum)
	if err != nil {
		return nil, err
	}
	r, err := s.storage.Get(ctx, full)
	if err != nil {
		return nil, fmt.Errorf("get blob %s: %w", full, err)
	}
	return r, nil
}

// resolveChecksum resolves a full or abbreviated checksum to the canonical full checksum.
// A prefix of at least 4 characters is accepted; returns an error if ambiguous or unmatched.
func (s *Store) resolveChecksum(ctx context.Context, prefix string) (string, error) {
	if len(prefix) == 64 {
		return prefix, nil
	}
	if len(prefix) < 4 {
		return "", fmt.Errorf("checksum prefix too short (minimum 4 characters): %q", prefix)
	}
	var matches []string
	err := s.storage.Walk(ctx, "", func(key string, _ *blobfs.Meta, _ error) error {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			matches = append(matches, key)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walk blobs: %w", err)
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no blob matches prefix %q", prefix)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("ambiguous prefix %q matches %d blobs", prefix, len(matches))
	}
}

// Delete removes a snapshot and reclaims any blobs no longer referenced by any remaining snapshot.
func (s *Store) Delete(ctx context.Context, id string) error {
	manifest, err := s.loadManifest(id)
	if err != nil {
		return err
	}

	idx, err := s.loadIndex()
	if err != nil {
		return err
	}

	stillReferenced := make(map[string]struct{})
	for _, meta := range idx.Snapshots {
		if meta.ID == id {
			continue
		}
		m, err := s.loadManifest(meta.ID)
		if err != nil {
			return fmt.Errorf("load manifest %s: %w", meta.ID, err)
		}
		for _, sf := range m.Files {
			stillReferenced[sf.Checksum] = struct{}{}
		}
	}

	for _, sf := range manifest.Files {
		if _, ok := stillReferenced[sf.Checksum]; ok {
			continue
		}
		if err := s.storage.Delete(ctx, sf.Checksum); err != nil {
			return fmt.Errorf("delete blob %s: %w", sf.Checksum, err)
		}
	}

	if err := os.Remove(s.manifestPath(id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove manifest file: %w", err)
	}

	filtered := idx.Snapshots[:0]
	for _, meta := range idx.Snapshots {
		if meta.ID != id {
			filtered = append(filtered, meta)
		}
	}
	idx.Snapshots = filtered
	if err := s.saveIndex(idx); err != nil {
		return err
	}

	if _, err := s.storage.GC(ctx); err != nil {
		return fmt.Errorf("gc: %w", err)
	}

	return nil
}
