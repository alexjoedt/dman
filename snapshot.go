package dman

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
)

// SnapshotIndex is the top-level index persisted as index.json in the snapshot directory.
type SnapshotIndex struct {
	Snapshots []SnapshotMeta `json:"snapshots"`
}

// SnapshotMeta holds lightweight metadata about a single snapshot stored in the index.
type SnapshotMeta struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	Message   string    `json:"message,omitempty"`
	FileCount int       `json:"fileCount"`
}

// SnapshotManifest lists every file captured in a snapshot.
// It is persisted as <id>.json alongside index.json.
type SnapshotManifest struct {
	ID    string         `json:"id"`
	Files []SnapshotFile `json:"files"`
}

// SnapshotFile represents a single file entry in a snapshot manifest.
// The Checksum is the SHA-256 hex of the file content and serves as the blobfs key.
type SnapshotFile struct {
	Path     string      `json:"path"`     // home-relative, e.g. ".zshrc"
	Checksum string      `json:"checksum"` // SHA-256 hex — also the blobfs key
	Size     int64       `json:"size"`
	Mode     fs.FileMode `json:"mode"`
}

// SnapshotStore manages snapshot storage on disk using blobfs for blob deduplication.
type SnapshotStore struct {
	dir     string           // root snapshot directory
	storage *blobfs.Storage  // content-addressable blob store
}

const indexFile = "index.json"

// newSnapshotStore creates (if necessary) dir, initialises a blobfs storage under
// dir/blobs with Zstd compression and read-time integrity verification, and returns
// a ready-to-use SnapshotStore.
func newSnapshotStore(dir string) (*SnapshotStore, error) {
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
	return &SnapshotStore{dir: dir, storage: storage}, nil
}

// --- index operations ---

func (s *SnapshotStore) loadIndex() (*SnapshotIndex, error) {
	p := filepath.Join(s.dir, indexFile)
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return &SnapshotIndex{}, nil
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, fmt.Errorf("open index: %w", err)
	}
	defer f.Close()
	var idx SnapshotIndex
	if err := json.NewDecoder(f).Decode(&idx); err != nil {
		return nil, fmt.Errorf("decode index: %w", err)
	}
	return &idx, nil
}

