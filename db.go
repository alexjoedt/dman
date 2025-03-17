package dman

import (
	"time"

	bolt "go.etcd.io/bbolt"
)

type Store struct {
	db *bolt.DB
}

type Snapshopt struct {
	Hash []byte   `json:"hash"`
	Date DateTime `json:"date"`
}

type Dotfile struct {
	Name string `json:"name"`
	Hash []byte `json:"hash"`
	Data []byte `json:"data"`
}

type DateTime struct {
	time.Time
}
