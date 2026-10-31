//go:build linux || darwin

package daemon

import (
	"golang.org/x/sys/unix"
	"io"
	"os"
)

func lockSkillExclude(path string) (func(), error) {
	fd, err := unix.Open(path+".agent-net.lock", unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(fd, unix.LOCK_EX); err != nil {
		unix.Close(fd)
		return nil, err
	}
	return func() { unix.Flock(fd, unix.LOCK_UN); unix.Close(fd) }, nil
}
func appendSkillExclude(path string, data []byte) error {
	fd, err := unix.Open(path, unix.O_WRONLY|unix.O_APPEND|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), path)
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	return err
}

func readSkillExclude(path string) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 4<<20 {
		return nil, os.ErrInvalid
	}
	return io.ReadAll(io.LimitReader(f, (4<<20)+1))
}
