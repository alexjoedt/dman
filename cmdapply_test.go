package dman

import (
	"fmt"
	"testing"
)

func TestApply(t *testing.T) {
	m := make(map[string]string)
	apply(RepoDir(), m)
	for k := range m {
		fmt.Println(k, m[k])
	}
}
