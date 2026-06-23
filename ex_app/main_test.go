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
