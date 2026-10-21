package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestFFmpegCancellationStopsProcessTree(t *testing.T) {
	modes := []string{"direct", "wrapper"}
	if os.Getenv("VC_TEST_CPULIMIT_BIN") != "" {
		modes = append(modes, "cpulimit")
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			pidFile := filepath.Join(dir, "encoder.pid")
			t.Setenv("VC_TEST_ENCODER_PID", pidFile)
			t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
			if err := os.WriteFile(filepath.Join(dir, "ffmpeg"), []byte("#!/bin/sh\necho $$ > \"$VC_TEST_ENCODER_PID\"\necho out_time_us=1\nexec sleep 30\n"), 0755); err != nil {
				t.Fatal(err)
			}
			wrapper := "#!/bin/sh\nshift 3\n\"$@\" &\nwait $!\n"
			if mode == "cpulimit" {
				wrapper = "#!/bin/sh\nexec \"$VC_TEST_CPULIMIT_BIN\" \"$@\"\n"
			}
			if err := os.WriteFile(filepath.Join(dir, "cpulimit"), []byte(wrapper), 0755); err != nil {
				t.Fatal(err)
			}
			cleanupChild := func() {
				data, _ := os.ReadFile(pidFile)
				if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
					_ = syscall.Kill(pid, syscall.SIGKILL)
				}
			}
			defer cleanupChild()
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			limit := CPULimit{Percent: 50, CPULimitPercent: 50}
			if mode == "direct" {
				limit = CPULimit{Percent: 100}
			}
			result := make(chan error, 1)
			go func() { result <- runFFmpeg("test-process-tree", ctx, nil, 1, Config{}, "", "", limit) }()
			var runErr error
			select {
			case err := <-result:
				runErr = err
				if err == nil {
					t.Fatal("cancelled conversion returned success")
				}
			case <-time.After(3 * time.Second):
				cleanupChild()
				select {
				case <-result:
				case <-time.After(time.Second):
				}
				t.Fatal("cancelled encoder kept running or held the output pipes open")
			}
			data, err := os.ReadFile(pidFile)
			if err != nil {
				t.Fatalf("encoder never started: %v (command error: %v)", err, runErr)
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil {
				t.Fatal(err)
			}
			stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
			if err == nil {
				fields := strings.Fields(string(stat)[strings.LastIndex(string(stat), ")")+1:])
				if len(fields) > 0 && fields[0] != "Z" && fields[0] != "X" {
					t.Fatalf("encoder %d survived cancellation, state=%s", pid, fields[0])
				}
			} else if !os.IsNotExist(err) {
				t.Fatal(err)
			}
		})
	}
}
