package dman

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

func TestDotfileMarshal(t *testing.T) {

	dot := Dotfile{
		Name: "/Users/alex/.zshrc",
		ID:   []byte("lala"),
	}
	data, err := os.ReadFile(dot.Name)
	if err != nil {
		t.Fail()
	}
	dot.Data = data

	jdata, err := json.MarshalIndent(&dot, "", "  ")
	if err != nil {
		t.Fail()
	}

	var restored Dotfile
	json.Unmarshal(jdata, &restored)
	fmt.Println(string(restored.Data))
}
