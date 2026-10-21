package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
)

var errOutputExists = errors.New("output file already exists")

func strongETag(etag string) bool {
	return len(etag) >= 2 && strings.HasPrefix(etag, `"`) && strings.HasSuffix(etag, `"`) &&
		!strings.ContainsAny(etag, "\r\n")
}

func uploadConvertedWebDAV(ctx context.Context, client *http.Client, cfg Config, req ConversionRequest, localPath string) (string, error) {
	base := buildRemoteOutputPath(req.FilePath, req.FileName, req.Container)
	ext := path.Ext(base)
	for attempt := 1; attempt <= 100; attempt++ {
		remotePath := base
		if attempt > 1 {
			remotePath = fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(base, ext), attempt, ext)
		}
		err := uploadWebDAV(ctx, client, cfg, req.Cookie, req.AppAPIAuth, req.RequestToken, remotePath, localPath)
		if errors.Is(err, errOutputExists) {
			continue
		}
		return remotePath, err
	}
	return "", errors.New("cannot find an unused output name after 100 attempts; existing files were preserved")
}
