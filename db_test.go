package dman

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestDotfileMarshal(t *testing.T) {
	dot := NewDotfile("abc123", "/Users/alex/.zshrc")

	jdata, err := json.MarshalIndent(dot, "", "  ")
	if err != nil {
		t.Fail()
	}

	var restored Dotfile
	if err := json.Unmarshal(jdata, &restored); err != nil {
		t.Fail()
	}
	fmt.Println(restored.Hash)
}
