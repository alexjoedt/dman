package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

const (
	commandMatrixStart = "<!-- COMMAND_MATRIX_START -->"
	commandMatrixEnd   = "<!-- COMMAND_MATRIX_END -->"
)

type namedFlag interface {
	Names() []string
}

func TestReadmeCommandMatrixMatchesCLI(t *testing.T) {
	readmePath := readmePathFromThisFile(t)
	data, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read README: %v", err)
	}

	readme := string(data)
	want, err := extractCommandMatrix(readme)
	if err != nil {
		t.Fatalf("extract command matrix: %v", err)
	}

	got := renderCommandMatrix(newRootCommand(nil))
	if strings.TrimSpace(got) != strings.TrimSpace(want) {
		t.Fatalf("README command matrix is out of sync\n\nexpected:\n%s\n\nactual:\n%s", want, got)
	}
}

func readmePathFromThisFile(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("determine current file path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "README.md"))
}

func extractCommandMatrix(readme string) (string, error) {
	start := strings.Index(readme, commandMatrixStart)
	if start == -1 {
		return "", fmt.Errorf("missing %s marker", commandMatrixStart)
	}
	start += len(commandMatrixStart)

	end := strings.Index(readme[start:], commandMatrixEnd)
	if end == -1 {
		return "", fmt.Errorf("missing %s marker", commandMatrixEnd)
	}

	return strings.TrimSpace(readme[start : start+end]), nil
}

func renderCommandMatrix(root *cli.Command) string {
	rows := []string{
		"| Command | Args | Flags |",
		"| --- | --- | --- |",
	}

	for _, cmd := range root.Commands {
		collectCommandRows(&rows, "dman", cmd)
	}

	return strings.Join(rows, "\n")
}

func collectCommandRows(rows *[]string, prefix string, cmd *cli.Command) {
	commandPath := fmt.Sprintf("`%s %s`", prefix, cmd.Name)
	args := "-"
	if cmd.ArgsUsage != "" {
		args = fmt.Sprintf("`%s`", cmd.ArgsUsage)
	}

	flags := "-"
	if len(cmd.Flags) > 0 {
		flagNames := make([]string, 0, len(cmd.Flags))
		for _, f := range cmd.Flags {
			nf, ok := f.(namedFlag)
			if !ok {
				continue
			}
			for _, name := range nf.Names() {
				if len(name) == 1 {
					flagNames = append(flagNames, fmt.Sprintf("`-%s`", name))
					continue
				}
				flagNames = append(flagNames, fmt.Sprintf("`--%s`", name))
			}
		}
		if len(flagNames) > 0 {
			flags = strings.Join(flagNames, ", ")
		}
	}

	*rows = append(*rows, fmt.Sprintf("| %s | %s | %s |", commandPath, args, flags))

	for _, sub := range cmd.Commands {
		collectCommandRows(rows, prefix+" "+cmd.Name, sub)
	}
}
