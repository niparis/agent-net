//go:build darwin

package daemon

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"time"
)

func processHasOpenPath(path string) (bool, error) {
	if os.Geteuid() != 0 {
		return false, errors.New("cannot inspect all processes without sufficient permissions")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "lsof", "-n", "-F", "n", "+D", path)
	var output, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if stderr.Len() != 0 {
		return false, errors.New(stderr.String())
	}
	if output.Len() > 0 {
		return true, nil
	}
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}
