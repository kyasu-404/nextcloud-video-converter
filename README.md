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
- `GET /ui/convert.html`
- `POST /api/metadata`
- `POST /api/convert`
- `GET /api/task/{id}`
- `POST /api/task/{id}/cancel`

## Admin settings

On enable, the ExApp registers AppAPI declarative admin settings under the ExApps settings section. Nextcloud stores changed values in AppConfig.

- `max_concurrent_jobs` - total concurrent conversions; default is `1`.
- `max_concurrent_jobs_per_user` - concurrent conversions per user; default is `1`.
- `max_queued_jobs_per_user` - waiting conversions per user; default is `3`. Set to `0` to accept only jobs that can start immediately.
- `job_timeout_minutes` - job timeout in minutes; default is `120`.
- `cpu_limit_percent` - maximum ffmpeg CPU budget from `1` to `100`; default is `50`.
- `threads_per_job` - ffmpeg threads per job; default is `0`, which leaves thread selection unrestricted.

Every logged-in Nextcloud user can use the converter; there is no group restriction.
If the settings service is unavailable, `/api/convert` returns HTTP 503.
The authenticated user owns the conversion; user IDs supplied in JSON are ignored. Only the owner can read or cancel a task.
CPU limiting is applied with `cpulimit`; `threads_per_job` adds ffmpeg `-threads` only when it is greater than `0`.
On Linux, cancellation and job timeouts stop the complete encoder process group, including `cpulimit` children.

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
- `APP_SECRET` — shared AppAPI secret, injected automatically by AppAPI/HaRP; required to verify incoming AppAPI user credentials

For direct requests in manual mode, authenticate with HTTP Basic using `NEXTCLOUD_USER` and `NEXTCLOUD_APP_PASSWORD`. A JSON user ID or a session cookie alone does not authenticate a direct request to the ExApp.

## File Actions Menu

The AppAPI docs describe FileActionsMenu registration via:
`POST /ocs/v1.php/apps/app_api/api/v2/ui/files-actions-menu`

The payload that AppAPI forwards to the ExApp includes:
- `fileId`
- `name`
- `directory`
- `mime`
- `userId`

The `/action/file` endpoint in this app opens the embedded conversion page, where the user can choose the conversion settings.

## File preservation

Converted files are created with a WebDAV `If-None-Match: *` precondition. If `source_converted.mp4` already exists, the app tries `source_converted (2).mp4`, then the next available number. Existing results are never overwritten.

Deleting the original requires the strong ETag returned by its download. The subsequent DELETE uses `If-Match`, so a file changed during conversion is preserved. If deletion fails, the task error includes the saved output path. If the server provides no strong ETag, disable deletion to convert while retaining the original.

The original bit-depth option preserves source precision. If the selected encoder cannot preserve it, choose an explicit supported depth rather than silently losing precision.

## Tests

```bash
go test ./...
go vet ./...
go test -race ./...
```

Race detection requires a C compiler. Media lifecycle tests run real FFmpeg/ffprobe when installed and otherwise skip those checks. Linux tests exercise cancellation both directly and through a wrapper process; set `VC_TEST_CPULIMIT_BIN` to an installed cpulimit executable to test it as well.

## Notes

- H.264 is the safest default.
- AAC is the safest audio codec.
- `SDR` mode uses HDR->SDR tone mapping when the source file is HDR.
- `copy` video mode ignores video-specific options that would require re-encoding.
- For production, run Nextcloud and this ExApp in the same Docker network or an exposed network reachable from the AppAPI deploy daemon.
