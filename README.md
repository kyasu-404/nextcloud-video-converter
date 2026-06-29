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

On enable, the ExApp registers AppAPI declarative admin settings under the ExApps settings section. Nextcloud stores changed values in AppConfig.

- `allowed_groups` - selected Nextcloud group IDs. Empty means all logged-in users can use the converter.
- `max_concurrent_jobs` - total concurrent conversions; default is `1`.
- `max_concurrent_jobs_per_user` - concurrent conversions per user; default is `1`.
- `max_queued_jobs_per_user` - queued conversions per user; default is `3`.
- `job_timeout_minutes` - job timeout in minutes; default is `120`.
- `cpu_limit_percent` - maximum ffmpeg CPU budget from `1` to `100`; default is `50`.
- `threads_per_job` - ffmpeg threads per job; default is `0`, which leaves thread selection unrestricted.

Group access is enforced by the ExApp on `/action/file`, `/ui/convert.html`, `/api/metadata`, and `/api/convert`.
CPU limiting is applied with `cpulimit`; `threads_per_job` adds ffmpeg `-threads` only when it is greater than `0`.
If AppAPI calls `/enabled` without a user context, set `settings_admin_user` in AppConfig to a Nextcloud admin user so the ExApp can populate group options.

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
