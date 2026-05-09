package dman

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/alexjoedt/blobfs"
	bolt "go.etcd.io/bbolt"
)

var (
	bucketSnapshots        = []byte("snapshots")
	bucketDotfiles         = []byte("dotfiles")
	bucketSnapshotDotfiles = []byte("snapshots-dotfiles")
)

var (
	ErrNoSnapshotBucket = errors.New("no snapshots")
	ErrNoDotfileBucket  = errors.New("no dotfiles")
)

func (a *App) openDB() (*bolt.DB, error) {
	db, err := bolt.Open(a.DBPath, a.HomeMode, nil)
	if err != nil {
		return nil, fmt.Errorf("creating or open db: %w", err)
	}

	return db, nil
}

func createSnapshot(ctx context.Context, db *bolt.DB, blobs *blobfs.Storage, files []string, tags ...string) error {
	h, err := createHash(files...)
	if err != nil {
		return fmt.Errorf("create snapshot: %w", err)
	}

	s := Snapshot{
		ID:   h,
		Date: DateTime{Time: time.Now()},
		Tags: tags,
	}

	// Phase 1: write blobs before opening the DB transaction.
	// Blob writes are idempotent; orphaned blobs are cleaned up by `dman gc`.
	type dotfileEntry struct {
		hash string
		path string
	}
	var entries []dotfileEntry

	for _, f := range files {
		if !isExist(f) {
			continue
		}

		hash, err := getHash(f)
		if err != nil {
			return fmt.Errorf("hash dotfile %s: %w", f, err)
		}

		exists, err := blobs.Exists(ctx, hash)
		if err != nil {
			return fmt.Errorf("check blob existence for %s: %w", f, err)
		}
		if !exists {
			file, err := os.Open(f)
			if err != nil {
				return fmt.Errorf("open dotfile %s: %w", f, err)
			}
			putErr := blobs.Put(ctx, hash, file)
			file.Close()
			if putErr != nil {
				return fmt.Errorf("store blob for %s: %w", f, putErr)
			}
		}

		entries = append(entries, dotfileEntry{hash: hash, path: f})
	}

	// Phase 2: commit snapshot metadata to BoltDB — no file I/O inside the tx.
	err = db.Update(func(tx *bolt.Tx) error {
		snapshots, err := tx.CreateBucketIfNotExists(bucketSnapshots)
		if err != nil {
			return fmt.Errorf("create snapshot bucket: %w", err)
		}

		sdata, err := json.Marshal(&s)
		if err != nil {
			return fmt.Errorf("marshal snapshot: %w", err)
		}

		if err = snapshots.Put(s.ID, sdata); err != nil {
			return fmt.Errorf("put snapshot: %w", err)
		}

		dotfiles, err := tx.CreateBucketIfNotExists(bucketDotfiles)
		if err != nil {
			return fmt.Errorf("create dotfiles bucket: %w", err)
		}

		relations, err := tx.CreateBucketIfNotExists(bucketSnapshotDotfiles)
		if err != nil {
			return fmt.Errorf("create snapshot-dotfiles bucket: %w", err)
		}

		for _, e := range entries {
			if existing := dotfiles.Get([]byte(e.hash)); existing == nil {
				df := NewDotfile(e.hash, e.path)
				df.CreatedAt = s.Date

				dfJSON, err := json.Marshal(df)
				if err != nil {
					return fmt.Errorf("marshal dotfile: %w", err)
				}

				if err = dotfiles.Put([]byte(e.hash), dfJSON); err != nil {
					return fmt.Errorf("put dotfile: %w", err)
				}
			}

			if err = relations.Put([]byte(fmt.Sprintf("%s_%s", s.ID, e.hash)), []byte(e.hash)); err != nil {
				return fmt.Errorf("put snapshot-dotfile mapping: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("create snapshot: %w", err)
	}

	return nil
}

func listSnapshots(db *bolt.DB) ([]*Snapshot, error) {
	var snapshots []*Snapshot

	err := db.View(func(tx *bolt.Tx) error {
		snapshotBucket := tx.Bucket(bucketSnapshots)
		if snapshotBucket == nil {
			return ErrNoSnapshotBucket
		}

		snapshotBucket.ForEach(func(k, v []byte) error {
			var snapshot Snapshot
			err := json.Unmarshal(v, &snapshot)
			if err != nil {
				return err
			}
			snapshots = append(snapshots, &snapshot)
			return nil
		})

		return nil
	})

	slices.SortFunc(snapshots, func(a *Snapshot, b *Snapshot) int {
		if a.Date.After(b.Date.Time) {
			return 1
		}
		return -1
	})

	return snapshots, err
}

func listDotfilesBySnapshot(db *bolt.DB, snapshotID []byte) ([]*Dotfile, error) {
	var dotfiles []*Dotfile

	err := db.View(func(tx *bolt.Tx) error {
		dotfileBucket := tx.Bucket(bucketDotfiles)
		if dotfileBucket == nil {
			return ErrNoSnapshotBucket
		}

		relationBucket := tx.Bucket(bucketSnapshotDotfiles)
		if relationBucket == nil {
			return ErrNoDotfileBucket
		}

		var dotfileIDs [][]byte

		relationBucket.ForEach(func(k, v []byte) error {
			parts := bytes.Split(k, []byte("_"))
			snapKey := parts[0]
			if len(snapKey) >= len(snapshotID) && bytes.Equal(snapKey[:len(snapshotID)], snapshotID) {
				dotfileIDs = append(dotfileIDs, v)
			}

			return nil
		})

		for _, id := range dotfileIDs {
			data := dotfileBucket.Get(id)
			if data == nil {
				continue
			}

			var dotfile Dotfile
			err := json.Unmarshal(data, &dotfile)
			if err != nil {
				return err
			}

			dotfiles = append(dotfiles, &dotfile)
		}

		return nil
	})

	return dotfiles, err
}

func listAllDotfiles(db *bolt.DB) ([]*Dotfile, error) {
	var dotfiles []*Dotfile

	err := db.View(func(tx *bolt.Tx) error {
		dotfileBucket := tx.Bucket(bucketDotfiles)
		if dotfileBucket == nil {
			return ErrNoSnapshotBucket
		}

		relationBucket := tx.Bucket(bucketSnapshotDotfiles)
		if relationBucket == nil {
			return ErrNoDotfileBucket
		}

		dotfileBucket.ForEach(func(k, v []byte) error {
			var dotfile Dotfile
			err := json.Unmarshal(v, &dotfile)
			if err != nil {
				return err
			}
			dotfiles = append(dotfiles, &dotfile)
			return nil
		})
		return nil
	})

	return dotfiles, err
}

func getDotfileByID(db *bolt.DB, id string) (*Dotfile, error) {
	var dotfile Dotfile
	err := db.View(func(tx *bolt.Tx) error {
		dotfiles := tx.Bucket(bucketDotfiles)
		if dotfiles == nil {
			return fmt.Errorf("no dotfile bucket")
		}

		dotfiles.ForEach(func(k, v []byte) error {
			dotfileID := []byte(id)
			if len(k) >= len(dotfileID) && bytes.Equal(k[:len(dotfileID)], dotfileID) {
				if err := json.Unmarshal(v, &dotfile); err != nil {
					return fmt.Errorf("unmarshal dotfile: %w", err)
				}
			}
			return nil
		})
		return nil
	})

	return &dotfile, err
}

// legacyDotfile is used only during migration to read old records that still carry Data.
type legacyDotfile struct {
	ID        []byte   `json:"id"`
	CreatedAt DateTime `json:"date_created"`
	Name      string   `json:"name"`
	Hash      string   `json:"hash"`
	Data      []byte   `json:"data"`
}

// checkMigrationNeeded returns ErrMigrationRequired if the dotfiles bucket
// contains any record that still has legacy Data content (Hash == "").
func checkMigrationNeeded(db *bolt.DB) error {
	var needed bool
	err := db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketDotfiles)
		if b == nil {
			return nil // empty DB — no migration needed
		}
		return b.ForEach(func(_, v []byte) error {
			var df legacyDotfile
			if err := json.Unmarshal(v, &df); err != nil {
				return nil // skip unreadable records
			}
			if df.Hash == "" && len(df.Data) > 0 {
				needed = true
				return fmt.Errorf("stop") // break iteration
			}
			return nil
		})
	})
	if err != nil && err.Error() != "stop" {
		return err
	}
	if needed {
		return ErrMigrationRequired
	}
	return nil
}

// listAllLegacyDotfiles returns all dotfiles including legacy Data content.
// Used only by the migrate command.
func listAllLegacyDotfiles(db *bolt.DB) ([]*legacyDotfile, error) {
	var dotfiles []*legacyDotfile
	err := db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketDotfiles)
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, v []byte) error {
			var df legacyDotfile
			if err := json.Unmarshal(v, &df); err != nil {
				return err
			}
			dotfiles = append(dotfiles, &df)
			return nil
		})
	})
	return dotfiles, err
}

// setDotfileHash updates a single dotfile record: sets Hash and clears Data.
func setDotfileHash(db *bolt.DB, id []byte, hash string) error {
	return db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketDotfiles)
		if b == nil {
			return fmt.Errorf("no dotfile bucket")
		}
		v := b.Get(id)
		if v == nil {
			return fmt.Errorf("dotfile %x not found", id)
		}
		var df legacyDotfile
		if err := json.Unmarshal(v, &df); err != nil {
			return fmt.Errorf("unmarshal dotfile: %w", err)
		}
		df.Hash = hash
		df.Data = nil
		updated, err := json.Marshal(&df)
		if err != nil {
			return fmt.Errorf("marshal dotfile: %w", err)
		}
		return b.Put(id, updated)
	})
}
