package app

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// App holds all resolved application paths.
type App struct {
	HomeDir   string
	HomeMode  os.FileMode
	ConfigDir string // ~/.config/dman
}

// NewApp resolves all paths from the environment and returns a ready-to-use App.
// It creates required directories if they do not exist.
func NewApp() (*App, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home directory: %w", err)
	}

	info, err := os.Stat(h)
	if err != nil {
		return nil, fmt.Errorf("stat home directory: %w", err)
	}

	configDir := filepath.Join(h, ".config", "dman")
	if !isExist(configDir) {
		if err := os.MkdirAll(configDir, info.Mode()); err != nil {
			return nil, fmt.Errorf("create config dir %s: %w", configDir, err)
		}
	}

	return &App{
		HomeDir:   h,
		HomeMode:  info.Mode(),
		ConfigDir: configDir,
	}, nil
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

// writeFile writes r to dst with the given mode, creating parent directories.
func writeFile(dst string, r io.Reader, mode fs.FileMode) (err error) {
	dir := filepath.Dir(dst)
	if merr := os.MkdirAll(dir, 0o755); merr != nil {
		if s := symlinkBlockingDir(dir); s != "" {
			return fmt.Errorf("mkdir %s: %w (symlink %s blocks directory creation; remove it from the repository first)", dir, merr, s)
		}
		return merr
	}

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	defer func() {
		if cerr := dstFile.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	if _, err := io.Copy(dstFile, r); err != nil {
		return err
	}
	// O_CREATE applies the mode only when it creates the file, so an existing
	// destination would otherwise keep whatever permissions it had.
	return dstFile.Chmod(mode.Perm())
}

// copySymlink recreates a symlink at dst pointing to the same target as src.
func copySymlink(dst, src string) error {
	target, err := os.Readlink(src)
	if err != nil {
		return err
	}
	dir := filepath.Dir(dst)
	if merr := os.MkdirAll(dir, 0o755); merr != nil {
		if s := symlinkBlockingDir(dir); s != "" {
			return fmt.Errorf("mkdir %s: %w (symlink %s blocks directory creation; remove it from the repository first)", dir, merr, s)
		}
		return merr
	}
	_ = os.RemoveAll(dst)
	return os.Symlink(target, dst)
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
