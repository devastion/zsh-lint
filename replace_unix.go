//go:build unix

package main

import (
	"io/fs"
	"os"
	"syscall"
)

// replaceable reports whether renaming a new file over this one keeps what
// other paths and its owner see: it has no other hard links and it belongs to
// the current user.
func replaceable(info fs.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Nlink == 1 && int(st.Uid) == os.Geteuid()
}
