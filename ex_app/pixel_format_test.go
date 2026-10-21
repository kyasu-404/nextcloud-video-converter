package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestConvertedFileCannotSilentlyLoseBitDepth(t *testing.T) {
	input := filepath.Join(t.TempDir(), "input.mkv")
	if err := os.WriteFile(input, makeTest10BitVideo(t), 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "reduced.mp4")
	cmd := exec.Command("ffmpeg", "-y", "-v", "error", "-i", input, "-c:v", "libx264", "-pix_fmt", "yuv420p", output)
	if data, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("8-bit fixture: %v: %s", err, data)
	}
	req := testConversionRequest()
	source := MediaInfo{BitDepth: "10 bit"}
	if err := verifyOutputBitDepth(context.Background(), req, source, output); err == nil {
		t.Fatal("8-bit result accepted while preserving original 10-bit depth")
	}
	req.BitDepth = "8"
	if err := verifyOutputBitDepth(context.Background(), req, source, output); err != nil {
		t.Fatalf("explicitly selected 8-bit output rejected: %v", err)
	}
}

func makeTest10BitVideo(t *testing.T) []byte {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe is not installed")
	}
	input := filepath.Join(t.TempDir(), "input.mkv")
	cmd := exec.Command("ffmpeg", "-y", "-v", "error", "-f", "lavfi", "-i", "color=c=red:s=64x64:r=2", "-t", "1", "-vf", "format=yuv420p10le", "-c:v", "ffv1", input)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("video fixture: %v: %s", err, output)
	}
	data, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestOutputPreservesBitDepthAndHDRTags(t *testing.T) {
	for _, test := range []struct {
		name, depth, codec, mode, wantFormat, wantTransfer string
		hdr                                                bool
	}{
		{name: "8-bit", depth: "8 bit", codec: "h264", mode: "copy", wantFormat: "yuv420p"},
		{name: "10-bit", depth: "10 bit", codec: "h264", mode: "copy", wantFormat: "yuv420p10le"},
		{name: "12-bit", depth: "12 bit", codec: "h265", mode: "copy", wantFormat: "yuv420p12le"},
		{name: "HDR-original", depth: "10 bit", codec: "h265", mode: "copy", wantFormat: "yuv420p10le", wantTransfer: "smpte2084", hdr: true},
		{name: "HDR-to-SDR", depth: "10 bit", codec: "h265", mode: "sdr", wantFormat: "yuv420p10le", wantTransfer: "bt709", hdr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := testConversionRequest()
			req.VideoCodec, req.HDRMode = test.codec, test.mode
			info := MediaInfo{BitDepth: test.depth, IsHDR: test.hdr, Transfer: "smpte2084"}
			args, err := buildFFmpegArgs(req, info, "in.mkv", "out.mp4")
			if err != nil {
				t.Fatal(err)
			}
			if !hasArgPair(args, "-pix_fmt", test.wantFormat) {
				t.Fatalf("wrong bit depth: %v", args)
			}
			if test.wantTransfer != "" && !hasArgPair(args, "-color_trc", test.wantTransfer) {
				t.Fatalf("wrong color transfer: %v", args)
			}
		})
	}
	req := testConversionRequest()
	if _, err := buildFFmpegArgs(req, MediaInfo{BitDepth: "12 bit"}, "in.mkv", "out.mp4"); err == nil {
		t.Fatal("unsupported original bit depth was silently reduced")
	}
	req.BitDepth = "8"
	args, err := buildFFmpegArgs(req, MediaInfo{BitDepth: "10 bit"}, "in.mkv", "out.mp4")
	if err != nil || !hasArgPair(args, "-pix_fmt", "yuv420p") {
		t.Fatalf("explicit 8-bit selection ignored: %v %v", args, err)
	}
	req.VideoCodec = "copy"
	args, err = buildFFmpegArgs(req, MediaInfo{BitDepth: "12 bit"}, "in.mkv", "out.mp4")
	if err != nil || strings.Contains(strings.Join(args, " "), "-pix_fmt") {
		t.Fatalf("stream copy changed pixel format: %v %v", args, err)
	}
}
