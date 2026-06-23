# syntax=docker/dockerfile:1
# SPDX-FileCopyrightText: 2024 Nextcloud GmbH and contributors
# SPDX-License-Identifier: AGPL-3.0-or-later

FROM golang:1.23-alpine AS build
WORKDIR /src

COPY go.mod ./
COPY ex_app ./ex_app
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/video-converter ./ex_app

FROM alpine:3.21
# ca-certificates required: HaRP installs Nextcloud custom CA certs into the container
# ffmpeg required: for video encoding/decoding
# frp and bash are required by the standard HaRP ExApp launcher.
RUN apk add --no-cache ffmpeg ca-certificates tzdata curl bash frp

WORKDIR /app

COPY --from=build /out/video-converter /app/video-converter
COPY appinfo /app/appinfo
COPY ex_app/ui /app/ui
COPY start.sh /app/start.sh
RUN chmod +x /app/start.sh

# APP_PORT is injected by AppAPI/HaRP; PORT is a legacy fallback for manual/dev mode.
ENV APP_PORT=8080
EXPOSE 8080

# NOTE: Do NOT switch to a non-root user here.
# HaRP needs to run `update-ca-certificates` inside the container as root
# during the certificate installation step (docker/exapp/install_certificates).

ENTRYPOINT ["/app/start.sh", "/app/video-converter"]
