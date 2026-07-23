//go:build unix

package paths

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// FileIdentity pins a filesystem object across renames: device plus
// inode on every supported platform (Linux and macOS).
type FileIdentity struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

// Identity stats path (without following a trailing symlink) and
// returns its identity.
func Identity(path string) (FileIdentity, error) {
	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		return FileIdentity{}, fmt.Errorf("stat %s: %w", path, err)
	}
	return FileIdentity{Device: uint64(st.Dev), Inode: uint64(st.Ino)}, nil
}

// IdentityFromInfo extracts identity from a stat result. It returns the
// zero identity when info carries no platform stat (e.g. test fakes).
func IdentityFromInfo(info fs.FileInfo) FileIdentity {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return FileIdentity{}
	}
	return FileIdentity{Device: uint64(st.Dev), Inode: uint64(st.Ino)}
}

// LinkCount reports the hard-link count from a stat result, or 1 when
// unknown.
func LinkCount(info fs.FileInfo) uint64 {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 1
	}
	return uint64(st.Nlink)
}

func identityOf(f *os.File) (FileIdentity, error) {
	var st syscall.Stat_t
	if err := syscall.Fstat(int(f.Fd()), &st); err != nil {
		return FileIdentity{}, fmt.Errorf("stat %s: %w", f.Name(), err)
	}
	return FileIdentity{Device: uint64(st.Dev), Inode: uint64(st.Ino)}, nil
}
