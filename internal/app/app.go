package app

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/alexjoedt/dman/internal/fsutil"
)

// App holds all resolved application paths.
type App struct {
	HomeDir   string
	HomeMode  os.FileMode
	ConfigDir string // $XDG_CONFIG_HOME/dman or ~/.config/dman

	crypt codecCache
}

// NewApp resolves all paths from the environment and returns a ready-to-use App.
// It creates no directories; saveConfig creates the config dir.
func NewApp() (*App, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home directory: %w", err)
	}

	info, err := os.Stat(h)
	if err != nil {
		return nil, fmt.Errorf("stat home directory: %w", err)
	}

	return &App{
		HomeDir:   h,
		HomeMode:  info.Mode(),
		ConfigDir: xdgPath(h, "XDG_CONFIG_HOME", ".config", configFileName),
	}, nil
}

// xdgPath returns <base>/dman, where base is the absolute directory in the
// env variable, or <home>/<legacy> when it is unset, empty or relative. When
// marker is non-empty and exists only below the legacy dir, the legacy dir is
// returned, so an existing setup keeps working after XDG is set.
func xdgPath(home, env, legacy, marker string) string {
	old := filepath.Join(home, legacy, "dman")
	base := os.Getenv(env)
	if !filepath.IsAbs(base) {
		return old
	}
	dir := filepath.Join(base, "dman")
	if marker != "" && isMissing(filepath.Join(dir, marker)) && isExist(filepath.Join(old, marker)) {
		return old
	}
	return dir
}

// isMissing reports whether p does not exist. A broken symlink or a stat
// error other than not-found counts as present, so the error surfaces later.
func isMissing(p string) bool {
	_, err := os.Lstat(p)
	return errors.Is(err, fs.ErrNotExist)
}

func isExist(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// symlinkBlockingDir walks dir upward and returns the first path component
// that exists as a symlink, or "" if none is found. Used to produce a
// helpful error when os.MkdirAll fails because a symlink sits in the way.
func symlinkBlockingDir(dir string) string {
	cur := dir
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		if fi, err := os.Lstat(cur); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return cur
		}
		cur = parent
	}
}

// copyFile copies src to dst, preserving the source file's permissions.
func copyFile(dst, src string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = srcFile.Close() }()

	return writeFile(dst, srcFile, srcInfo.Mode())
}

// writeFile writes r to dst atomically with the given mode, creating parent
// directories.
func writeFile(dst string, r io.Reader, mode fs.FileMode) error {
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		if s := symlinkBlockingDir(dir); s != "" {
			return fmt.Errorf("mkdir %s: %w (symlink %s blocks directory creation; remove it from the repository first)", dir, err, s)
		}
		return err
	}
	return fsutil.WriteFile(dst, r, mode)
}

func isSymlink(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.Mode()&os.ModeSymlink != 0
}

// executableMagics holds the leading bytes of common executable binary
// formats. Matching by magic keeps detection dependency-free while still
// allowing non-executable binaries such as images to be tracked.
var executableMagics = [][]byte{
	{0x7f, 'E', 'L', 'F'},    // ELF (Linux/BSD)
	{'M', 'Z'},               // DOS/PE (Windows .exe, .dll)
	{0xfe, 0xed, 0xfa, 0xce}, // Mach-O 32-bit
	{0xfe, 0xed, 0xfa, 0xcf}, // Mach-O 64-bit
	{0xce, 0xfa, 0xed, 0xfe}, // Mach-O 32-bit (reverse)
	{0xcf, 0xfa, 0xed, 0xfe}, // Mach-O 64-bit (reverse)
	{0xca, 0xfe, 0xba, 0xbe}, // Mach-O universal (fat) binary
	{0x00, 'a', 's', 'm'},    // WebAssembly module
}

// isExecutableFile reports whether path is a compiled executable binary.
// It inspects the leading bytes for known executable formats (ELF, PE,
// Mach-O, Wasm). Non-executable binaries such as PNG/JPEG images are not
// matched, so they can still be tracked. Shebang scripts are treated as
// text and therefore allowed as well.
func isExecutableFile(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()

	buf := make([]byte, 8)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return false, err
	}
	head := buf[:n]

	for _, magic := range executableMagics {
		if bytes.HasPrefix(head, magic) {
			return true, nil
		}
	}

	return false, nil
}
