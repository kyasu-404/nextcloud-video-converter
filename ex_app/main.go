package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed ui/*
var uiFS embed.FS

var (
	validFileID = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	taskCounter uint64

	taskStore = struct {
		sync.RWMutex
		m map[string]*Task
	}{m: make(map[string]*Task)}

	cancelFuncs = struct {
		sync.Mutex
		m map[string]context.CancelFunc
	}{m: make(map[string]context.CancelFunc)}

	globalAppAPIAuth string
	globalAppAPIUser string
	globalAuthMutex  sync.RWMutex
)

func updateAppAPIAuth(auth, userID string) {
	globalAuthMutex.Lock()
	defer globalAuthMutex.Unlock()
	if auth != "" {
		globalAppAPIAuth = auth
	}
	if userID != "" {
		globalAppAPIUser = userID
	}
}

func getAppAPIAuth() string {
	globalAuthMutex.RLock()
	defer globalAuthMutex.RUnlock()
	return globalAppAPIAuth
}

type Config struct {
	Port          string
	NextcloudURL  string
	NextcloudUser string
	NextcloudPass string
	BasePath      string
	OutputDir     string
	InsecureTLS   bool

	AppID      string
	AppSecret  string
	AppVersion string
	AAVersion  string
}

// isHaRPMode returns true when running under HaRP Docker deployment
// (AppAPI injects APP_ID automatically; manual mode uses APP_ID too but
// also sets NEXTCLOUD_USER/NEXTCLOUD_APP_PASSWORD for registration).
func isHaRPMode() bool {
	return os.Getenv("APP_SECRET") != "" && os.Getenv("NEXTCLOUD_URL") != "" && os.Getenv("NEXTCLOUD_USER") == ""
}

type ConversionRequest struct {
	FileID   string `json:"file_id"`
	FilePath string `json:"file_path"`
	FileName string `json:"file_name"`

	Container      string `json:"container"`
	VideoCodec     string `json:"video_codec"`
	Resolution     string `json:"resolution"`
	QualityCRF     string `json:"quality_crf"`
	HDRMode        string `json:"hdr_mode"`
	AudioCodec     string `json:"audio_codec"`
	DeleteOriginal bool   `json:"delete_original"`

	BitDepth     string `json:"bit_depth"`
	AudioBitrate string `json:"audio_bitrate"`
	Preset       string `json:"preset"`
	FPS          string `json:"fps"`
	Subtitles    bool   `json:"subtitles"`
	Metadata     string `json:"metadata"`
	Bitrate      string `json:"bitrate"` // Custom video bitrate
	FastStart    bool   `json:"fast_start"`
	Tonemap      string `json:"tonemap"`

	Cookie       string `json:"-"` // Used for Nextcloud session authentication
	RequestToken string `json:"requesttoken"`
	UserID       string `json:"user_id"`
}

type ActionPayload struct {
	FileID     string `json:"fileId"`
	Name       string `json:"name"`
	Directory  string `json:"directory"`
	Mime       string `json:"mime"`
	FileType   string `json:"fileType"`
	UserID     string `json:"userId"`
	InstanceID string `json:"instanceId"`
}

type MediaInfo struct {
	DurationSeconds float64
	IsHDR           bool
	Transfer        string
	Primaries       string
	Space           string
	PixelFormat     string
	Width           int
	Height          int
	VideoCodec      string
	AudioCodec      string
	Bitrate         int
	Size            int64
}

type Task struct {
	ID           string    `json:"id"`
	Status       string    `json:"status"`
	Progress     int       `json:"progress"`
	Message      string    `json:"message,omitempty"`
	Error        string    `json:"error,omitempty"`
	InputPath    string    `json:"input_path,omitempty"`
	OutputPath   string    `json:"output_path,omitempty"`
	RemoteOutput string    `json:"remote_output,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func main() {
	cfg := loadConfig()

	mux := http.NewServeMux()
	mux.HandleFunc("/heartbeat", heartbeatHandler)
	mux.HandleFunc("/init", initHandler)
	mux.HandleFunc("/enabled", makeEnabledHandler(cfg))
	mux.HandleFunc("/action/file", actionHandler)
	mux.HandleFunc("/ui/convert.html", makeUIHandler(cfg))
	mux.HandleFunc("/ui/app.js", assetHandler("ui/app.js", "application/javascript; charset=utf-8"))
	mux.HandleFunc("/ui/style.css", assetHandler("ui/style.css", "text/css; charset=utf-8"))
	mux.HandleFunc("/ui/icon-white.svg", assetHandler("ui/icon-white.svg", "image/svg+xml"))
	mux.HandleFunc("/ui/icon-black.svg", assetHandler("ui/icon-black.svg", "image/svg+xml"))
	mux.HandleFunc("/ui/init.js", makeInitJSHandler(cfg))
	mux.HandleFunc("/ui/init", makeInitJSHandler(cfg)) // Fallback if Nextcloud didn't append .js
	mux.HandleFunc("/api/metadata", makeMetadataHandler(cfg))
	mux.HandleFunc("/api/convert", makeConvertHandler(cfg))
	mux.HandleFunc("/api/task/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/cancel") {
			makeCancelHandler()(w, r)
			return
		}
		taskHandler(w, r)
	})

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           logging(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("Video Converter ExApp listening on :%s", cfg.Port)
	if cfg.NextcloudURL != "" {
		log.Printf("Nextcloud URL: %s", cfg.NextcloudURL)
	}
	log.Printf("Output dir: %s", cfg.OutputDir)
	if isHaRPMode() {
		log.Println("Режим деплоя: HaRP Docker (UI будет зарегистрирован при PUT /enabled)")
	} else if cfg.NextcloudUser != "" {
		log.Println("Режим деплоя: Manual (регистрация UI при старте)")
		go func() {
			// Даем серверу 2 секунды на полный запуск перед отправкой запроса
			time.Sleep(2 * time.Second)
			if err := registerTopMenu(cfg); err != nil {
				log.Printf("Ошибка регистрации Top Menu: %v", err)
			} else {
				log.Println("Top Menu 'convert' зарегистрирован")
			}
			if err := registerScript(cfg); err != nil {
				log.Printf("Ошибка регистрации скрипта: %v", err)
			} else {
				log.Println("Скрипт init.js зарегистрирован")
			}
			if err := registerFilesAction(cfg); err != nil {
				log.Printf("Ошибка регистрации кнопки в UI: %v", err)
			} else {
				log.Println("Кнопка 'Конвертировать видео' успешно добавлена в Nextcloud!")
			}
		}()
	}

	log.Fatal(srv.ListenAndServe())
}

func loadConfig() Config {
	// APP_PORT is the standard env var injected by AppAPI/HaRP docker-install.
	// PORT is kept as a legacy fallback for manual/dev mode.
	port := env("APP_PORT", env("PORT", "8080"))
	return Config{
		Port:          port,
		NextcloudURL:  strings.TrimRight(env("NEXTCLOUD_URL", ""), "/"),
		NextcloudUser: env("NEXTCLOUD_USER", ""),
		NextcloudPass: env("NEXTCLOUD_APP_PASSWORD", ""),
		// In HaRP mode the WebDAV path uses /remote.php/webdav (cookie auth is used).
		// In manual mode NEXTCLOUD_BASE_PATH can be set explicitly.
		BasePath:    ensureTrailingSlash(env("NEXTCLOUD_BASE_PATH", "/remote.php/webdav")),
		OutputDir:   env("APP_PERSISTENT_STORAGE", env("OUTPUT_DIR", "/tmp")),
		InsecureTLS: envBool("NEXTCLOUD_INSECURE_TLS", false),
		AppID:       env("APP_ID", "video_converter_exapp"),
		AppSecret:   env("APP_SECRET", ""),
		AppVersion:  env("APP_VERSION", "1.0.0"),
		AAVersion:   env("AA_VERSION", "4.0.0"),
	}
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return fallback
	}
}

func ensureTrailingSlash(s string) string {
	if s == "" {
		return "/"
	}
	if !strings.HasSuffix(s, "/") {
		return s + "/"
	}
	return s
}

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Capture the signed AppAPI auth header that Nextcloud sends to us.
		// We re-use the exact same value when calling Nextcloud APIs back.
		auth := r.Header.Get("AUTHORIZATION-APP-API")
		updateAppAPIAuth(auth, "")

		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}

func heartbeatHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"message": "alive",
	})
}

func initHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// ExApp не требует долгой инициализации — сразу сигнализируем успех.
	// AppAPI после этого перейдёт к PUT /enabled.
	writeJSON(w, http.StatusOK, map[string]any{
		"error": "",
	})
}

// makeEnabledHandler returns a handler that:
// - GET  /enabled → возвращает статус включения
// - PUT  /enabled → при enabled=true регистрирует UI-элементы в Nextcloud,
//                    при enabled=false — ничего не делает (AppAPI управляет удалением)
func makeEnabledHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, map[string]any{"error": ""})
			return
		}
		if r.Method != http.MethodPut {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Read the body first so we can parse app_api_user AND enabled.
		// AppAPI may send enabled as bool true/false, int 1/0, or string "1"/"0".
		bodyBytes, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		log.Printf("/enabled PUT: query=%s body=%s auth-present=%v",
			r.URL.RawQuery, string(bodyBytes), r.Header.Get("AUTHORIZATION-APP-API") != "")

		var rawBody map[string]json.RawMessage
		_ = json.Unmarshal(bodyBytes, &rawBody)

		// Extract app_api_user so we know the internal user Nextcloud is using.
		if raw, ok := rawBody["app_api_user"]; ok {
			var userID string
			if err := json.Unmarshal(raw, &userID); err == nil && userID != "" {
				updateAppAPIAuth("", userID)
				log.Printf("/enabled: app_api_user=%s", userID)
			}
		}

		// Determine enabled state: check query param first, then body.
		// Body value may be bool, integer, or string.
		isEnabled := false
		if qv := r.URL.Query().Get("enabled"); qv != "" {
			isEnabled = qv == "1" || qv == "true"
		} else if raw, ok := rawBody["enabled"]; ok {
			// Try bool first
			var b bool
			if err := json.Unmarshal(raw, &b); err == nil {
				isEnabled = b
			} else {
				// Try number
				var n int
				if err := json.Unmarshal(raw, &n); err == nil {
					isEnabled = n != 0
				} else {
					// Try string
					var s string
					if err := json.Unmarshal(raw, &s); err == nil {
						isEnabled = s == "1" || s == "true"
					}
				}
			}
		}

		log.Printf("/enabled: isEnabled=%v", isEnabled)

		if isEnabled && cfg.NextcloudURL != "" && cfg.AppID != "" {
			// AppAPI signals activation — register UI elements.
			log.Println("/enabled: registering UI elements in Nextcloud")
			go func() {
				if err := registerTopMenu(cfg); err != nil {
					log.Printf("Ошибка регистрации Top Menu: %v", err)
				} else {
					log.Println("Top Menu 'convert' зарегистрирован")
				}
				if err := registerScript(cfg); err != nil {
					log.Printf("Ошибка регистрации скрипта: %v", err)
				} else {
					log.Println("Скрипт init.js зарегистрирован")
				}
				if err := registerFilesAction(cfg); err != nil {
					log.Printf("Ошибка регистрации кнопки в UI: %v", err)
				} else {
					log.Println("Кнопка 'Конвертировать видео' успешно добавлена в Nextcloud!")
				}
			}()
		}

		writeJSON(w, http.StatusOK, map[string]any{"error": ""})
	}
}

// registerTopMenu registers a Top Menu entry so that the embedded page
// route (/apps/app_api/embedded/{appId}/{name}/...) can resolve it.
// Without this entry, TopMenuController::viewExAppPage returns 404.
func registerTopMenu(cfg Config) error {
	if cfg.NextcloudURL == "" || cfg.AppID == "" {
		return errors.New("NEXTCLOUD_URL / APP_ID are required")
	}

	payload := map[string]any{
		"name":        "convert",
		"displayName": "Video Converter",
		"icon":        "/ui/icon-white.svg",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	endpoint, err := buildNextcloudAPIURL(cfg, "/apps/app_api/api/v1/ui/top-menu")
	if err != nil {
		return err
	}

	return postOCSJSON(cfg, endpoint, body, true)
}

// registerScript registers a JS file for the top-menu embedded page.
// When the embedded page loads, AppAPI will inject this script, which creates
// an iframe pointing to the ExApp's proxy URL.
func registerScript(cfg Config) error {
	if cfg.NextcloudURL == "" || cfg.AppID == "" {
		return errors.New("NEXTCLOUD_URL / APP_ID are required")
	}

	payload := map[string]any{
		"type": "top_menu",
		"name": "convert",
		"path": "/ui/init", // Nextcloud API automatically appends .js
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	endpoint, err := buildNextcloudAPIURL(cfg, "/apps/app_api/api/v1/ui/script")
	if err != nil {
		return err
	}

	return postOCSJSON(cfg, endpoint, body, true)
}

func registerFilesAction(cfg Config) error {
	if cfg.AppID == "" {
		return errors.New("APP_ID is required")
	}

	payload := map[string]any{
		"name":          "convert_video",
		"displayName":   "Конвертировать видео",
		"mime":          "video",
		"permissions":   31,
		"order":         0,
		"icon":          "/ui/icon-black.svg",
		"actionHandler": "/action/file",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	endpoint, err := buildNextcloudAPIURL(cfg, "/apps/app_api/api/v2/ui/files-actions-menu")
	if err != nil {
		return err
	}

	if err := postOCSJSON(cfg, endpoint, body, true); err != nil {
		fallback, fbErr := buildNextcloudAPIURL(cfg, "/apps/app_api/api/v1/ui/files-actions-menu")
		if fbErr != nil {
			return err
		}
		if fbPostErr := postOCSJSON(cfg, fallback, body, true); fbPostErr != nil {
			return fmt.Errorf("v2 registration failed: %v; v1 fallback failed: %v", err, fbPostErr)
		}
	}

	return nil
}

// actionHandler receives the file action context forwarded by AppAPI.
// It returns a redirect handler pointing to the UI page that can render the modal/page.
var fileInfoCache sync.Map

func actionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Читаем тело запроса целиком (полезно для отладки)
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "body too large", http.StatusBadRequest)
		return
	}

	// Используем map[string]any, чтобы съесть и числа, и строки без паники
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		log.Printf("ERROR parsing file action JSON: %v. Body: %s", err, string(body))
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	// Поддержка v2 payload (где данные приходят в массиве files)
	if files, ok := payload["files"].([]any); ok && len(files) > 0 {
		if fileObj, ok := files[0].(map[string]any); ok {
			payload = fileObj
		}
	}

	// Безопасно конвертируем числовой fileId в строку
	fileID := fmt.Sprintf("%v", payload["fileId"])
	if fileID == "<nil>" || fileID == "" {
		log.Printf("ERROR: fileId is missing from payload. Body: %s", string(body))
		http.Error(w, "fileId is missing", http.StatusBadRequest)
		return
	}

	fileName := "video"
	if name, ok := payload["name"].(string); ok && name != "" {
		fileName = name
	}

	// Nextcloud иногда может присылать dir вместо directory, страхуемся
	directory := "/"
	if dir, ok := payload["directory"].(string); ok && dir != "" {
		directory = dir
	} else if dir, ok := payload["dir"].(string); ok && dir != "" {
		directory = dir
	}

	relativePath := buildRelativePath(directory, fileName)

	// Cache the file info for the UI handler
	fileInfoCache.Store(fileID, map[string]string{
		"file_name": fileName,
		"file_path": relativePath,
	})

	log.Printf("File action triggered: %s (ID: %s)", relativePath, fileID)

	// redirect_handler must be just the top-menu entry name (no leading slash,
	// no query params). The JS appends "?fileIds=..." automatically.
	// The embedded page will be served at:
	//   /apps/app_api/embedded/{appId}/convert?fileIds=...
	// which loads the ExApp's /ui/convert page in an iframe via the proxy.
	writeJSON(w, http.StatusOK, map[string]any{
		"redirect_handler": "convert",
	})
}

// makeInitJSHandler returns a handler that serves a small JS bootstrap script.
//   - File action context (fileIds present): opens the converter in a new browser tab
//     and redirects the current page back to Files, so the user stays in Files.
//   - Top menu context (no fileIds): embeds the converter inline in the page content area.
func makeInitJSHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		js := `(function() {
  var appId = ` + fmt.Sprintf("%q", cfg.AppID) + `;
  var params = new URLSearchParams(window.location.search);
  var fileIds = params.get('fileIds') || '';

  var el = document.getElementById('content');
  if (!el) return;
  el.style.padding = '0';
  el.style.margin = '0';
  el.style.minHeight = '100%';
  el.style.height = '100%';
  el.style.overflowY = 'auto';
  el.style.overflowX = 'hidden';
  el.style.webkitOverflowScrolling = 'touch';
  
  // Build the proxy URL using Nextcloud's built-in OC.generateUrl
  var proxyPath = '/apps/app_api/proxy/' + appId + '/ui/convert.html';
  var finalUrl = window.OC.generateUrl(proxyPath);
  if (fileIds) {
    finalUrl += '?fileIds=' + encodeURIComponent(fileIds);
  }

  fetch(finalUrl).then(function(res) {
    if (!res.ok) throw new Error('HTTP ' + res.status);
    return res.text();
  }).then(function(html) {
    var proxyUiPath = window.OC.generateUrl('/apps/app_api/proxy/' + appId);
    html = html.replace(/\{\{PROXY_BASE\}\}/g, proxyUiPath);

    var doc = new DOMParser().parseFromString(html, 'text/html');
    doc.querySelectorAll('link[rel="stylesheet"]').forEach(function(link) {
      var href = link.getAttribute('href');
      if (!href || document.querySelector('link[href="' + href + '"]')) return;
      var newLink = document.createElement('link');
      newLink.rel = 'stylesheet';
      newLink.href = href;
      document.head.appendChild(newLink);
    });

    el.innerHTML = doc.body ? doc.body.innerHTML : html;

    // Scripts injected via innerHTML don't execute automatically
    var scripts = el.querySelectorAll('script');
    scripts.forEach(function(s) {
      var newScript = document.createElement('script');
      if (s.src) {
        newScript.src = s.src;
      } else {
        newScript.innerHTML = s.innerHTML;
      }
      document.body.appendChild(newScript);
      s.parentNode.removeChild(s);
    });
  }).catch(function(err) {
    console.error('Failed to load Video Converter UI:', err);
    el.innerHTML = '<div style="padding: 20px; color: red; font-family: sans-serif;">' +
                   'Failed to load Video Converter UI: ' + err.message + '</div>';
  });
})();
`
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		_, _ = w.Write([]byte(js))
	}
}

func makeUIHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		b, err := uiFS.ReadFile("ui/index.html")
		if err != nil {
			http.Error(w, "ui not found", http.StatusInternalServerError)
			return
		}

		fileID := escapeAttr(r.URL.Query().Get("file_id"))
		// Fallback: fileIds from the embedded redirect
		if fileID == "" {
			fileID = escapeAttr(r.URL.Query().Get("fileIds"))
		}

		fileName := escapeAttr(r.URL.Query().Get("file_name"))
		filePath := escapeAttr(r.URL.Query().Get("file_path"))

		// Restore missing file details from cache
		if (fileName == "" || filePath == "") && fileID != "" {
			if cachedInfo, ok := fileInfoCache.Load(fileID); ok {
				if infoMap, ok := cachedInfo.(map[string]string); ok {
					fileName = escapeAttr(infoMap["file_name"])
					filePath = escapeAttr(infoMap["file_path"])
				}
			}
		}

		// We leave {{PROXY_BASE}} intact so that init.js can replace it dynamically
		// with the exact absolute URL of the proxy, avoiding 404 errors.
		// Cache busting for app.js and style.css so the user gets the latest UI
		page := strings.ReplaceAll(string(b), "{{FILE_ID}}", fileID)
		page = strings.ReplaceAll(page, "{{FILE_NAME}}", fileName)
		page = strings.ReplaceAll(page, "{{FILE_PATH}}", filePath)
		page = strings.ReplaceAll(page, "app.js", "app.js?v="+strconv.FormatInt(time.Now().Unix(), 10))
		page = strings.ReplaceAll(page, "style.css", "style.css?v="+strconv.FormatInt(time.Now().Unix(), 10))

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		_, _ = w.Write([]byte(page))
	}
}

func assetHandler(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		b, err := uiFS.ReadFile("ui/" + filepath.Base(name))
		if err != nil {
			http.Error(w, "asset not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "public, max-age=31536000")
		_, _ = w.Write(b)
	}
}

func makeConvertHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req ConversionRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}

		// Save the user's session cookie for WebDAV authentication
		req.Cookie = r.Header.Get("Cookie")

		if err := validateRequest(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}

		taskID := newTask(req)
		writeJSON(w, http.StatusAccepted, map[string]any{
			"status":  "processing",
			"message": "task queued",
			"task_id": taskID,
		})

		go processTask(cfg, taskID, req)
	}
}

func makeMetadataHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			FilePath string `json:"file_path"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}

		if req.FilePath == "" {
			http.Error(w, "file_path is required", http.StatusBadRequest)
			return
		}

		cookie := r.Header.Get("Cookie")

		info, err := probeRemoteMedia(r.Context(), cfg, cookie, req.FilePath)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, info)
	}
}

func taskHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/task/")
	if id == "" {
		http.Error(w, "task id required", http.StatusBadRequest)
		return
	}

	taskStore.RLock()
	task, ok := taskStore.m[id]
	taskStore.RUnlock()
	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	writeJSON(w, http.StatusOK, task)
}

func makeCancelHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/task/")
		id = strings.TrimSuffix(id, "/cancel")
		if id == "" {
			http.Error(w, "task id required", http.StatusBadRequest)
			return
		}

		cancelFuncs.Lock()
		if cancel, ok := cancelFuncs.m[id]; ok {
			cancel()
			delete(cancelFuncs.m, id)
		}
		cancelFuncs.Unlock()

		setTaskError(id, "Отменено пользователем")
		writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
	}
}

func newTask(req ConversionRequest) string {
	id := fmt.Sprintf("task-%d", atomic.AddUint64(&taskCounter, 1))
	task := &Task{
		ID:        id,
		Status:    "В очереди",
		Progress:  0,
		Message:   "Добавлено в очередь",
		InputPath: req.FilePath,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	taskStore.Lock()
	taskStore.m[id] = task
	taskStore.Unlock()

	return id
}

func updateTask(id string, fn func(t *Task)) {
	taskStore.Lock()
	defer taskStore.Unlock()
	if t, ok := taskStore.m[id]; ok {
		fn(t)
		t.UpdatedAt = time.Now()
	}
}

func setTaskError(id, msg string) {
	updateTask(id, func(t *Task) {
		t.Status = "Ошибка"
		t.Error = msg
		t.Message = msg
	})
}

func setTaskStatus(id, status, msg string, progress int) {
	updateTask(id, func(t *Task) {
		t.Status = status
		t.Message = msg
		if progress >= 0 {
			t.Progress = progress
		}
	})
}

var conversionSemaphore = make(chan struct{}, 1)

func processTask(cfg Config, taskID string, req ConversionRequest) {
	ctx, cancel := context.WithCancel(context.Background())
	cancelFuncs.Lock()
	cancelFuncs.m[taskID] = cancel
	cancelFuncs.Unlock()

	defer func() {
		cancelFuncs.Lock()
		delete(cancelFuncs.m, taskID)
		cancelFuncs.Unlock()
		cancel()
		if r := recover(); r != nil {
			setTaskError(taskID, fmt.Sprintf("panic: %v", r))
		}

		taskStore.RLock()
		task, ok := taskStore.m[taskID]
		taskStore.RUnlock()
		if ok && req.UserID != "" {
			if task.Status == "Ошибка" {
				sendNotification(cfg, req.UserID, "Ошибка конвертации", task.Error)
			} else if task.Status == "Готово" {
				sendNotification(cfg, req.UserID, "Конвертация успешно завершена", "Файл "+req.FileName+" сконвертирован")
			}
		}
	}()

	setTaskStatus(taskID, "В очереди", "Ожидание своей очереди", 0)

	// Acquire semaphore to limit to 1 concurrent conversion
	select {
	case conversionSemaphore <- struct{}{}:
		defer func() { <-conversionSemaphore }()
	case <-ctx.Done():
		setTaskError(taskID, "Отменено пользователем")
		return
	}

	setTaskStatus(taskID, "Скачивание", "Загрузка файла с сервера...", 5)

	// Notify user that conversion has started
	if req.UserID != "" {
		sendNotification(cfg, req.UserID, "Конвертация начата",
			"Файл "+req.FileName+" поставлен в очередь на конвертацию")
	}

	if cfg.NextcloudURL == "" {
		setTaskError(taskID, "NEXTCLOUD_URL is required")
		return
	}

	client := newHTTPClient(cfg.InsecureTLS)

	tmpDir := cfg.OutputDir
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		setTaskError(taskID, "cannot create output dir: "+err.Error())
		return
	}

	sourceExt := filepath.Ext(req.FileName)
	if sourceExt == "" {
		sourceExt = ".input"
	}
	inFile, err := os.CreateTemp(tmpDir, "vc-input-*"+sourceExt)
	if err != nil {
		setTaskError(taskID, "cannot create temp input: "+err.Error())
		return
	}
	inPath := inFile.Name()
	inFile.Close()
	defer os.Remove(inPath)

	if err := downloadWebDAV(ctx, client, cfg, req.Cookie, req.FilePath, inPath, taskID); err != nil {
		if errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "context canceled") {
			setTaskError(taskID, "Отменено пользователем")
		} else {
			setTaskError(taskID, "Ошибка скачивания: "+err.Error())
		}
		return
	}

	info, err := probeMedia(ctx, inPath)
	if err != nil {
		setTaskError(taskID, "ffprobe failed: "+err.Error())
		return
	}

	outExt := "." + strings.TrimPrefix(req.Container, ".")
	outFile, err := os.CreateTemp(tmpDir, "vc-output-*"+outExt)
	if err != nil {
		setTaskError(taskID, "cannot create temp output: "+err.Error())
		return
	}
	outPath := outFile.Name()
	outFile.Close()
	defer os.Remove(outPath)

	setTaskStatus(taskID, "Подготовка", "Анализ видео...", 41)

	args, err := buildFFmpegArgs(req, info, inPath, outPath)
	if err != nil {
		setTaskError(taskID, err.Error())
		return
	}

	if err := runFFmpeg(taskID, ctx, args, info.DurationSeconds, cfg, req.UserID, req.FileName); err != nil {
		if errors.Is(ctx.Err(), context.Canceled) || strings.Contains(err.Error(), "context canceled") {
			setTaskError(taskID, "Отменено пользователем")
		} else {
			setTaskError(taskID, "Ошибка ffmpeg: "+err.Error())
		}
		return
	}

	setTaskStatus(taskID, "Загрузка", "Сохранение готового файла в облако...", 90)
	remoteOut := buildRemoteOutputPath(req.FilePath, req.FileName, req.Container)
	if err := uploadWebDAV(ctx, client, cfg, req.Cookie, req.RequestToken, remoteOut, outPath); err != nil {
		if errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "context canceled") {
			setTaskError(taskID, "Отменено пользователем")
		} else {
			setTaskError(taskID, "Ошибка выгрузки: "+err.Error())
		}
		return
	}

	if req.DeleteOriginal {
		setTaskStatus(taskID, "Завершение", "Удаление оригинала", 99)
		if err := deleteWebDAV(ctx, client, cfg, req.Cookie, req.RequestToken, req.FilePath); err != nil {
			setTaskError(taskID, "delete original failed: "+err.Error())
			return
		}
	}

	updateTask(taskID, func(t *Task) {
		t.Status = "Готово"
		t.Progress = 100
		t.Message = "Конвертация успешно завершена"
		t.OutputPath = remoteOut
		t.RemoteOutput = remoteOut
	})
}

func runFFmpeg(taskID string, ctx context.Context, args []string, durationSeconds float64, cfg Config, userID, fileName string) error {
	if durationSeconds <= 0 {
		durationSeconds = 1
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	var (
		stderrBuf bytes.Buffer
		wg        sync.WaitGroup
	)

	if err := cmd.Start(); err != nil {
		return err
	}

	progressCh := make(chan int, 32)
	doneCh := make(chan error, 1)

	wg.Add(2)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			line := sc.Text()
			// ffmpeg -progress pipe:1 emits:
			//   out_time_us=<microseconds>  (always microseconds despite name)
			//   out_time_ms=<microseconds>  (same value, despite "ms" name - ffmpeg quirk)
			//   out_time=HH:MM:SS.XXXXXX   (human-readable)
			var seconds float64
			parsed := false
			switch {
			case strings.HasPrefix(line, "out_time_us="):
				raw := strings.TrimSpace(strings.TrimPrefix(line, "out_time_us="))
				if us, err := strconv.ParseFloat(raw, 64); err == nil && us > 0 {
					seconds = us / 1_000_000
					parsed = true
				}
			case strings.HasPrefix(line, "out_time_ms="):
				// Despite its name, ffmpeg reports this in microseconds too
				raw := strings.TrimSpace(strings.TrimPrefix(line, "out_time_ms="))
				if us, err := strconv.ParseFloat(raw, 64); err == nil && us > 0 {
					seconds = us / 1_000_000
					parsed = true
				}
			case strings.HasPrefix(line, "out_time="):
				// Format: HH:MM:SS.XXXXXX
				raw := strings.TrimSpace(strings.TrimPrefix(line, "out_time="))
				if raw != "N/A" && raw != "" {
					seconds = parseTime(raw)
					if seconds > 0 {
						parsed = true
					}
				}
			}
			if parsed && seconds > 0 {
				// Map 0-100% of video to UI progress range 42-89%
				// so there's no jump between download (5-40%) and upload (90%)
				rawPct := (seconds / durationSeconds) * 100
				pct := 42 + int(rawPct*47/100) // 42 + (0..47)
				if pct < 42 {
					pct = 42
				}
				if pct > 89 {
					pct = 89
				}
				select {
				case progressCh <- pct:
				default:
				}
			}
			if strings.HasPrefix(line, "progress=end") {
				select {
				case progressCh <- 99:
				default:
				}
			}
		}
		close(progressCh)
	}()

	go func() {
		defer wg.Done()
		_, _ = io.Copy(&stderrBuf, stderr)
	}()

	go func() {
		doneCh <- cmd.Wait()
	}()

	lastProgress := 0
	notifiedHalf := false
	for progressCh != nil || doneCh != nil {
		select {
		case p, ok := <-progressCh:
			if !ok {
				progressCh = nil
				continue
			}
			if p > lastProgress {
				lastProgress = p
				setTaskStatus(taskID, "Конвертация", "Работает ffmpeg", p)
				// Send a mid-progress notification once at ~50% of video (p ≈ 65 in 42-89 scale)
				if !notifiedHalf && p >= 65 && userID != "" {
					notifiedHalf = true
					go sendNotification(cfg, userID, "Половина пути",
						"Конвертация файла "+fileName+" выполнена примерно на 50%")
				}
			}
		case err := <-doneCh:
			doneCh = nil
			wg.Wait()
			if err != nil {
				msg := strings.TrimSpace(stderrBuf.String())
				if msg == "" {
					return err
				}
				return fmt.Errorf("%w: %s", err, msg)
			}
			return nil
		}
	}
	wg.Wait()
	return nil
}

func buildFFmpegArgs(req ConversionRequest, info MediaInfo, inPath, outPath string) ([]string, error) {
	args := []string{"-y", "-hide_banner", "-nostats", "-v", "error", "-progress", "pipe:1", "-i", inPath}

	// Map only the streams we actually need.
	args = append(args, "-map", "0:v:0")
	if req.AudioCodec != "" {
		args = append(args, "-map", "0:a?")
	}
	if req.Subtitles {
		args = append(args, "-map", "0:s?")
	}

	// Metadata handling
	if req.Metadata == "remove" {
		args = append(args, "-map_metadata", "-1", "-map_chapters", "-1")
	} else {
		args = append(args, "-map_metadata", "0", "-map_chapters", "0")
	}

	// Video codec
	if req.VideoCodec == "copy" {
		args = append(args, "-c:v", "copy")
	} else {
		enc := map[string]string{
			"h264": "libx264",
			"h265": "libx265",
			"av1":  "libsvtav1",
		}[req.VideoCodec]
		if enc == "" {
			return nil, errors.New("unsupported video codec")
		}
		args = append(args, "-c:v", enc)

		if preset := encoderPreset(req.VideoCodec, req.Preset); preset != "" {
			args = append(args, "-preset", preset)
		}

		if req.Bitrate != "" && req.QualityCRF == "bitrate" {
			args = append(args, "-b:v", req.Bitrate+"k")
		} else {
			crf := qualityToCRF(req.VideoCodec, req.QualityCRF)
			args = append(args, "-crf", crf)
		}

		videoFilters := make([]string, 0, 3)

		if target := targetScaleFilter(req.Resolution); target != "" {
			videoFilters = append(videoFilters, target)
		}

		// If source is HDR and user wants SDR, tone-map to Rec.709.
		if info.IsHDR && req.HDRMode == "sdr" {
			tonemap := "hable"
			if req.Tonemap != "" && req.Tonemap != "none" {
				tonemap = req.Tonemap
			}

			if req.Tonemap == "none" {
				videoFilters = append(videoFilters,
					"zscale=t=linear:npl=100",
					"format=gbrpf32le",
					"zscale=p=bt709",
					"zscale=t=bt709:m=bt709:r=tv",
					"format=yuv420p",
				)
			} else {
				videoFilters = append(videoFilters,
					"zscale=t=linear:npl=100",
					"format=gbrpf32le",
					"zscale=p=bt709",
					"tonemap=tonemap="+tonemap+":desat=0",
					"zscale=t=bt709:m=bt709:r=tv",
					"format=yuv420p",
				)
			}
		} else {
			if req.BitDepth == "10" || req.HDRMode == "hdr" {
				videoFilters = append(videoFilters, "format=yuv420p10le")
			} else {
				videoFilters = append(videoFilters, "format=yuv420p")
			}
		}

		if len(videoFilters) > 0 {
			args = append(args, "-vf", strings.Join(videoFilters, ","))
		}

		if req.BitDepth == "10" || req.HDRMode == "hdr" {
			if info.IsHDR {
				primaries, trc, space := hdrColorTags(info)
				args = append(args,
					"-pix_fmt", "yuv420p10le",
					"-color_primaries", primaries,
					"-color_trc", trc,
					"-colorspace", space,
				)
			} else {
				args = append(args, "-pix_fmt", "yuv420p10le")
			}
		} else {
			args = append(args, "-pix_fmt", "yuv420p")
		}
	}

	// FPS
	if req.FPS != "" && req.FPS != "copy" {
		args = append(args, "-r", req.FPS)
	}

	// Audio codec
	switch req.AudioCodec {
	case "", "copy":
		args = append(args, "-c:a", "copy")
	case "aac":
		args = append(args, "-c:a", "aac")
		if req.AudioBitrate != "" {
			args = append(args, "-b:a", req.AudioBitrate+"k")
		}
	case "ac3":
		args = append(args, "-c:a", "ac3")
		if req.AudioBitrate != "" {
			args = append(args, "-b:a", req.AudioBitrate+"k")
		}
	default:
		return nil, errors.New("unsupported audio codec")
	}

	// Subtitle codec
	if req.Subtitles {
		if req.Container == "mkv" {
			args = append(args, "-c:s", "copy")
		} else {
			args = append(args, "-c:s", "mov_text")
		}
	} else {
		args = append(args, "-sn")
	}

	if (req.Container == "mp4" || req.Container == "mov") && req.FastStart {
		args = append(args, "-movflags", "+faststart")
	}

	args = append(args, outPath)
	return args, nil
}

func qualityToCRF(codec, quality string) string {
	table := map[string]map[string]string{
		"h264": {
			"low": "28", "normal": "23", "high": "18", "very_high": "14",
		},
		"h265": {
			"low": "34", "normal": "28", "high": "22", "very_high": "18",
		},
		"av1": {
			"low": "48", "normal": "40", "high": "32", "very_high": "26",
		},
	}
	if v, ok := table[codec][quality]; ok {
		return v
	}
	return map[string]string{"h264": "23", "h265": "28", "av1": "40"}[codec]
}

func encoderPreset(codec, uiPreset string) string {
	switch codec {
	case "av1":
		switch uiPreset {
		case "fast":
			return "8"
		case "medium":
			return "6"
		case "slow":
			return "4"
		default:
			return "6"
		}
	default:
		if uiPreset == "" {
			return "medium"
		}
		return uiPreset
	}
}

func targetScaleFilter(res string) string {
	switch res {
	case "1080p":
		return "scale='min(1920,iw)':-2"
	case "720p":
		return "scale='min(1280,iw)':-2"
	case "4k":
		return "scale='min(3840,iw)':-2"
	default:
		return ""
	}
}

func hdrColorTags(info MediaInfo) (primaries, trc, space string) {
	primaries = "bt2020"
	space = "bt2020nc"
	switch strings.ToLower(info.Transfer) {
	case "arib-std-b67":
		trc = "arib-std-b67"
	case "smpte2084":
		trc = "smpte2084"
	default:
		trc = "smpte2084"
	}
	if info.Primaries != "" {
		primaries = info.Primaries
	}
	if info.Space != "" {
		space = info.Space
	}
	return
}

func probeMedia(ctx context.Context, path string) (MediaInfo, error) {
	var info MediaInfo

	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "quiet", "-print_format", "json", "-show_format", "-show_streams", path)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return info, err
	}

	var data struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			CodecType      string            `json:"codec_type"`
			ColorTransfer  string            `json:"color_transfer"`
			ColorPrimaries string            `json:"color_primaries"`
			ColorSpace     string            `json:"color_space"`
			Width          int               `json:"width"`
			Height         int               `json:"height"`
			PixFmt         string            `json:"pix_fmt"`
			Duration       string            `json:"duration"`
			Tags           map[string]string `json:"tags"`
		} `json:"streams"`
	}

	if err := json.Unmarshal(out.Bytes(), &data); err != nil {
		return info, err
	}
	if d, err := strconv.ParseFloat(data.Format.Duration, 64); err == nil {
		info.DurationSeconds = d
	}

	for _, s := range data.Streams {
		if s.CodecType != "video" {
			continue
		}
		if info.DurationSeconds <= 0 {
			if s.Duration != "" {
				if d, err := strconv.ParseFloat(s.Duration, 64); err == nil {
					info.DurationSeconds = d
				}
			} else if durStr, ok := s.Tags["DURATION"]; ok {
				info.DurationSeconds = parseTime(durStr)
			}
		}
		info.Transfer = s.ColorTransfer
		info.Primaries = s.ColorPrimaries
		info.Space = s.ColorSpace
		info.PixelFormat = s.PixFmt
		info.Width = s.Width
		info.Height = s.Height

		t := strings.ToLower(s.ColorTransfer)
		p := strings.ToLower(s.ColorPrimaries)
		c := strings.ToLower(s.ColorSpace)
		if t == "smpte2084" || t == "arib-std-b67" || p == "bt2020" || strings.Contains(c, "2020") {
			info.IsHDR = true
		}
		break
	}
	return info, nil
}

func downloadWebDAV(ctx context.Context, client *http.Client, cfg Config, cookie, remotePath, localPath string, taskID string) error {
	u, err := buildWebDAVURL(cfg, remotePath)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	} else if cfg.NextcloudUser != "" && cfg.NextcloudPass != "" {
		req.SetBasicAuth(cfg.NextcloudUser, cfg.NextcloudPass)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("webdav GET %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	f, err := os.Create(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	// Track download progress if Content-Length is known
	contentLength := resp.ContentLength
	if contentLength > 0 && taskID != "" {
		pr := &progressReader{
			r:       resp.Body,
			total:   contentLength,
			taskID:  taskID,
			minPct:  5,
			maxPct:  40,
			lastPct: 5,
		}
		_, err = io.Copy(f, pr)
	} else {
		_, err = io.Copy(f, resp.Body)
	}
	return err
}

// progressReader wraps an io.Reader and updates task progress during copy.
type progressReader struct {
	r       io.Reader
	total   int64
	read    int64
	taskID  string
	minPct  int
	maxPct  int
	lastPct int
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	if n > 0 {
		pr.read += int64(n)
		pct := pr.minPct + int(float64(pr.read)/float64(pr.total)*float64(pr.maxPct-pr.minPct))
		if pct > pr.maxPct {
			pct = pr.maxPct
		}
		if pct > pr.lastPct {
			pr.lastPct = pct
			setTaskStatus(pr.taskID, "Скачивание", fmt.Sprintf("Загрузка файла... %d%%", pct), pct)
		}
	}
	return n, err
}

func uploadWebDAV(ctx context.Context, client *http.Client, cfg Config, cookie, requestToken, remotePath, localPath string) error {
	u, err := buildWebDAVURL(cfg, remotePath)
	if err != nil {
		return err
	}

	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, f)
	if err != nil {
		return err
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
		if requestToken != "" {
			req.Header.Set("requesttoken", requestToken)
		}
	} else if cfg.NextcloudUser != "" && cfg.NextcloudPass != "" {
		req.SetBasicAuth(cfg.NextcloudUser, cfg.NextcloudPass)
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("webdav PUT %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

func deleteWebDAV(ctx context.Context, client *http.Client, cfg Config, cookie, requestToken, remotePath string) error {
	u, err := buildWebDAVURL(cfg, remotePath)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return err
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
		if requestToken != "" {
			req.Header.Set("requesttoken", requestToken)
		}
	} else if cfg.NextcloudUser != "" && cfg.NextcloudPass != "" {
		req.SetBasicAuth(cfg.NextcloudUser, cfg.NextcloudPass)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("webdav DELETE %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

func buildNextcloudAPIURL(cfg Config, endpoint string) (string, error) {
	if cfg.NextcloudURL == "" {
		return "", errors.New("NEXTCLOUD_URL is required")
	}
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "", errors.New("endpoint is empty")
	}
	if !strings.HasPrefix(endpoint, "/") {
		endpoint = "/" + endpoint
	}

	u, err := url.Parse(cfg.NextcloudURL)
	if err != nil {
		return "", err
	}
	u.Path = joinURLPath(u.Path, endpoint)
	return u.String(), nil
}

func postOCSJSON(cfg Config, endpoint string, body []byte, useAppAPIAuth bool) error {
	client := newHTTPClient(cfg.InsecureTLS)
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("OCS-APIRequest", "true")
	if cfg.NextcloudUser != "" && cfg.NextcloudPass != "" {
		req.SetBasicAuth(cfg.NextcloudUser, cfg.NextcloudPass)
	}
	if useAppAPIAuth {
		if cfg.AppID == "" || cfg.AppVersion == "" || cfg.AAVersion == "" {
			return errors.New("APP_ID / APP_VERSION / AA_VERSION are required for AppAPI auth")
		}
		authValue := getAppAPIAuth()
		if authValue == "" {
			return errors.New("AppAPI auth required but AUTHORIZATION-APP-API header was not received yet")
		}
		req.Header.Set("AA-VERSION", cfg.AAVersion)
		req.Header.Set("EX-APP-ID", cfg.AppID)
		req.Header.Set("EX-APP-VERSION", cfg.AppVersion)
		req.Header.Set("AUTHORIZATION-APP-API", authValue)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}
	return nil
}
func buildWebDAVURL(cfg Config, remotePath string) (string, error) {
	if cfg.NextcloudURL == "" {
		return "", errors.New("NEXTCLOUD_URL is required")
	}
	rel, err := cleanRemotePath(remotePath)
	if err != nil {
		return "", err
	}

	base := strings.TrimRight(cfg.BasePath, "/")
	if base == "" || strings.Contains(base, "/dav/files/converter") {
		base = "/remote.php/webdav"
	}
	fullPath := base + "/" + rel

	u, err := url.Parse(cfg.NextcloudURL)
	if err != nil {
		return "", err
	}
	u.Path = joinURLPath(u.Path, fullPath)
	return u.String(), nil
}

func joinURLPath(base, add string) string {
	if base == "" {
		base = "/"
	}
	if !strings.HasPrefix(base, "/") {
		base = "/" + base
	}
	if !strings.HasPrefix(add, "/") {
		add = "/" + add
	}
	return path.Clean(base + "/" + strings.TrimPrefix(add, "/"))
}

func cleanRemotePath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", errors.New("remote path is empty")
	}
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimPrefix(p, "/")
	p = path.Clean("/" + p)
	p = strings.TrimPrefix(p, "/")
	if p == "." || p == "/" || p == "" {
		return "", errors.New("remote path is invalid")
	}
	if strings.HasPrefix(p, "..") {
		return "", errors.New("remote path traversal detected")
	}
	return p, nil
}

func buildRelativePath(directory, name string) string {
	dir := strings.TrimSpace(directory)
	dir = strings.ReplaceAll(dir, "\\", "/")
	dir = strings.TrimPrefix(dir, "/")
	dir = path.Clean("/" + dir)
	dir = strings.TrimPrefix(dir, "/")
	if dir == "." || dir == "" {
		return "/" + name
	}
	return "/" + path.Join(dir, name)
}

func buildRemoteOutputPath(sourcePath, fileName, container string) string {
	dir := path.Dir(strings.ReplaceAll(sourcePath, "\\", "/"))
	if dir == "." {
		dir = ""
	}
	base := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	if base == "" {
		base = "video"
	}
	outName := sanitizeFilename(base) + "_converted." + strings.TrimPrefix(container, ".")
	if dir == "" || dir == "/" {
		return "/" + outName
	}
	return path.Join("/", dir, outName)
}

func sanitizeFilename(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	if s == "" || s == "." || s == ".." {
		return "video"
	}
	return s
}

func validateRequest(req *ConversionRequest) error {
	if !validFileID.MatchString(req.FileID) {
		return errors.New("invalid file_id")
	}
	if req.FilePath == "" {
		return errors.New("file_path is required")
	}

	// Normalize and validate basic enumerations.
	validContainers := map[string]bool{"mp4": true, "mkv": true, "mov": true}
	validVideoCodecs := map[string]bool{"h264": true, "h265": true, "av1": true, "copy": true}
	validResolutions := map[string]bool{"original": true, "1080p": true, "720p": true, "4k": true}
	validHDR := map[string]bool{"sdr": true, "hdr": true, "copy": true}
	validAudio := map[string]bool{"aac": true, "ac3": true, "opus": true, "mp3": true, "flac": true, "copy": true}
	validBitDepth := map[string]bool{"8": true, "10": true, "copy": true}
	validPreset := map[string]bool{"fast": true, "medium": true, "slow": true}
	validFPS := map[string]bool{"copy": true}
	validMetadata := map[string]bool{"copy": true, "remove": true}

	if req.FileName == "" && req.FilePath != "" {
		req.FileName = filepath.Base(req.FilePath)
	}

	switch {
	case !validContainers[req.Container]:
		return errors.New("unsupported container")
	case !validVideoCodecs[req.VideoCodec]:
		return errors.New("unsupported video codec")
	case !validResolutions[req.Resolution]:
		return errors.New("unsupported resolution")
	case !validHDR[req.HDRMode]:
		return errors.New("unsupported hdr mode")
	case !validAudio[req.AudioCodec]:
		return errors.New("unsupported audio codec")
	case !validBitDepth[req.BitDepth]:
		return errors.New("unsupported bit depth")
	case !validPreset[req.Preset]:
		return errors.New("unsupported preset")
	case !validMetadata[req.Metadata]:
		return errors.New("unsupported metadata mode")
	}

	if req.QualityCRF != "bitrate" {
		crf, err := strconv.ParseFloat(req.QualityCRF, 64)
		if err != nil || crf < 0 || crf > 51 {
			return errors.New("unsupported quality crf")
		}
	}

	if !validFPS[req.FPS] {
		fps, err := strconv.ParseFloat(req.FPS, 64)
		if err != nil || fps <= 0 || fps > 240 {
			return errors.New("unsupported fps")
		}
	}

	if req.AudioBitrate != "" {
		bitrate, err := strconv.Atoi(req.AudioBitrate)
		if err != nil || bitrate <= 0 || bitrate > 1000 {
			return errors.New("audio_bitrate must be a positive number")
		}
	}

	if req.Bitrate != "" {
		bitrate, err := strconv.Atoi(req.Bitrate)
		if err != nil || bitrate <= 0 || bitrate > 1000000 {
			return errors.New("bitrate must be a positive number")
		}
	}

	return nil
}

func newHTTPClient(insecure bool) *http.Client {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure}, // #nosec G402 - optional local deployment support
	}
	return &http.Client{
		Transport: tr,
		Timeout:   0, // conversion/download may be long; let ffmpeg / webdav operations run until completion
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func escapeAttr(s string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#39;",
	)
	return replacer.Replace(s)
}

// parseTime parses HH:MM:SS.XXXXXX into total seconds.
func parseTime(s string) float64 {
	// Handle both HH:MM:SS.us and HH:MM:SS formats
	parts := strings.Split(s, ":")
	if len(parts) == 3 {
		h, _ := strconv.ParseFloat(parts[0], 64)
		m, _ := strconv.ParseFloat(parts[1], 64)
		secStr := parts[2]
		sVal, _ := strconv.ParseFloat(secStr, 64)
		return h*3600 + m*60 + sVal
	}
	return 0
}

func probeRemoteMedia(ctx context.Context, cfg Config, cookie, remotePath string) (MediaInfo, error) {
	var info MediaInfo
	u, err := buildWebDAVURL(cfg, remotePath)
	if err != nil {
		return info, err
	}

	args := []string{"-v", "quiet", "-print_format", "json", "-show_format", "-show_streams"}

	if cookie != "" {
		args = append(args, "-headers", "Cookie: "+cookie+"\r\n")
	} else if cfg.NextcloudUser != "" && cfg.NextcloudPass != "" {
		args = append(args, "-headers", "Authorization: Basic "+base64.StdEncoding.EncodeToString([]byte(cfg.NextcloudUser+":"+cfg.NextcloudPass))+"\r\n")
	}

	args = append(args, u)

	cmd := exec.CommandContext(ctx, "ffprobe", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return info, err
	}

	var data struct {
		Format struct {
			Duration string `json:"duration"`
			Size     string `json:"size"`
			BitRate  string `json:"bit_rate"`
		} `json:"format"`
		Streams []struct {
			CodecType      string            `json:"codec_type"`
			CodecName      string            `json:"codec_name"`
			ColorTransfer  string            `json:"color_transfer"`
			ColorPrimaries string            `json:"color_primaries"`
			ColorSpace     string            `json:"color_space"`
			Width          int               `json:"width"`
			Height         int               `json:"height"`
			PixFmt         string            `json:"pix_fmt"`
			Duration       string            `json:"duration"`
			Tags           map[string]string `json:"tags"`
		} `json:"streams"`
	}

	if err := json.Unmarshal(out.Bytes(), &data); err != nil {
		return info, err
	}

	if d, err := strconv.ParseFloat(data.Format.Duration, 64); err == nil {
		info.DurationSeconds = d
	}
	if s, err := strconv.ParseInt(data.Format.Size, 10, 64); err == nil {
		info.Size = s
	}
	if b, err := strconv.Atoi(data.Format.BitRate); err == nil {
		info.Bitrate = b
	}

	for _, s := range data.Streams {
		if s.CodecType == "audio" && info.AudioCodec == "" {
			info.AudioCodec = s.CodecName
		}
		if s.CodecType != "video" {
			continue
		}

		info.VideoCodec = s.CodecName

		if info.DurationSeconds <= 0 {
			if s.Duration != "" {
				if d, err := strconv.ParseFloat(s.Duration, 64); err == nil {
					info.DurationSeconds = d
				}
			} else if durStr, ok := s.Tags["DURATION"]; ok {
				info.DurationSeconds = parseTime(durStr)
			}
		}
		info.Transfer = s.ColorTransfer
		info.Primaries = s.ColorPrimaries
		info.Space = s.ColorSpace
		info.PixelFormat = s.PixFmt
		info.Width = s.Width
		info.Height = s.Height

		t := strings.ToLower(s.ColorTransfer)
		p := strings.ToLower(s.ColorPrimaries)
		c := strings.ToLower(s.ColorSpace)
		if t == "smpte2084" || t == "arib-std-b67" || p == "bt2020" || strings.Contains(c, "2020") {
			info.IsHDR = true
		}
	}
	return info, nil
}

// sendNotification sends a Nextcloud notification to userID via AppAPI.
// The params payload is flat (not nested), and userId is derived from
// the AUTHORIZATION-APP-API header (base64(userId:secret)), so the
// userID passed here must match the user in that credential.
func sendNotification(cfg Config, userID, subject, message string) {
	if cfg.NextcloudURL == "" || cfg.AppID == "" || userID == "" {
		return
	}

	// Payload structure per ExNotificationsManager.php:
	// $params['object'], $params['object_id'], $params['subject_type'], $params['subject_params']
	// The userId is taken from the AUTHORIZATION-APP-API header (base64(userId:secret))
	// so we encode userId into the auth header, not the body.
	payload := map[string]any{
		"object":       "app_api",
		"object_id":    taskNotificationID(userID),
		"subject_type": "app_api_ex_app",
		"subject_params": map[string]any{
			"rich_subject":        subject,
			"rich_subject_params": map[string]any{},
			"rich_message":        message,
			"rich_message_params": map[string]any{},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("sendNotification: marshal error: %v", err)
		return
	}

	endpoint, err := buildNextcloudAPIURL(cfg, "/ocs/v1.php/apps/app_api/api/v1/notification")
	if err != nil {
		log.Printf("sendNotification: URL build error: %v", err)
		return
	}

	// Override the auth header to encode userID:secret so AppAPI knows which user to notify.
	client := newHTTPClient(cfg.InsecureTLS)
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		log.Printf("sendNotification: request build error: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("OCS-APIRequest", "true")
	req.Header.Set("AA-VERSION", cfg.AAVersion)
	req.Header.Set("EX-APP-ID", cfg.AppID)
	req.Header.Set("EX-APP-VERSION", cfg.AppVersion)
	// AUTHORIZATION-APP-API must be base64(userId:secret) where userId is the RECIPIENT
	authValue := base64.StdEncoding.EncodeToString([]byte(userID + ":" + cfg.AppSecret))
	req.Header.Set("AUTHORIZATION-APP-API", authValue)

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("sendNotification: HTTP error: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		log.Printf("sendNotification: server returned %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}
}

func taskNotificationID(userID string) string {
	return fmt.Sprintf("video_converter_%s_%d", userID, time.Now().UnixMilli())
}
