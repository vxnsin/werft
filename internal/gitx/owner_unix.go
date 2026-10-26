//go:build unix

package gitx

import (
	"os"
	"os/user"
	"strconv"
	"syscall"
)

// Owner returns the user name that owns dir.
func Owner(dir string) string {
	info, err := os.Stat(dir)
	if err != nil {
		return ""
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	u, err := user.LookupId(strconv.Itoa(int(st.Uid)))
	if err != nil {
		return ""
	}
	return u.Username
}