func (s *SnapshotStore) saveIndex(idx *SnapshotIndex) error {
	p := filepath.Join(s.dir, indexFile)
	f, err := os.Create(p)
	if err != nil {
		return fmt.Errorf("write index: %w", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(idx)
}

// --- manifest operations ---

func (s *SnapshotStore) manifestPath(id string) string {
	return filepath.Join(s.dir, id+".json")
}

func (s *SnapshotStore) loadManifest(id string) (*SnapshotManifest, error) {
	f, err := os.Open(s.manifestPath(id))
	if err != nil {
		return nil, fmt.Errorf("open manifest %s: %w", id, err)
	}
	defer f.Close()
	var m SnapshotManifest
	if err := json.NewDecoder(f).Decode(&m); err != nil {
		return nil, fmt.Errorf("decode manifest %s: %w", id, err)
	}
	return &m, nil
}

func (s *SnapshotStore) saveManifest(m *SnapshotManifest) error {
	f, err := os.Create(s.manifestPath(m.ID))
	if err != nil {
		return fmt.Errorf("write manifest %s: %w", m.ID, err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(m)
}

// --- public API ---

// Create takes a point-in-time snapshot of the given absolute file paths.
// homeDir is used to compute home-relative paths stored in the manifest.
// Each file is keyed in blobfs by its SHA-256 checksum, enabling automatic
// deduplication across snapshots.
func (s *SnapshotStore) Create(ctx context.Context, homeDir string, files []string, message string) (SnapshotMeta, error) {
	id := time.Now().UTC().Format("20060102-150405.000000000")
	manifest := SnapshotManifest{ID: id}

	for _, abs := range files {
		checksum, err := getHash(abs)
		if err != nil {
			return SnapshotMeta{}, fmt.Errorf("hash %s: %w", abs, err)
		}

		info, err := os.Stat(abs)
		if err != nil {
			return SnapshotMeta{}, fmt.Errorf("stat %s: %w", abs, err)
		}

		rel, err := filepath.Rel(homeDir, abs)
		if err != nil {
			return SnapshotMeta{}, fmt.Errorf("rel path %s: %w", abs, err)
		}

		// Only store blob if not already present (blobfs deduplicates by content,
		// but we also skip Put when the key already exists to avoid redundant I/O).
		if exists, err := s.storage.Exists(ctx, checksum); err != nil {
			return SnapshotMeta{}, fmt.Errorf("exists check %s: %w", checksum, err)
		} else if !exists {
			f, err := os.Open(abs)
			if err != nil {
				return SnapshotMeta{}, fmt.Errorf("open %s: %w", abs, err)
			}
			putErr := s.storage.Put(ctx, checksum, f)
			f.Close()
			if putErr != nil {
				return SnapshotMeta{}, fmt.Errorf("store %s: %w", abs, putErr)
			}
		}

		manifest.Files = append(manifest.Files, SnapshotFile{
			Path:     rel,
			Checksum: checksum,
			Size:     info.Size(),
			Mode:     info.Mode(),
		})
	}

	if err := s.saveManifest(&manifest); err != nil {
		return SnapshotMeta{}, err
	}

	meta := SnapshotMeta{
		ID:        id,
		CreatedAt: time.Now().UTC(),
		Message:   message,
		FileCount: len(manifest.Files),
	}

	idx, err := s.loadIndex()
	if err != nil {
		return SnapshotMeta{}, err
	}
	idx.Snapshots = append(idx.Snapshots, meta)
	if err := s.saveIndex(idx); err != nil {
		return SnapshotMeta{}, err
	}

	return meta, nil
}

// List returns all snapshot metadata entries from the index.
func (s *SnapshotStore) List() ([]SnapshotMeta, error) {
	idx, err := s.loadIndex()
	if err != nil {
		return nil, err
	}
	return idx.Snapshots, nil
}

// Files returns the file list for the given snapshot ID.
func (s *SnapshotStore) Files(id string) ([]SnapshotFile, error) {
	m, err := s.loadManifest(id)
	if err != nil {
		return nil, err
	}
	return m.Files, nil
}

// Cat retrieves the blob for the given SHA-256 checksum as a ReadCloser.
// The caller is responsible for closing the returned reader.
func (s *SnapshotStore) Cat(ctx context.Context, checksum string) (io.ReadCloser, error) {
	r, err := s.storage.Get(ctx, checksum)
	if err != nil {
		return nil, fmt.Errorf("get blob %s: %w", checksum, err)
	}
	return r, nil
}

// Delete removes a snapshot and reclaims any blobs that are no longer referenced
// by any remaining snapshot.
func (s *SnapshotStore) Delete(ctx context.Context, id string) error {
	manifest, err := s.loadManifest(id)
	if err != nil {
		return err
	}

	idx, err := s.loadIndex()
	if err != nil {
		return err
	}

	// Build the set of checksums still referenced by snapshots other than id.
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

	// Delete blobs only when no other snapshot references the same checksum.
	for _, sf := range manifest.Files {
		if _, ok := stillReferenced[sf.Checksum]; ok {
			continue
		}
		if err := s.storage.Delete(ctx, sf.Checksum); err != nil {
			return fmt.Errorf("delete blob %s: %w", sf.Checksum, err)
		}
	}

	// Remove manifest file.
	if err := os.Remove(s.manifestPath(id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove manifest file: %w", err)
	}

	// Remove entry from index.
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

	// Reclaim orphaned objects from the blobfs object store.
	if _, err := s.storage.GC(ctx); err != nil {
		return fmt.Errorf("gc: %w", err)
	}

	return nil
}
