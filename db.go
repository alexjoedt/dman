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
	"time"

	bolt "go.etcd.io/bbolt"
)

var (
	bucketSnapshots = []byte("snapshots")
	bucketDotfiles  = []byte("dotfiles")
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

		for _, f := range files {
			if !isExist(f) {
				// origin file is not present in $HOME
				continue
			}

			df, err := NewDotfile(f, s.ID)
			if err != nil {
				return fmt.Errorf("create dotfile for snapshot: %w", err)
			}

			dfJSON, err := json.Marshal(&df)
			if err != nil {
				return fmt.Errorf("marshal dotfile: %w", err)
			}

			err = dotfiles.Put(df.ID, dfJSON)
			if err != nil {
				return fmt.Errorf("put dotfile: %w", err)
			}
			df = nil
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

	return snapshots, err
}

func listDotfiles(db *bolt.DB, snapshotID []byte) ([]*Dotfile, error) {
	var dotfiles []*Dotfile

	err := db.View(func(tx *bolt.Tx) error {
		dotfileBucket := tx.Bucket(bucketDotfiles)
		if dotfileBucket == nil {
			return ErrNoSnapshotBucket
		}

		dotfileBucket.ForEach(func(k, v []byte) error {
			var dotfile Dotfile
			err := json.Unmarshal(v, &dotfile)
			if err != nil {
				return err
			}

			if bytes.Equal(dotfile.SnapshotID[:12], snapshotID[:12]) {
				dotfiles = append(dotfiles, &dotfile)
			}

			return nil
		})

		return nil
	})

	return dotfiles, err
}

type Snapshot struct {
	ID   []byte   `json:"id"`
	Date DateTime `json:"date"`
	Tags []string `json:"tags"`
}

type Dotfile struct {
	ID         []byte `json:"id"`
	SnapshotID []byte `json:"snapshot_id"`
	Name       string `json:"name"`
	Data       []byte `json:"data"`
}

func NewDotfile(f string, snapshotID []byte) (*Dotfile, error) {
	hasher := sha256.New()
	hashFile(f, hasher)
	id := hex.EncodeToString(hasher.Sum(nil))

	data, err := os.ReadFile(f)
	if err != nil {
		return nil, fmt.Errorf("read dotfile data: %w", err)
	}

	return &Dotfile{
		ID:         []byte(id),
		SnapshotID: snapshotID,
		Name:       f,
		Data:       data,
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
