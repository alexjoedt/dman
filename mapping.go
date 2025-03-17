package dman

import (
	"fmt"
	"os"
	"path/filepath"
)

type Pair struct {
	src string
	dst string
}

func (p *Pair) String() string {
	return fmt.Sprintf("%s --> %s", p.src, p.dst)
}

func (p *Pair) Apply(dry bool) (bool, error) {
	srcHash, err := getHash(p.src)
	if err != nil {
		return false, fmt.Errorf("apply pair: %w", err)
	}

	var dstHash string
	if isExist(p.dst) {
		dstHash, err = getHash(p.dst)
		if err != nil {
			return false, fmt.Errorf("apply pair: %w", err)
		}
	}

	if srcHash != dstHash {
		if dry {
			return true, nil
		}
		os.MkdirAll(filepath.Dir(p.dst), HomeDir().Mode())
		err := copyFile(p.dst, p.src)
		if err != nil {
			return false, fmt.Errorf("apply pair: %w", err)
		}
		return true, nil
	}

	return false, nil
}

type DotfileMapping struct {
	mapping map[string]string
	errors  []error
}

func (dm *DotfileMapping) Set(src string, dst string) {
	dm.mapping[src] = dst
}

func (dm *DotfileMapping) Apply() error {
	for d, o := range dm.mapping {
		p := Pair{
			src: d,
			dst: o,
		}

		applied, err := p.Apply(false)
		if err != nil {
			dm.errors = append(dm.errors, err)
		}
		if applied {
			fmt.Println(p.String())
		}
	}

	return nil
}

func (dm *DotfileMapping) ApplyDry() map[string]string {
	m := make(map[string]string)
	for d, o := range dm.mapping {
		p := Pair{
			src: d,
			dst: o,
		}

		applied, err := p.Apply(true)
		if err != nil {
			dm.errors = append(dm.errors, err)
		}
		if applied {
			m[d] = o
		}
	}

	return m
}

func (dm *DotfileMapping) GetFilesFromHome() []string {
	homeFiles := make([]string, 0, len(dm.mapping))
	for _, hf := range dm.mapping {
		homeFiles = append(homeFiles, hf)
	}
	return homeFiles

}

func (dm *DotfileMapping) Len() int {
	return len(dm.mapping)
}
