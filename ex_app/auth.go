package main

import (
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
)

// User IDs in request bodies are never an authentication source.
func requestUserID(cfg Config, r *http.Request) (string, error) {
	if auth := r.Header.Get("AUTHORIZATION-APP-API"); auth != "" {
		decoded, err := base64.StdEncoding.DecodeString(auth)
		if err != nil {
			return "", errors.New("invalid AppAPI credentials")
		}
		userID, secret, ok := strings.Cut(string(decoded), ":")
		if !ok || userID == "" || cfg.AppSecret == "" || subtle.ConstantTimeCompare([]byte(secret), []byte(cfg.AppSecret)) != 1 {
			return "", errors.New("invalid AppAPI credentials")
		}
		return userID, nil
	}
	// Direct manual-mode requests must authenticate as the configured account.
	if user, password, ok := r.BasicAuth(); ok && cfg.NextcloudUser != "" && cfg.NextcloudPass != "" &&
		subtle.ConstantTimeCompare([]byte(user), []byte(cfg.NextcloudUser)) == 1 &&
		subtle.ConstantTimeCompare([]byte(password), []byte(cfg.NextcloudPass)) == 1 {
		return cfg.NextcloudUser, nil
	}
	return "", errors.New("user authentication required")
}

var errSettingsUnavailable = errors.New("Video Converter settings are unavailable")
