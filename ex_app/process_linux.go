package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configureProcessCancellation(cmd *exec.Cmd) {
	// cpulimit forks both the encoder and a limiter. Killing its parent PID
	// alone leaves those children running and holding the progress pipes open.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
