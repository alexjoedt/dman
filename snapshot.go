package dman

import (
	"encoding/json"
	"time"
)

// Snapshot represents a point-in-time backup of dotfiles.
type Snapshot struct {
	ID   []byte   `json:"id"`
	Date DateTime `json:"date"`
	Tags []string `json:"tags"`
}

// Dotfile holds the metadata of a single dotfile at the time of a snapshot.
type Dotfile struct {
	ID        []byte   `json:"id"`
	CreatedAt DateTime `json:"date_created"`
	Name      string   `json:"name"`
	Hash      string   `json:"hash"`
}

func NewDotfile(hash string, path string) *Dotfile {
	return &Dotfile{
		ID:   []byte(hash),
		Hash: hash,
		Name: path,
	}
}

// DateTime wraps time.Time with RFC3339 JSON marshaling.
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
