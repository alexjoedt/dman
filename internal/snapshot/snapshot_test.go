package snapshot

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotCRD(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	homeDir := t.TempDir()

	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	zshrc := filepath.Join(homeDir, ".zshrc")
	writeFile(t, zshrc, "export PATH=$PATH:/usr/local/bin\n")

	meta, err := store.Create(ctx, homeDir, []string{zshrc}, "test message")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if meta.FileCount != 1 {
		t.Errorf("FileCount = %d, want 1", meta.FileCount)
	}
	if meta.Message != "test message" {
		t.Errorf("Message = %q, want %q", meta.Message, "test message")
	}

	snaps, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("List count = %d, want 1", len(snaps))
	}
	if snaps[0].ID != meta.ID {
		t.Errorf("ID mismatch: got %s, want %s", snaps[0].ID, meta.ID)
	}

	files, err := store.Files(meta.ID)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("Files count = %d, want 1", len(files))
	}
	if files[0].Path != ".zshrc" {
		t.Errorf("Path = %q, want %q", files[0].Path, ".zshrc")
	}

	r, err := store.Cat(ctx, files[0].Checksum)
	if err != nil {
		t.Fatalf("Cat: %v", err)
	}
	got, _ := readAll(r)
	_ = r.Close()
	if got != "export PATH=$PATH:/usr/local/bin\n" {
		t.Errorf("Cat content = %q, want original content", got)
	}

	if err := store.Delete(ctx, meta.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	snaps, err = store.List()
	if err != nil {
		t.Fatalf("List after delete: %v", err)
	}
	if len(snaps) != 0 {
		t.Errorf("List count after delete = %d, want 0", len(snaps))
	}
}

func TestSnapshotDedup(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	homeDir := t.TempDir()

	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	f1 := filepath.Join(homeDir, ".zshrc")
	f2 := filepath.Join(homeDir, ".bashrc")
	content := "# same content\n"
	writeFile(t, f1, content)
	writeFile(t, f2, content)

	meta1, err := store.Create(ctx, homeDir, []string{f1}, "snap 1")
	if err != nil {
		t.Fatalf("Create snap1: %v", err)
	}
	meta2, err := store.Create(ctx, homeDir, []string{f2}, "snap 2")
	if err != nil {
		t.Fatalf("Create snap2: %v", err)
	}

	files1, _ := store.Files(meta1.ID)
	files2, _ := store.Files(meta2.ID)
	if files1[0].Checksum != files2[0].Checksum {
		t.Errorf("checksums differ but content is identical: %s vs %s",
			files1[0].Checksum, files2[0].Checksum)
	}

	stats, err := store.storage.GC(ctx)
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if stats.ObjectsRemoved != 0 {
		t.Errorf("GC removed %d objects, want 0 (dedup should share one object)", stats.ObjectsRemoved)
	}
}

func TestSnapshotDeleteRefCount(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	homeDir := t.TempDir()

	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	shared := filepath.Join(homeDir, ".zshrc")
	writeFile(t, shared, "shared content\n")

	meta1, err := store.Create(ctx, homeDir, []string{shared}, "snap 1")
	if err != nil {
		t.Fatalf("Create snap1: %v", err)
	}
	meta2, err := store.Create(ctx, homeDir, []string{shared}, "snap 2")
	if err != nil {
		t.Fatalf("Create snap2: %v", err)
	}

	checksum := func() string {
		f, _ := store.Files(meta1.ID)
		return f[0].Checksum
	}()

	if err := store.Delete(ctx, meta1.ID); err != nil {
		t.Fatalf("Delete snap1: %v", err)
	}

	r, err := store.Cat(ctx, checksum)
	if err != nil {
		t.Fatalf("Cat after deleting snap1: %v, blob should still exist (referenced by snap2)", err)
	}
	_ = r.Close()

	if err := store.Delete(ctx, meta2.ID); err != nil {
		t.Fatalf("Delete snap2: %v", err)
	}

	if _, err := store.Cat(ctx, checksum); err == nil {
		t.Error("Cat after deleting both snapshots should fail; blob should be gone")
	}
}

