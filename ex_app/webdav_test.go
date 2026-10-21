package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestConcurrentUploadsPreserveExistingResults(t *testing.T) {
	var mu sync.Mutex
	base := "/remote.php/webdav/source_converted.mp4"
	files := map[string]string{base: "valuable previous result"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.Header.Get("If-None-Match") != "*" {
			t.Errorf("upload missing atomic create precondition: %s %v", r.Method, r.Header)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		if _, exists := files[r.URL.Path]; exists {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		files[r.URL.Path] = string(body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	cfg := Config{NextcloudURL: server.URL, BasePath: "/remote.php/webdav"}
	request := testConversionRequest()
	file := filepath.Join(t.TempDir(), "result.mp4")
	if err := os.WriteFile(file, []byte("new conversion"), 0600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	outputs := make(chan string, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			output, err := uploadConvertedWebDAV(context.Background(), server.Client(), cfg, request, file)
			if err != nil {
				t.Error(err)
				return
			}
			outputs <- output
		}()
	}
	wg.Wait()
	close(outputs)
	seen := make(map[string]bool)
	for output := range outputs {
		if seen[output] {
			t.Fatalf("concurrent conversions shared an output: %s", output)
		}
		seen[output] = true
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 || len(files) != 3 || files[base] != "valuable previous result" {
		t.Fatalf("existing or new results lost: outputs=%v files=%v", seen, files)
	}
}

func TestDeleteOnlyDownloadedSourceVersion(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprint("changed=", changed), func(t *testing.T) {
			var mu sync.Mutex
			version, exists := `"v1"`, true
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch r.Method {
				case http.MethodGet:
					w.Header().Set("ETag", version)
					fmt.Fprint(w, "downloaded version")
				case http.MethodDelete:
					if r.Header.Get("If-Match") != version {
						w.WriteHeader(http.StatusPreconditionFailed)
						return
					}
					exists = false
					w.WriteHeader(http.StatusNoContent)
				}
			}))
			defer server.Close()
			cfg := Config{NextcloudURL: server.URL, BasePath: "/remote.php/webdav"}
			file := filepath.Join(t.TempDir(), "input.mkv")
			etag, err := downloadWebDAV(context.Background(), server.Client(), cfg, "", testAuth("alice"), "/source.mkv", file, "")
			if err != nil || etag != `"v1"` {
				t.Fatalf("download ETag=%q, error=%v", etag, err)
			}
			if changed {
				mu.Lock()
				version = `"v2"`
				mu.Unlock()
			}
			err = deleteWebDAV(context.Background(), server.Client(), cfg, "", testAuth("alice"), "", "/source.mkv", etag)
			mu.Lock()
			defer mu.Unlock()
			if changed && (err == nil || !exists) {
				t.Fatalf("changed source was not preserved: exists=%v error=%v", exists, err)
			}
			if !changed && (err != nil || exists) {
				t.Fatalf("unchanged original not deleted: exists=%v error=%v", exists, err)
			}
		})
	}
}

func TestDeleteRefusesMissingOrWeakETag(t *testing.T) {
	for _, etag := range []string{"", `W/"v1"`, "*", "invalid", "\"v1\"\r\n"} {
		if err := deleteWebDAV(context.Background(), &http.Client{}, Config{}, "", "", "", "/source.mkv", etag); err == nil {
			t.Errorf("unsafe deletion allowed for ETag %q", etag)
		}
	}
}

func TestTaskFileLifecycle(t *testing.T) {
	input := makeTest10BitVideo(t)
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprint("source-changed=", changed), func(t *testing.T) {
			var mu sync.Mutex
			version, originalExists := `"v1"`, true
			outputs := map[string][]byte{"/remote.php/webdav/source_converted.mp4": []byte("previous result")}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if strings.HasPrefix(r.URL.Path, "/ocs/") {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				switch r.Method {
				case http.MethodGet:
					w.Header().Set("ETag", version)
					w.Write(input)
				case http.MethodPut:
					if r.Header.Get("If-None-Match") != "*" {
						t.Error("output upload had no precondition")
					}
					if _, exists := outputs[r.URL.Path]; exists {
						w.WriteHeader(http.StatusPreconditionFailed)
						return
					}
					outputs[r.URL.Path], _ = io.ReadAll(r.Body)
					if changed {
						version = `"v2"`
					}
					w.WriteHeader(http.StatusCreated)
				case http.MethodDelete:
					if r.Header.Get("If-Match") != version {
						w.WriteHeader(http.StatusPreconditionFailed)
						return
					}
					originalExists = false
					w.WriteHeader(http.StatusNoContent)
				}
			}))
			defer server.Close()
			cfg := Config{NextcloudURL: server.URL, BasePath: "/remote.php/webdav", OutputDir: t.TempDir(), AppID: "converter", AppSecret: "test-secret"}
			request := testConversionRequest()
			request.UserID, request.AppAPIAuth, request.DeleteOriginal = "alice", testAuth("alice"), true
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reservation, _ := (&jobGate{}).reserve(defaultAppSettings(), "alice")
			id := newTask(request, cancel)
			processTask(cfg, id, request, ctx, cancel, reservation)
			task, _ := taskSnapshot(id)
			wantStatus := "Готово"
			if changed {
				wantStatus = "Ошибка"
			}
			if task.Status != wantStatus {
				t.Fatalf("task %s: %s", task.Status, task.Error)
			}
			mu.Lock()
			defer mu.Unlock()
			if originalExists != changed || string(outputs["/remote.php/webdav/source_converted.mp4"]) != "previous result" {
				t.Fatalf("original or previous conversion lost: original=%v", originalExists)
			}
			converted := outputs["/remote.php/webdav/source_converted (2).mp4"]
			if len(converted) == 0 {
				t.Fatal("converted output was not saved with an unused name")
			}
			file := filepath.Join(t.TempDir(), "result.mp4")
			if err := os.WriteFile(file, converted, 0600); err != nil {
				t.Fatal(err)
			}
			info, err := probeMedia(context.Background(), file)
			if err != nil || info.BitDepth != "10 bit" {
				t.Fatalf("result lost bit depth: %+v error=%v", info, err)
			}
			entries, err := os.ReadDir(cfg.OutputDir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("temporary work files leaked: %v %v", entries, err)
			}
		})
	}
}
