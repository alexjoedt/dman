package dman

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"slices"
	"time"

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

func openDB() (*bolt.DB, error) {
	db, err := bolt.Open(DatabasePath(), HomeDir().Mode(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating or open db: %w", err)
	}

	return db, nil
}

func createSnapshot(db *bolt.DB, files []string, tags ...string) error {
	h, err := createHash(files...)
	if err != nil {
		return fmt.Errorf("create snapshot: %w", err)
	}

	s := Snapshot{
		ID:   h,
		Date: DateTime{Time: time.Now()},
		Tags: tags,
	}

	err = db.Update(func(tx *bolt.Tx) error {
		snapshots, err := tx.CreateBucketIfNotExists(bucketSnapshots)
		if err != nil {
			return fmt.Errorf("create snapshot bucket: %w", err)
		}

		sdata, err := json.Marshal(&s)
		if err != nil {
			return fmt.Errorf("marshal snapshot: %w", err)
		}

		err = snapshots.Put(s.ID, sdata)
		if err != nil {
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

		for _, f := range files {
			if !isExist(f) {
				continue
			}

			hash, err := getHash(f)
			if err != nil {
				return fmt.Errorf("hash dotfile: %w", err)
			}

			if existing := dotfiles.Get([]byte(hash)); existing == nil {
				df, err := NewDotfile(hash, f)
				if err != nil {
					return fmt.Errorf("create dotfile for snapshot: %w", err)
				}

				df.CreatedAt = s.Date

				dfJSON, err := json.Marshal(&df)
				if err != nil {
					return fmt.Errorf("marshal dotfile: %w", err)
				}

				err = dotfiles.Put([]byte(hash), dfJSON)
				if err != nil {
					return fmt.Errorf("put dotfile: %w", err)
				}
			}

			err = relations.Put([]byte(fmt.Sprintf("%s_%s", s.ID, hash)), []byte(hash))
			if err != nil {
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
				dotfileIDs = append(dotfileIDs, v) // `v` is the id of the Dotfile
			}

			return nil
		})

		for _, id := range dotfileIDs {
			data := dotfileBucket.Get(id)
			if data == nil {
				// TODO: log warning in verbose mode
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

type Snapshot struct {
	ID   []byte   `json:"id"`
	Date DateTime `json:"date"`
	Tags []string `json:"tags"`
}

type Dotfile struct {
	ID         []byte   `json:"id"`
	CreatedAt  DateTime `json:"date_created"`
	Name       string   `json:"name"`
	Data       []byte   `json:"data"`
}

func NewDotfile(id string, f string) (*Dotfile, error) {
	data, err := os.ReadFile(f)
	if err != nil {
		return nil, fmt.Errorf("read dotfile data: %w", err)
	}

	return &Dotfile{
		ID:   []byte(id),
		Name: f,
		Data: data,
	}, nil
}

type DateTime struct {
	time.Time
}

func (dt DateTime) String() string {
	return dt.Format(time.RFC3339)
}

func (dt DateTime) MarshalJSON() ([]byte, error) {
	return json.Marshal(dt.Format(time.RFC3339))
}

func (dt *DateTime) UnmarshalJSON(b []byte) error {
	var str string
	if err := json.Unmarshal(b, &str); err != nil {
		return err
	}

	t, err := time.Parse(time.RFC3339, str)
	if err != nil {
		return err
	}

	dt.Time = t
	return nil
}

func createHash(files ...string) ([]byte, error) {
	hasher := sha256.New()

	for _, file := range files {
		if !isExist(file) {
			continue
		}
		err := hashFile(file, hasher)
		if err != nil {
			return nil, fmt.Errorf("create hash: %w", err)
		}
	}
	return []byte(hex.EncodeToString(hasher.Sum(nil))), nil
}

func hashFile(filename string, hasher hash.Hash) error {
	f, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("hash file '%s': %w", filename, err)
	}
	defer f.Close()

	_, err = io.Copy(hasher, f)
	if err != nil {
		return fmt.Errorf("copy to hasher: %w", err)
	}
	return err
}

func getHash(f string) (string, error) {
	hasher := sha256.New()
	err := hashFile(f, hasher)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}
