package dman

import (
	"encoding/json"
	"testing"
)

func TestDotfileMarshal(t *testing.T) {
	dot := NewDotfile("abc123", "/Users/alex/.zshrc")

	jdata, err := json.MarshalIndent(dot, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var restored Dotfile
	if err := json.Unmarshal(jdata, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if restored.Hash != dot.Hash {
		t.Errorf("hash: got %q, want %q", restored.Hash, dot.Hash)
	}
}
