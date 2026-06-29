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

func TestParseAllowedGroupsValue(t *testing.T) {
	got := parseAllowedGroupsValue("video-editors, admin; video-editors\nmedia")
	want := []string{"video-editors", "admin", "media"}
	if !sameStrings(got, want) {
		t.Fatalf("parseAllowedGroupsValue() = %#v, want %#v", got, want)
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
