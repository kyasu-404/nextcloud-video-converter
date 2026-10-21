//go:build !linux

package main

import "os/exec"

func configureProcessCancellation(cmd *exec.Cmd) {
	// The production image is Linux. Other platforms use CommandContext's
	// default cancellation of the encoder itself (cpulimit is Linux-only).
}
