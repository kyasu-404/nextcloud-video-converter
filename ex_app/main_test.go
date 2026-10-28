package main

import "testing"

func TestBuildNextcloudAPIURLStripsIndexPHP(t *testing.T) {
	cfg := Config{NextcloudURL: "https://nc.example.test/index.php"}

	got, err := buildNextcloudAPIURL(cfg, "/ocs/v1.php/apps/app_api/ex-app/status")
	if err != nil {
		t.Fatal(err)
	}

	const want = "https://nc.example.test/ocs/v1.php/apps/app_api/ex-app/status"
	if got != want {
		t.Fatalf("buildNextcloudAPIURL() = %q, want %q", got, want)
	}
}

func TestBuildWebDAVURLStripsIndexPHP(t *testing.T) {
	cfg := Config{
		NextcloudURL: "https://nc.example.test/index.php",
		BasePath:     "/remote.php/webdav",
	}

	got, err := buildWebDAVURL(cfg, "/Videos/source file.mp4")
	if err != nil {
		t.Fatal(err)
	}

	const want = "https://nc.example.test/remote.php/webdav/Videos/source%20file.mp4"
	if got != want {
		t.Fatalf("buildWebDAVURL() = %q, want %q", got, want)
	}
}

func TestBuildRemoteOutputPathKeepsSourceDirectory(t *testing.T) {
	got := buildRemoteOutputPath("/Videos/Trips/source.mp4", "source.mp4", "mkv")
	const want = "/Videos/Trips/source_converted.mkv"
	if got != want {
		t.Fatalf("buildRemoteOutputPath() = %q, want %q", got, want)
	}
}

func TestUserIDFromAppAPIAuth(t *testing.T) {
	got := userIDFromAppAPIAuth("a3lhc3U6c2VjcmV0")
	const want = "kyasu"
	if got != want {
		t.Fatalf("userIDFromAppAPIAuth() = %q, want %q", got, want)
	}
}

func TestBuildFFmpegArgsAudioCodecs(t *testing.T) {
	tests := []struct {
		name      string
		codec     string
		bitrate   string
		wantCodec string
		wantRate  string
	}{
		{name: "aac", codec: "aac", bitrate: "192", wantCodec: "aac", wantRate: "192k"},
		{name: "ac3", codec: "ac3", bitrate: "320", wantCodec: "ac3", wantRate: "320k"},
		{name: "opus", codec: "opus", bitrate: "128", wantCodec: "libopus", wantRate: "128k"},
		{name: "mp3", codec: "mp3", bitrate: "192", wantCodec: "libmp3lame", wantRate: "192k"},
		{name: "flac", codec: "flac", wantCodec: "flac"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, err := buildFFmpegArgs(ConversionRequest{
				Container:    "mkv",
				VideoCodec:   "copy",
				AudioCodec:   tt.codec,
				AudioBitrate: tt.bitrate,
				Subtitles:    false,
				Metadata:     "copy",
			}, MediaInfo{}, "input.mov", "output.mkv")
			if err != nil {
				t.Fatal(err)
			}
			if !hasArgPair(args, "-c:a", tt.wantCodec) {
				t.Fatalf("expected audio codec %q in args %v", tt.wantCodec, args)
			}
			if tt.wantRate != "" && !hasArgPair(args, "-b:a", tt.wantRate) {
				t.Fatalf("expected audio bitrate %q in args %v", tt.wantRate, args)
			}
			if tt.wantRate == "" && hasArg(args, "-b:a") {
				t.Fatalf("did not expect audio bitrate in args %v", args)
			}
		})
	}
}

