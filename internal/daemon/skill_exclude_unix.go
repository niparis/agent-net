//go:build linux || darwin

package daemon

import (
	"golang.org/x/sys/unix"
	"io"
	"os"
)

func lockSkillExclude(path skillPath) (func(), error) {
	f, err := path.root.OpenFile(path.name+".agent-net.lock", os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { unix.Flock(int(f.Fd()), unix.LOCK_UN); f.Close() }, nil
}
func appendSkillExclude(path skillPath, data []byte) error {
	f, err := path.root.OpenFile(path.name, os.O_WRONLY|os.O_APPEND|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	return err
}

func readSkillExclude(path skillPath) ([]byte, error) {
	f, err := path.root.OpenFile(path.name, os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
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
