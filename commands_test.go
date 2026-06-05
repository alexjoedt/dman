package dman

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandAddInputs_FileAndDirectory(t *testing.T) {
	home := t.TempDir()

	dotfile := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(dotfile, []byte("export EDITOR=nvim\n"), 0o644); err != nil {
		t.Fatalf("write dotfile: %v", err)
	}

	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin dir: %v", err)
	}
	script := filepath.Join(binDir, "myscript")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	inputs, err := expandAddInputs(home, []string{dotfile, binDir})
	if err != nil {
		t.Fatalf("expand inputs: %v", err)
	}

	if len(inputs) != 2 {
		t.Fatalf("expected 2 inputs, got %d", len(inputs))
	}

	foundDotfile := false
	foundScript := false
	for _, in := range inputs {
		switch in.path {
		case dotfile:
			if in.fromDir {
				t.Fatalf("expected direct file to have fromDir=false")
			}
			foundDotfile = true
		case script:
			if !in.fromDir {
				t.Fatalf("expected walked directory file to have fromDir=true")
			}
			foundScript = true
		}
	}

	if !foundDotfile {
		t.Fatalf("missing direct dotfile input")
	}
	if !foundScript {
		t.Fatalf("missing directory file input")
	}
}

func TestExpandAddInputs_RejectsOutsideHome(t *testing.T) {
	home := t.TempDir()
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, ".outside")
	if err := os.WriteFile(outsideFile, []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	_, err := expandAddInputs(home, []string{outsideFile})
	if err == nil {
		t.Fatalf("expected error for file outside home")
	}
}

func TestExpandAddInputs_Deduplicates(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin dir: %v", err)
	}
	script := filepath.Join(binDir, "tool")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	inputs, err := expandAddInputs(home, []string{binDir, script})
	if err != nil {
		t.Fatalf("expand inputs: %v", err)
	}
	if len(inputs) != 1 {
		t.Fatalf("expected 1 input after dedupe, got %d", len(inputs))
	}
	if inputs[0].path != script {
		t.Fatalf("unexpected path: %s", inputs[0].path)
	}
}
