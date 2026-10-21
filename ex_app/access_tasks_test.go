package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testAuth(userID string) string {
	return base64.StdEncoding.EncodeToString([]byte(userID + ":test-secret"))
}

func testConversionRequest() ConversionRequest {
	return ConversionRequest{
		FileID: "picked", FilePath: "/source.mkv", FileName: "source.mkv",
		Container: "mp4", VideoCodec: "h264", Resolution: "original", QualityCRF: "23",
		HDRMode: "copy", AudioCodec: "aac", BitDepth: "copy", Preset: "medium",
		FPS: "copy", Subtitles: true, Metadata: "copy",
	}
}

func useTestSettings(t *testing.T, settings AppSettings, expires time.Time) {
	t.Helper()
	settingsCache.Lock()
	oldValue, oldExpiry := settingsCache.value, settingsCache.expires
	settingsCache.value, settingsCache.expires = settings, expires
	settingsCache.Unlock()
	t.Cleanup(func() {
		settingsCache.Lock()
		settingsCache.value, settingsCache.expires = oldValue, oldExpiry
		settingsCache.Unlock()
	})
}

func testTask(t *testing.T, userID string, cancel context.CancelFunc) string {
	t.Helper()
	id := newTask(ConversionRequest{UserID: userID, FilePath: "/private/meeting.mp4"}, cancel)
	t.Cleanup(func() {
		taskStore.Lock()
		delete(taskStore.m, id)
		taskStore.Unlock()
	})
	return id
}

func waitForTaskWorker(t *testing.T, id string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if task, ok := taskSnapshot(id); ok && task.cancel == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("task worker did not release its resources")
}

func TestTaskOwnership(t *testing.T) {
	cfg := Config{AppSecret: "test-secret"}
	for _, action := range []string{"read", "cancel"} {
		for _, user := range []string{"alice", "bob", "", "bad-secret"} {
			t.Run(action+"/"+user, func(t *testing.T) {
				var cancelled atomic.Bool
				id := testTask(t, "alice", func() { cancelled.Store(true) })
				method, route := http.MethodGet, "/api/task/"+id
				handler := makeTaskHandler(cfg)
				if action == "cancel" {
					method, route = http.MethodPost, route+"/cancel"
					handler = makeCancelHandler(cfg)
				}
				r := httptest.NewRequest(method, route, nil)
				if user != "" {
					r.Header.Set("AUTHORIZATION-APP-API", testAuth(user))
				}
				if user == "bad-secret" {
					r.Header.Set("AUTHORIZATION-APP-API", base64.StdEncoding.EncodeToString([]byte("alice:forged")))
				}
				w := httptest.NewRecorder()
				handler(w, r)
				want := http.StatusOK
				if user == "bob" {
					want = http.StatusNotFound
				} else if user != "alice" {
					want = http.StatusUnauthorized
				}
				if w.Code != want {
					t.Fatalf("HTTP %d, want %d: %s", w.Code, want, w.Body.String())
				}
				if cancelled.Load() != (action == "cancel" && user == "alice") {
					t.Fatal("cancellation was not restricted to the owner")
				}
			})
		}
	}
}

func TestCompletedTaskCannotBeCancelled(t *testing.T) {
	id := testTask(t, "alice", func() { t.Error("completed task was cancelled") })
	setTaskStatus(id, "Готово", "done", 100)
	r := httptest.NewRequest(http.MethodPost, "/api/task/"+id+"/cancel", nil)
	r.Header.Set("AUTHORIZATION-APP-API", testAuth("alice"))
	w := httptest.NewRecorder()
	makeCancelHandler(Config{AppSecret: "test-secret"})(w, r)
	task, _ := taskSnapshot(id)
	if w.Code != http.StatusConflict || task.Status != "Готово" {
		t.Fatalf("completed task changed: HTTP %d, %+v", w.Code, task)
	}
}

