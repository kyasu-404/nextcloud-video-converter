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
