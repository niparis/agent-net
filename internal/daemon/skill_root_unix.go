//go:build linux || darwin

package daemon

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"golang.org/x/sys/unix"
)

// openSkillRoot walks from the home directory using no-follow directory
// descriptors. All later staging and rename operations use that descriptor.
func openSkillRoot(home, rel string) (string, func(), error) {
	fd, err := unix.Open(home, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", nil, err
	}
	for _, part := range strings.Split(rel, "/") {
		err = unix.Mkdirat(fd, part, 0700)
		if err != nil && err != unix.EEXIST {
			unix.Close(fd)
			return "", nil, err
		}
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return "", nil, e
		}
		fd = next
	}
	prefix := "/proc/self/fd"
	if runtime.GOOS == "darwin" {
		prefix = "/dev/fd"
	}
	path := fmt.Sprintf("%s/%d", prefix, fd)
	if _, err = os.Stat(path); err != nil {
		unix.Close(fd)
		return "", nil, err
	}
	return path, func() { unix.Close(fd) }, nil
}
