# Video Converter ExApp

A small Nextcloud AppAPI / ExApp service that:
- registers a Files context action for videos,
- opens a conversion UI,
- downloads the source video from Nextcloud via WebDAV,
- runs ffmpeg,
- uploads the converted file back to Nextcloud,
- optionally deletes the original file.

## What you need in Nextcloud

1. AppAPI enabled.
2. A registered Deploy Daemon.
3. This ExApp image available to the daemon.
4. A File Actions Menu entry registered in AppAPI for `video/*` (or any video mime types you want).

The ExApp side in this repository exposes:
- `GET /heartbeat`
- `POST /init`
- `GET /enabled`
- `POST /action/file`
- `GET /ui/convert`
- `POST /api/convert`
- `GET /api/task/{id}`

## Admin settings

On enable, the ExApp registers a declarative admin settings form through AppAPI:
`POST /ocs/v1.php/apps/app_api/api/v1/ui/settings`.

The form is shown in the Nextcloud admin settings under the ExApps settings section and stores values in AppConfig:

- `allowed_groups` - comma-separated Nextcloud group IDs. Empty means all logged-in users can use the converter.
- `max_cpu_percent` - maximum ffmpeg CPU budget from `1` to `100`; default is `100`.

Group access is enforced by the ExApp on `/action/file`, `/ui/convert.html`, `/api/metadata`, and `/api/convert`.
CPU limiting is applied to ffmpeg with `-threads` and, in the Docker image, `cpulimit`.

## Build and run locally

```bash
docker compose build
docker compose up -d
```

## Environment variables

- `NEXTCLOUD_URL` — base URL of your Nextcloud instance
- `NEXTCLOUD_USER` — account that has access to the files to be converted
- `NEXTCLOUD_APP_PASSWORD` — app password for that account
- `NEXTCLOUD_BASE_PATH` — WebDAV base path, for example `/remote.php/dav/files/converter/`
- `OUTPUT_DIR` — local temp folder for ffmpeg work files
- `NEXTCLOUD_INSECURE_TLS` — set to `true` only for self-signed test setups

## File Actions Menu

The AppAPI docs describe FileActionsMenu registration via:
`POST /ocs/v1.php/apps/app_api/api/v2/ui/files-actions-menu`

The payload that AppAPI forwards to the ExApp includes:
- `fileId`
- `name`
- `directory`
- `mime`
- `userId`

The `/action/file` endpoint in this app turns that payload into a redirect to `/ui/convert`, where the user can choose the conversion settings.

## Notes

- H.264 is the safest default.
- AAC is the safest audio codec.
- `SDR` mode uses HDR->SDR tone mapping when the source file is HDR.
- `copy` video mode ignores video-specific options that would require re-encoding.
- For production, run Nextcloud and this ExApp in the same Docker network or an exposed network reachable from the AppAPI deploy daemon.