func readAll(r interface{ Read([]byte) (int, error) }) (string, error) {
	buf := make([]byte, 0, 512)
	tmp := make([]byte, 512)
	for {
		n, err := r.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return string(buf), nil
}

func TestSnapshotDeleteOrphanedIndexEntry(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	homeDir := t.TempDir()

	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	f := filepath.Join(homeDir, ".zshrc")
	writeFile(t, f, "content\n")
	meta, err := store.Create(ctx, homeDir, []string{f}, "snap")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Simulate an earlier partial delete that lost the manifest but not the index entry.
	if err := os.Remove(store.manifestPath(meta.ID)); err != nil {
		t.Fatalf("remove manifest: %v", err)
	}

	if err := store.Delete(ctx, meta.ID); err != nil {
		t.Fatalf("Delete orphaned entry: %v", err)
	}
	list, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("index still lists %d snapshot(s) after delete", len(list))
	}
}

func TestSaveMetadata_FailedWriteKeepsPrevious(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.saveIndex(&Index{Snapshots: []Meta{{ID: "a"}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.saveManifest(&Manifest{ID: "a"}); err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(dir, indexFile)
	paths := []string{indexPath, store.manifestPath("a")}
	before := map[string]string{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		before[p] = string(b)
	}
	entriesBefore, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o750) })
	if err := store.saveIndex(&Index{Snapshots: []Meta{{ID: "b"}}}); err == nil {
		t.Error("saveIndex into read-only dir succeeded")
	}
	if err := store.saveManifest(&Manifest{ID: "a", Files: []File{{Path: "x"}}}); err == nil {
		t.Error("saveManifest into read-only dir succeeded")
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != before[p] {
			t.Errorf("%s changed by failed write: %q", filepath.Base(p), b)
		}
	}
	entriesAfter, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entriesAfter) != len(entriesBefore) {
		t.Errorf("leftover files: %v", entriesAfter)
	}
}

func TestSnapshotRecordsAbsentFiles(t *testing.T) {
	ctx := context.Background()
	homeDir := t.TempDir()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	present := filepath.Join(homeDir, ".present")
	writeFile(t, present, "x\n")

	meta, err := store.Create(ctx, homeDir, []string{present, filepath.Join(homeDir, ".newrc")}, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if meta.FileCount != 2 {
		t.Errorf("FileCount = %d, want 2", meta.FileCount)
	}
	files, err := store.Files(meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Absent || files[0].Checksum == "" {
		t.Fatalf("present entry wrong: %+v", files)
	}
	if got := files[1]; !got.Absent || got.Path != ".newrc" || got.Checksum != "" || got.Size != 0 || got.Mode != 0 {
		t.Errorf("absent entry = %+v", got)
	}
	if err := store.Delete(ctx, meta.ID); err != nil {
		t.Errorf("Delete with absent entry: %v", err)
	}
}

func TestSnapshotLoadsManifestWithoutAbsentField(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	old := `{"id":"old","files":[{"path":".zshrc","checksum":"abc","size":3,"mode":420}]}`
	if err := os.WriteFile(filepath.Join(dir, "old.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := store.Files("old")
	if err != nil || len(files) != 1 || files[0].Absent || files[0].Checksum != "abc" {
		t.Errorf("Files = %+v, %v", files, err)
	}
}

func TestSnapshotNotDirParentIsAbsent(t *testing.T) {
	ctx := context.Background()
	homeDir := t.TempDir()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(homeDir, "foo"), "x\n")
	meta, err := store.Create(ctx, homeDir, []string{filepath.Join(homeDir, "foo", "bar")}, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	files, _ := store.Files(meta.ID)
	if len(files) != 1 || !files[0].Absent {
		t.Errorf("files = %+v", files)
	}
}
