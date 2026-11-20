# AGENTS.md

Instructions for AI coding agents working in this repository. Read this before
changing code; user-facing docs live in [README.md](README.md) and [the API is
documented there](README.md#api-概览).

## Project at a glance

Echo Service is the Go backend for **Echo App** (not yet built). It lets a user

1. configure their own Z-Library account (credentials or cookies),
2. search and download EPUBs (zlib EAPI implemented **in-process**, never via
   the zlib CLI),
3. hand downloaded EPUBs to an external [Echo TTS
   Service](https://github.com/imlinhanchao/tts-service) and get per-chapter
   audio + SRT, and
4. play/download the result.

Storage is MySQL 8 via GORM; every row belongs to exactly one user.

## Repository layout

```
cmd/echo-service/          main HTTP service
cmd/resume-audiobook/      resume an interrupted conversion from saved segments
cmd/split-audiobook/       backfill chapter chunks for old audiobooks
cmd/prune-full-audio/      delete legacy full-length audio files
internal/api/              HTTP handlers, middleware, file serving
internal/auth/             argon2id passwords, session/API token generation
internal/audiobook/        EPUB parsing, segmentation, TTS pipeline, mastering,
                           chunking, SRT
internal/config/           env/.env configuration
internal/jobs/             in-process job queue (persisted in MySQL)
internal/providers/        Provider interface + registry
internal/providers/zlib/   zlib EAPI client (pure Go)
internal/store/            GORM models, DB setup, repositories
internal/tts/              client for the external TTS service
internal/web/              embedded test frontend (vanilla JS)
deploy/                    systemd unit template
docs/screenshots/          README images
scripts/                   e2e, CDP screenshot tools, systemd installer
```

## Commands

```bash
make build         # bin/echo-service + helper tools
make run           # go run ./cmd/echo-service
make test          # unit tests, no DB or network
make test-mysql    # MySQL integration tests (reads .env, uses echo_service_test)
make e2e           # optional full e2e: needs zlib session + TTS service + jq
docker compose up -d --build   # MySQL + service
make install-service           # systemd user service (Linux)
```

Run `gofmt -w` on Go files before committing. `go vet ./...` and `make test`
must pass. CI runs the same plus a MySQL 8 job.

## Architecture rules

### Account isolation (never break this)

- Every user-owned table has `user_id`; every repo query must scope by it.
  Handlers get the user from `userFrom(r)` (session cookie or Bearer token) and
  must never accept a user id from the request.
- File serving only accepts record ids; server-side paths come from the DB and
  are joined under `ECHO_DATA_DIR`. Never build filesystem paths from user
  input.
- New endpoints need an isolation test or a manual verification note in the PR.

### Providers are pluggable; zlib must stay CLI-free

- Implement `providers.Provider` (see `internal/providers/provider.go`) and
  register it in `cmd/echo-service/main.go`.
- Do **not** shell out to `zlib`, `ebook-convert`, or similar. The zlib plugin
  speaks the EAPI HTTP protocol directly and fails with typed errors
  (`ErrSessionExpired`, `ErrBlocked`, `ErrQuotaExceeded`, ...).
- Provider credentials are encrypted with AES-256-GCM (`internal/secret`) and
  are never returned to clients or written to logs.
- Keep new providers independent: no shared mutable state beyond caches.

### TTS is an external service

- All synthesis goes through `internal/tts.Client`. Contract: `GET /health`,
  `GET /api/models`, `GET /api/voices?model=`, `POST /api/warmup`,
  `POST /v1/audio/speech`. Timing lives in response headers
  (`X-Audio-Seconds`, `X-Sample-Rate`, `X-RTF`, `X-Queue-Seconds`).
- Local models (Qwen/Kokoro) are serialized by the TTS service; send segments
  sequentially. Edge TTS is network-bound and can be concurrent (the pipeline
  uses 6 segment workers for `edge-tts`).
- Do not hardcode model/voice names in handlers; expose them from the service.
- Never call the TTS service from an HTTP handler for long jobs; enqueue a job.

### Audiobook pipeline

- EPUB → chapters → segments (≤80 runes, CJK punctuation aware) → TTS with
  retries/fallback → **streaming** master (RMS normalize, peak limit, fades,
  gaps) → SRT → per-chapter chunks → delete the redundant full-length file.
- Mastering must stay streaming. A 15-hour book would otherwise need several
  GB of RAM.
- Chunks are `chapters/NNNN.mp3|wav` + `NNNN.srt` + `manifest.json`;
  `Audiobook.ManifestPath` points at the manifest. The single full-length file
  is not kept (legacy data can be cleaned with `cmd/prune-full-audio`).
- `ReuseSegments` in `audiobook.Options` is how `resume-audiobook` reuses
  `segments/*.wav`; preserve that contract when touching the pipeline.
- `MAX_JOB_CHARS` bounds one job; the frontend must show the limit instead of
  letting users hit a late failure.

### Jobs

- Long work (downloads, conversions) is a row in `jobs`, claimed atomically
  (`UPDATE ... WHERE status='queued'` and `RowsAffected()==1`).
- At most one active job per user. Cancellation is cooperative: the worker polls
  `Jobs.IsCancelled` and stops at a segment boundary.
- On startup, stale `running` jobs become `failed` ("服务重启中断，请重试").
- Update user-visible progress (0-100) and a meaningful error message.

### Auth

- Web: `POST /api/auth/login` sets the HttpOnly `echo_session` cookie.
- App: `POST /api/auth/token` returns `echo_pat_...`; clients send
  `Authorization: Bearer ...`. Tokens are stored as SHA-256 only, expire
  (`ECHO_TOKEN_TTL`), and refresh rotates/revokes.
- Bearer requests skip the CSRF Origin check; cookie requests keep it.
- Login/token endpoints are rate limited.

### Database

- GORM `AutoMigrate` runs on start and is **additive only**. Changing a column
  type or dropping a column needs an explicit migration + README note.
- Store UTC (`time.Now().UTC()`); API returns RFC3339.
- `users`, `books`, `jobs`, `audiobooks`, `provider_accounts`, `sessions`,
  `api_tokens`, `user_settings` are the core tables.

## Conventions

- Go: `gofmt`, standard library first; keep packages small. Errors wrap with
  `%w`. User-facing API messages are Chinese; technical logs/errors may be
  English.
- API errors: `{"error":{"code":"...","message":"..."}}` with a sensible HTTP
  status (401/403/404/409/429/502/503).
- New environment variables: add to `internal/config/config.go`,
  `.env.example`, and the README table in the same change.
- Do not add dependencies without a clear reason; the service is intentionally
  small.
- New HTTP route: add it under `/api`, use the `s.auth(...)` middleware, and
  make sure file/ownership access is checked.
- Frontend (`internal/web`) is vanilla JS with no build step; keep it working
  offline and run `node --check internal/web/app.js` after edits.
- Update README when behavior, endpoints, configuration or the run path
  changes. Keep `docs/screenshots/` current if the UI changes.

## Testing

- Unit tests must not require MySQL, network, or credentials. Use `httptest`
  fakes for zlib/TTS (see existing tests in `internal/providers/zlib` and
  `internal/audiobook`).
- MySQL integration tests are gated:
  `ECHO_TEST_MYSQL=1 ECHO_MYSQL_PASSWORD=... go test ./internal/store -v`
  (or `make test-mysql`). They create/migrate `echo_service_test` and clean
  tables.
- `scripts/e2e.sh` is a manual acceptance test: it needs a running service, a
  zlib session (`zlib login` or `ZLIB_SESSION`), a TTS service, `curl` and
  `jq`. Do not wire it into CI.
- Before finishing: `gofmt -l`, `go vet ./...`, `make test`. If MySQL is
  available, `make test-mysql`. If you touched the pipeline, also run the
  audiobook tests and ideally a small real conversion (or the e2e script).

## Common pitfalls

- **zlib** is an external service with changing mirrors, bot protection and a
  daily download quota. Surface `ErrSessionExpired`/`ErrBlocked`/
  `ErrQuotaExceeded` instead of returning generic 500s; keep the domain
  failover and cookie import paths.
- **TTS service** is Apple-Silicon MLX based. Without it, search/download still
  work; conversion must fail with a clear "TTS 不可用" message. Do not make TTS
  a hard dependency of startup.
- A chapter longer than the chunk limit is split into equal pieces; SRT cues
  must be rebased per chunk (`internal/audiobook/split.go`).
- Old audiobooks may predate chunking; use `bin/split-audiobook` to backfill
  before assuming `chapters_url` exists.
- Cookie name is `echo_session`; renaming it logs everyone out.
- `ECHO_DATA_DIR` paths in DB are relative to the data dir; don't store
  absolute paths.
- Do not commit `.env`, `data/`, `bin/`, `.tmp/`, logs, cookies or any real
  credentials. Tests use obvious fake values (`FAKE-KEY`).

## Security checklist

- New endpoint: auth middleware? user scoping? file ownership? input limits?
- No secrets/tokens/passwords in logs, errors, URLs or committed files.
- Validate download sizes (`MAX_DOWNLOAD_MB`) and job limits.
- Keep provider credentials encrypted and API tokens hashed.
- Report vulnerabilities privately (see README security section) rather than in
  public issues.