func TestTaskStatusSnapshotsDuringUpdates(t *testing.T) {
	id := testTask(t, "alice", nil)
	setTaskStatus(id, "0", "0", 0)
	handler := makeTaskHandler(Config{AppSecret: "test-secret"})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			value := fmt.Sprint(i)
			setTaskStatus(id, value, value, i%100)
		}
	}()
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				r := httptest.NewRequest(http.MethodGet, "/api/task/"+id, nil)
				r.Header.Set("AUTHORIZATION-APP-API", testAuth("alice"))
				w := httptest.NewRecorder()
				handler(w, r)
				var task Task
				if err := json.Unmarshal(w.Body.Bytes(), &task); err != nil || task.Status != task.Message {
					t.Errorf("inconsistent task response: %s (%v)", w.Body.String(), err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestConvertUsesAuthenticatedIdentity(t *testing.T) {
	request := testConversionRequest()
	body, _ := json.Marshal(struct {
		ConversionRequest
		UserID string `json:"user_id"`
	}{request, "bob"})
	r := httptest.NewRequest(http.MethodPost, "/api/convert", bytes.NewReader(body))
	r.Header.Set("AUTHORIZATION-APP-API", testAuth("alice"))
	w := httptest.NewRecorder()
	makeConvertHandler(Config{AppSecret: "test-secret"})(w, r)
	if w.Code != http.StatusAccepted {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	waitForTaskWorker(t, response.TaskID)
	task, _ := taskSnapshot(response.TaskID)
	if task.UserID != "alice" {
		t.Fatalf("task owned by %q instead of authenticated alice", task.UserID)
	}
}

func TestSettingsFailureDeniesEveryConversionEntryPoint(t *testing.T) {
	for _, upstream := range []struct {
		name   string
		status int
		body   string
	}{
		{"unavailable", 503, "unavailable"},
		{"invalid-json", 200, "not-json"},
		{"ocs-failure", 200, `{"ocs":{"meta":{"status":"failure","statuscode":997},"data":[]}}`},
	} {
		t.Run(upstream.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(upstream.status)
				fmt.Fprint(w, upstream.body)
			}))
			defer server.Close()
			settings := defaultAppSettings()
			settings.AllowedGroups = []string{"editors"}
			useTestSettings(t, settings, time.Time{})
			cfg := Config{NextcloudURL: server.URL, AppID: "converter", AppSecret: "test-secret"}
			conversion, _ := json.Marshal(testConversionRequest())
			for _, entry := range []struct {
				method, path, body string
				handler            http.HandlerFunc
			}{
				{http.MethodPost, "/api/convert", string(conversion), makeConvertHandler(cfg)},
				{http.MethodPost, "/api/metadata", `{"file_path":"/source.mkv"}`, makeMetadataHandler(cfg)},
				{http.MethodPost, "/action/file", `{"fileId":123,"name":"source.mkv","userId":"admin"}`, makeActionHandler(cfg)},
				{http.MethodGet, "/ui/convert.html", "", makeUIHandler(cfg)},
			} {
				r := httptest.NewRequest(entry.method, entry.path, strings.NewReader(entry.body))
				r.Header.Set("AUTHORIZATION-APP-API", testAuth("bob"))
				w := httptest.NewRecorder()
				entry.handler(w, r)
				if w.Code != http.StatusServiceUnavailable {
					t.Errorf("%s returned HTTP %d, want 503: %s", entry.path, w.Code, w.Body.String())
				}
			}
		})
	}
}

func TestPayloadCannotBorrowAnotherUsersGroup(t *testing.T) {
	settings := defaultAppSettings()
	settings.AllowedGroups = []string{"editors"}
	useTestSettings(t, settings, time.Now().Add(time.Hour))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/bob") {
			t.Errorf("looked up an identity from the payload: %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"ocs":{"data":{"groups":["users"]}}}`)
	}))
	defer server.Close()
	cfg := Config{NextcloudURL: server.URL, AppID: "converter", AppSecret: "test-secret"}
	for _, entry := range []struct {
		path, body string
		handler    http.HandlerFunc
	}{
		{"/api/convert", `{"user_id":"admin"}`, makeConvertHandler(cfg)},
		{"/action/file", `{"fileId":123,"userId":"admin"}`, makeActionHandler(cfg)},
	} {
		r := httptest.NewRequest(http.MethodPost, entry.path, strings.NewReader(entry.body))
		r.Header.Set("AUTHORIZATION-APP-API", testAuth("bob"))
		w := httptest.NewRecorder()
		entry.handler(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s: HTTP %d, want 403", entry.path, w.Code)
		}
	}
}

