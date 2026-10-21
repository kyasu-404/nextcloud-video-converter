package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Some FFmpeg builds silently select a lower-depth format when their encoder
// cannot produce the requested one. Verify the result before uploading it or
// deleting the source, rather than relying on command-line options alone.
func verifyOutputBitDepth(ctx context.Context, req ConversionRequest, source MediaInfo, outputPath string) error {
	if req.VideoCodec == "copy" {
		return nil // Stream copy preserves the encoded video bitstream.
	}
	pixelFormat, err := outputPixelFormat(req, source)
	if err != nil {
		return err
	}
	actual, err := probeMedia(ctx, outputPath)
	if err != nil {
		return fmt.Errorf("cannot verify converted video: %w", err)
	}
	expected := bitDepthFromPixelFormat(pixelFormat, "")
	if actual.BitDepth != expected {
		return fmt.Errorf("encoder produced %s video instead of %s; the original was preserved", actual.BitDepth, expected)
	}
	return nil
}

func outputPixelFormat(req ConversionRequest, info MediaInfo) (string, error) {
	depth := 8
	switch req.BitDepth {
	case "10":
		depth = 10
	case "", "copy":
		bits := info.BitDepth
		if bits == "" {
			bits = bitDepthFromPixelFormat(info.PixelFormat, "")
		}
		if fields := strings.Fields(bits); len(fields) > 0 {
			if n, err := strconv.Atoi(fields[0]); err == nil {
				depth = n
			}
		}
		if req.HDRMode == "hdr" && depth < 10 {
			depth = 10
		}
	}
	switch {
	case depth <= 8:
		return "yuv420p", nil
	case depth <= 10:
		return "yuv420p10le", nil
	case depth <= 12 && req.VideoCodec == "h265":
		return "yuv420p12le", nil
	default:
		return "", fmt.Errorf("%s cannot preserve %d-bit video; select an explicit supported bit depth", req.VideoCodec, depth)
	}
}
