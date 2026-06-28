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