func TestParsePositiveIntValue(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  int
	}{
		{name: "string", value: "35", want: 35},
		{name: "json string", value: "\"40\"", want: 40},
		{name: "too high", value: float64(150), want: 100},
		{name: "invalid defaults", value: "nope", want: 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parsePositiveIntValue(tt.value, 100, 1, 100); got != tt.want {
				t.Fatalf("parsePositiveIntValue() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestResolveCPULimitKeepsZeroThreadsUnlimited(t *testing.T) {
	limit := resolveCPULimit(50, 0)
	if limit.Percent != 50 {
		t.Fatalf("Percent = %d, want 50", limit.Percent)
	}
	if limit.Threads != 0 {
		t.Fatalf("Threads = %d, want 0", limit.Threads)
	}
	if limit.CPULimitPercent <= 0 {
		t.Fatalf("CPULimitPercent = %d, want positive", limit.CPULimitPercent)
	}
}

func TestApplyFFmpegThreadLimit(t *testing.T) {
	args := []string{"-y", "-i", "input.mp4", "-c:v", "libx264", "output.mp4"}
	got := applyFFmpegThreadLimit(args, 2)
	want := []string{"-y", "-i", "input.mp4", "-c:v", "libx264", "-threads", "2", "output.mp4"}
	if !sameStrings(got, want) {
		t.Fatalf("applyFFmpegThreadLimit() = %#v, want %#v", got, want)
	}
}

func TestFFmpegCommandUsesCPULimitWhenAvailable(t *testing.T) {
	cmd, args := ffmpegCommand([]string{"-i", "in.mp4", "out.mp4"}, CPULimit{
		Percent:         50,
		CPULimitPercent: 200,
	}, true)

	if cmd != "cpulimit" {
		t.Fatalf("command = %q, want cpulimit", cmd)
	}
	want := []string{"-l", "200", "--", "ffmpeg", "-i", "in.mp4", "out.mp4"}
	if !sameStrings(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func hasArgPair(args []string, key, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == key && args[i+1] == value {
			return true
		}
	}
	return false
}

func hasArg(args []string, key string) bool {
	for _, arg := range args {
		if arg == key {
			return true
		}
	}
	return false
}

func TestQualityToCRF_NumericAndPresets(t *testing.T) {
	tests := []struct {
		codec   string
		quality string
		want    string
	}{
		{"h264", "18", "18"},
		{"h264", "23", "23"},
		{"h264", "28", "28"},
		{"h264", "32", "32"},
		{"h264", "low", "28"},
		{"h264", "high", "18"},
		{"h265", "20", "20"},
		{"h265", "normal", "28"},
		{"av1", "30", "30"},
		{"av1", "normal", "40"},
	}

	for _, tt := range tests {
		got := qualityToCRF(tt.codec, tt.quality)
		if got != tt.want {
			t.Errorf("qualityToCRF(%q, %q) = %q, want %q", tt.codec, tt.quality, got, tt.want)
		}
	}
}

func TestValidateRequest_Tonemap(t *testing.T) {
	baseReq := ConversionRequest{
		FileID:       "file-123",
		FilePath:     "/test.mp4",
		Container:    "mp4",
		VideoCodec:   "h264",
		Resolution:   "original",
		QualityCRF:   "23",
		HDRMode:      "sdr",
		AudioCodec:   "aac",
		BitDepth:     "8",
		Preset:       "medium",
		FPS:          "copy",
		Metadata:     "copy",
	}

	validReq := baseReq
	validReq.Tonemap = "hable"
	if err := validateRequest(&validReq); err != nil {
		t.Fatalf("expected valid tonemap 'hable' to pass, got: %v", err)
	}

	invalidReq := baseReq
	invalidReq.Tonemap = "hable:desat=0,drawtext=evil"
	if err := validateRequest(&invalidReq); err == nil {
		t.Fatalf("expected injection tonemap to be rejected, but it passed")
	}
}

func TestBuildFFmpegArgs_BitmapSubtitles(t *testing.T) {
	req := ConversionRequest{
		Container:    "mp4",
		VideoCodec:   "h264",
		AudioCodec:   "aac",
		Subtitles:    true,
		Metadata:     "copy",
		QualityCRF:   "23",
		Resolution:   "original",
		HDRMode:      "copy",
		BitDepth:     "8",
	}

	// 1. Text subtitles (e.g. subrip / srt) should use mov_text in MP4
	textInfo := MediaInfo{
		HasSubtitles:  true,
		SubtitleCodec: "subrip",
	}
	args, err := buildFFmpegArgs(req, textInfo, "in.mkv", "out.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if !hasArgPair(args, "-c:s", "mov_text") {
		t.Errorf("expected -c:s mov_text for text subtitles in mp4, got args %v", args)
	}

	// 2. Bitmap subtitles (e.g. hdmv_pgs_subtitle) should drop subtitles to avoid ffmpeg crash in MP4
	bitmapInfo := MediaInfo{
		HasSubtitles:  true,
		SubtitleCodec: "hdmv_pgs_subtitle",
	}
	argsBitmap, err := buildFFmpegArgs(req, bitmapInfo, "in.mkv", "out.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if hasArgPair(argsBitmap, "-c:s", "mov_text") {
		t.Errorf("did not expect mov_text for bitmap subtitles in mp4, got args %v", argsBitmap)
	}
	if !hasArg(argsBitmap, "-sn") {
		t.Errorf("expected -sn to drop bitmap subtitles in mp4, got args %v", argsBitmap)
	}

	// 3. For MKV container, bitmap subtitles can be copied safely
	reqMKV := req
	reqMKV.Container = "mkv"
	argsMKV, err := buildFFmpegArgs(reqMKV, bitmapInfo, "in.mkv", "out.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if !hasArgPair(argsMKV, "-c:s", "copy") {
		t.Errorf("expected -c:s copy for bitmap subtitles in mkv, got args %v", argsMKV)
	}
}

