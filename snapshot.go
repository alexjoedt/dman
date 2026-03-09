package dman

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Snapshot represents a point-in-time backup of dotfiles.
type Snapshot struct {
	ID   []byte   `json:"id"`
	Date DateTime `json:"date"`
	Tags []string `json:"tags"`
}

// Dotfile holds the content of a single dotfile at the time of a snapshot.
type Dotfile struct {
	ID        []byte   `json:"id"`
	CreatedAt DateTime `json:"date_created"`
	Name      string   `json:"name"`
	Data      []byte   `json:"data"`
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