func TestManualAuthentication(t *testing.T) {
	cfg := Config{NextcloudUser: "manual", NextcloudPass: "app-password"}
	r := httptest.NewRequest(http.MethodGet, "/api/task/1", nil)
	r.SetBasicAuth("manual", "app-password")
	if user, err := requestUserID(cfg, r); err != nil || user != "manual" {
		t.Fatalf("valid manual authentication: user=%q error=%v", user, err)
	}
	r.SetBasicAuth("manual", "wrong")
	if _, err := requestUserID(cfg, r); err == nil {
		t.Fatal("invalid manual password accepted")
	}
}

func TestCancellationBeforeWorkerStart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			t.Error("cancelled task downloaded its source")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	g := &jobGate{}
	reservation, _ := g.reserve(defaultAppSettings(), "alice")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := testConversionRequest()
	request.UserID = "alice"
	id := newTask(request, cancel)
	r := httptest.NewRequest(http.MethodPost, "/api/task/"+id+"/cancel", nil)
	r.Header.Set("AUTHORIZATION-APP-API", testAuth("alice"))
	w := httptest.NewRecorder()
	cfg := Config{NextcloudURL: server.URL, AppSecret: "test-secret"}
	makeCancelHandler(cfg)(w, r)
	processTask(cfg, id, request, ctx, cancel, reservation)
	task, _ := taskSnapshot(id)
	if w.Code != http.StatusOK || task.Status != "Ошибка" || !task.cancelled || g.activeTotal != 0 {
		t.Fatalf("cancellation was lost: HTTP %d, task=%+v, active=%d", w.Code, task, g.activeTotal)
	}
}

func TestConvertEndpointQueueLimits(t *testing.T) {
	for _, queueLimit := range []int{0, 1} {
		t.Run(fmt.Sprint("queue=", queueLimit), func(t *testing.T) {
			started := make(chan struct{})
			var once sync.Once
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					once.Do(func() { close(started) })
					<-r.Context().Done()
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(server.Close)
			settings := defaultAppSettings()
			settings.MaxQueuedJobsPerUser = queueLimit
			useTestSettings(t, settings, time.Now().Add(time.Hour))
			oldGate := conversionGate
			conversionGate = &jobGate{}
			t.Cleanup(func() { conversionGate = oldGate })
			cfg := Config{NextcloudURL: server.URL, AppID: "converter", AppSecret: "test-secret", BasePath: "/remote.php/webdav", OutputDir: t.TempDir()}
			handler := makeConvertHandler(cfg)
			body, _ := json.Marshal(testConversionRequest())
			submit := func() (int, string) {
				r := httptest.NewRequest(http.MethodPost, "/api/convert", bytes.NewReader(body))
				r.Header.Set("AUTHORIZATION-APP-API", testAuth("alice"))
				w := httptest.NewRecorder()
				handler(w, r)
				var result struct {
					TaskID string `json:"task_id"`
				}
				_ = json.Unmarshal(w.Body.Bytes(), &result)
				return w.Code, result.TaskID
			}
			var ids []string
			t.Cleanup(func() {
				// Cancel queued work first so it cannot consume the freed worker.
				for i := len(ids) - 1; i >= 0; i-- {
					r := httptest.NewRequest(http.MethodPost, "/api/task/"+ids[i]+"/cancel", nil)
					r.Header.Set("AUTHORIZATION-APP-API", testAuth("alice"))
					makeCancelHandler(cfg)(httptest.NewRecorder(), r)
					waitForTaskWorker(t, ids[i])
				}
			})
			code, id := submit()
			if id != "" {
				ids = append(ids, id)
			}
			if code != http.StatusAccepted {
				t.Fatalf("idle converter rejected job: HTTP %d", code)
			}
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("active job never started downloading")
			}
			var wg sync.WaitGroup
			accepted := make(chan string, 256)
			for i := 0; i < 256; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					code, id := submit()
					if code == http.StatusAccepted {
						accepted <- id
					} else if code != http.StatusTooManyRequests {
						t.Errorf("unexpected HTTP %d", code)
					}
				}()
			}
			wg.Wait()
			close(accepted)
			queued := 0
			for id := range accepted {
				ids = append(ids, id)
				queued++
			}
			if queued != queueLimit {
				t.Fatalf("queue limit %d admitted %d waiting requests", queueLimit, queued)
			}
		})
	}
}
