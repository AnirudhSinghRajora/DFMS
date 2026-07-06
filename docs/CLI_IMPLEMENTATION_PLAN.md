# DFMS CLI (`dfmsctl`) — Implementation Plan

> Status: **Proposed** · Companion to [CLI_DESIGN.md](CLI_DESIGN.md)
> · Last updated: 2026-06-22

This is the end-to-end software-engineering plan to build `dfmsctl`: principles,
phases, concrete tasks, tests, and acceptance criteria. It is organized as
**vertical slices** — each phase ships a usable, tested capability rather than a
horizontal layer — so the tool is dogfoodable from Phase 1 onward.

---

## 1. Engineering Approach

- **Iterative & vertical.** Each phase delivers working commands end-to-end
  (command → client → API → rendered output), not a half-wired layer.
- **Test-first where it counts.** The client and config/token stores are pure
  logic and get unit tests against an `httptest.Server` and temp dirs. Cobra
  commands stay thin so most logic is tested below the command layer.
- **Reuse over re-implement.** Import `pkg/models` and `pkg/errors`; mirror the
  server's Viper config patterns; follow existing Makefile/CI conventions.
- **Definition of Done (per phase):** code + tests passing under `-race`,
  `golangci-lint` clean (the repo's [.golangci.yml](../.golangci.yml)),
  `gofmt`/`goimports` clean, docs/help text updated, manually exercised against a
  local stack (`make dev`).
- **Conventional commits** and small PRs per slice, matching the repo history.

### Tooling/CI guardrails (apply to every phase)
- `go build ./...`, `go test -race ./...`, `golangci-lint run` must stay green.
- New packages meet the repo's coverage bar (aim ≥80% for `dfmsclient` and the
  config/token stores).
- The CLI must **not** import server-internal packages (`internal/...` of the
  services); only `pkg/...` is shared. Enforced by review (optionally a lint rule).

---

## 2. Milestones at a Glance

| Phase | Theme | Outcome | Rough size |
|---|---|---|---|
| 0 | Foundations & scaffolding | `dfmsctl version`/`help` build & run; CI wired | S |
| 1 | Contexts & config | Manage servers; config precedence works | S–M |
| 2 | Auth & secure tokens | `register`/`login`/`logout`/`status`; auto-refresh | M |
| 3 | Core file operations | `upload`/`download`/`list`/`get`/`delete` | M–L |
| 4 | Full API parity | folders, versions, multipart-auto, search, storage, admin | M–L |
| 5 | UX polish | json/yaml output, progress, color, completions | M |
| 6 | Distribution & docs | GoReleaser, man pages, install docs, dogfooding | M |

Sizes are relative effort (S≈1–2 days, M≈3–5, L≈1–2 weeks) for one engineer;
treat as planning aids, not commitments.

---

## 3. Phase 0 — Foundations & Scaffolding

**Goal:** a building, runnable skeleton with CI and conventions in place.

### Tasks
- Create `cmd/dfmsctl/main.go` (thin) and `internal/cli/root.go` with the root
  Cobra command, global persistent flags (`--context/-c`, `--output/-o`,
  `--quiet/-q`, `--verbose`), and `version`.
- Add `version`/`commit`/`date` vars wired via `-ldflags` (extend `GOFLAGS`
  pattern in the [Makefile](../Makefile)).
- Add `dfmsctl` to the Makefile `SERVICES` list; add a `make run-cli ARGS=…`
  helper if convenient.
- Wire dependencies: `go get` cobra, viper (already present).
- Add CLI build to the existing CI workflow (`.github/workflows/ci.yml`) matrix.
- Stub `internal/dfmsclient/client.go` with the `Client` struct + constructor
  (no endpoints yet) so the layering compiles.

### Deliverables
- `dfmsctl`, `dfmsctl version`, `dfmsctl help` work.
- Green `build` + `lint` in CI for the new paths.

### Acceptance
- `make build` produces `bin/dfmsctl`; `./bin/dfmsctl version` prints injected
  version metadata.

---

## 4. Phase 1 — Contexts & Configuration

**Goal:** manage one or more servers; establish config loading/precedence.

### Tasks
- `internal/config/cli/config.go`: load/save `~/.config/dfms/config.yaml`
  (XDG-aware), schema per [CLI_DESIGN.md §6](CLI_DESIGN.md). Create dirs `0700`,
  files `0600`.
- Viper precedence: flag → `DFMSCTL_*` env → file → default.
- `internal/cli/context.go`: `context add|list|use|remove|show`.
- Resolve the active context in a persistent pre-run hook; expose it to commands.

### Tests
- Config round-trip (write → read) in a temp `XDG_CONFIG_HOME`.
- Precedence: env overrides file; `--context` overrides env.
- `context use` of an unknown name errors clearly.

### Acceptance
- `dfmsctl context add local --url http://localhost:8080 && dfmsctl context use local && dfmsctl context list`
  works and persists across invocations.

---

## 5. Phase 2 — Authentication & Secure Token Storage

**Goal:** authenticate once; tokens stored securely; access tokens auto-refresh.

### Tasks
- `internal/config/cli/tokens.go`: token store interface with two backends —
  `go-keyring` and `0600` file fallback — keyed by context. Runtime selection.
- `internal/dfmsclient/auth.go`: `Register`, `Login`, `Refresh`, `Whoami`
  (local token decode).
- `internal/dfmsclient/transport.go`: auth `RoundTripper` — inject bearer; detect
  expiry (decode `exp`) or `401 AUTH_TOKEN_EXPIRED`; refresh + retry once; persist
  new tokens; on refresh failure, clear and signal re-login.
- `internal/dfmsclient/errors.go`: decode `pkg/errors` envelope → typed error with
  `code`/`message`/`request_id`; map to exit codes.
- `internal/cli/auth.go`: `auth register|login|logout|status`. No-echo password
  prompt (`x/term`); `--password-stdin` and `DFMSCTL_PASSWORD` for automation.

### Tests
- Login stores tokens; subsequent client call attaches bearer (httptest).
- Expired access token → transport calls `/auth/refresh` once and retries; verify
  exactly one refresh and one retry.
- Refresh failure → returns auth error (exit 4), tokens cleared.
- File-fallback store honors `0600`; keyring backend behind an interface (mockable).

### Acceptance
- Against a local stack (`make dev`): `auth register`/`login` succeed; a protected
  call works; after the access token's 15-min TTL the next call refreshes
  transparently with no user action.

### Security review checkpoint
- Confirm no token logging, no password flag, `Authorization` redaction in
  `--verbose`, restrictive file perms.

---

## 6. Phase 3 — Core File Operations

**Goal:** the everyday upload/download/list/get/delete loop, streaming-safe.

### Tasks
- `internal/dfmsclient/files.go`:
  - `UploadFile(ctx, name, r, size)` → `POST /files/upload` with
    `application/octet-stream` + `X-File-Name`, streamed from disk.
  - `DownloadFile(ctx, id, w)` → stream body to `w` via `io.Copy`.
  - `ListFiles`, `GetFile`, `DeleteFile`.
- `internal/cli/files.go`: `files upload|download|list|get|delete`.
  - `upload`: open file, stat for size, stream; default output is the new
    `models.File`.
  - `download`: `-o/--output-file` (default: server-provided name); never buffer.
- Basic table rendering for `list`/`get` (full renderer comes in Phase 5; start
  with a minimal `tabwriter`).

### Tests
- Upload streams (assert request headers + that the body equals the file; use a
  large temp file to prove no full-buffer).
- Download writes bytes to a temp path and checksum-matches.
- 404 → exit 5 with a clear message; quota error → exit 6.

### Acceptance
- Round-trip: `files upload X` then `files download <id>` reproduces the file
  byte-for-byte against a local stack.

---

## 7. Phase 4 — Full API Parity

**Goal:** cover the remaining endpoints, including transparent multipart.

### Tasks
- **Multipart (hidden):** `internal/dfmsclient/multipart.go` — `init → PUT parts
  → complete`, with `abort` on failure. `UploadFile` auto-selects multipart when
  `size > multipart_threshold` (config, default 64 MB); bounded part reads;
  optional concurrency for parts.
- **Versions:** `files versions list|download|delete`.
- **Folders:** `folders create|contents|move|delete`
  (`move` → `PUT /files/:id/move`).
- **Search:** `files search <query>` → `GET /search?q=`.
- **Storage:** `storage usage`.
- **Admin:** `admin nodes` (`GET /admin/nodes`) — requires admin role; handle
  `AUTH_FORBIDDEN` (exit 4) gracefully.

### Tests
- Multipart path triggered by size threshold; parts assembled; abort on simulated
  mid-upload failure leaves no dangling upload (assert `abort` called).
- Each new command: happy path + one error path against httptest.

### Acceptance
- Every row in [CLI_DESIGN.md §5](CLI_DESIGN.md) mapping table has a working
  command exercised against a local stack. A >64 MB upload visibly uses multipart.

---

## 8. Phase 5 — UX Polish

**Goal:** make it pleasant and scriptable.

### Tasks
- `internal/cli/output/`: pluggable renderer — `table` (default), `json`, `yaml`;
  TTY detection; `--quiet` (IDs only).
- Progress bars (`mpb`) for upload/download, auto-disabled on non-TTY/`--quiet`.
- Color via `fatih/color` (honors `NO_COLOR`).
- Shell completions: `dfmsctl completion bash|zsh|fish|powershell` (Cobra
  built-in) + dynamic completion for file IDs/context names where feasible.
- Consistent, friendly error formatting (code + message + `request_id`).

### Tests
- `-o json` output unmarshals back into the `pkg/models` type.
- Renderer selects correctly by flag/TTY; `NO_COLOR` disables color.

### Acceptance
- `dfmsctl files list -o json | jq` works; completions install and tab-complete
  subcommands; progress bar shows for a large upload in a terminal but not in a
  pipe.

---

## 9. Phase 6 — Distribution & Documentation

**Goal:** users can install and learn it without reading source.

### Tasks
- **GoReleaser** config: cross-platform archives, `checksums.txt`, version
  ldflags, Homebrew tap (decision-gated), optional `.deb`/`.rpm`.
- CI release job on git tag; verify `go install …/cmd/dfmsctl@latest`.
- Generate man pages (Cobra) and ship them.
- Docs: a CLI usage section in the [README](../README.md) (replace/augment the
  raw `curl` examples), and a `dfmsctl`-focused quickstart.
- Update [CLAUDE.md](../CLAUDE.md) commands section with the CLI build/run.

### Acceptance
- Tagging a release publishes binaries + checksums; `brew install` (if enabled)
  or `go install` yields a working `dfmsctl`; `--help`/man pages are complete.

---

## 10. Cross-Cutting Concerns

### Testing strategy
- **Unit:** `dfmsclient` against `httptest.Server` (success + each error code);
  config/token stores against temp dirs; transport refresh logic in isolation.
- **Command tests:** execute Cobra commands with a fake client interface; assert
  rendered output and exit codes.
- **Integration (opt-in, `-tags=integration`):** drive a real local stack
  (`make dev` / docker infra) through register → upload → download → delete.
  Mirrors the repo's existing integration-tag convention.
- **Race + coverage** gates as elsewhere in the project.

### Error handling & exit codes
- Single mapping from `pkg/errors` codes → exit codes (table in
  [CLI_DESIGN.md §9](CLI_DESIGN.md)), implemented once in `internal/cli/root.go`'s
  error sink so every command is consistent.

### Observability (optional, nice-to-have)
- `--verbose` dumps redacted request/response metadata (method, path, status,
  `request_id`, latency) to stderr — no bodies, no tokens.

### Security review (gate before v1.0)
- Token storage, perms, redaction, TLS defaults, rate-limit behavior re-checked
  end-to-end. Run the repo's `/security-review` over the diff.

---

## 11. Risk Register

| Risk | Impact | Mitigation |
|---|---|---|
| Keyring unavailable (headless/CI) | Login fails | `0600` file fallback selected at runtime; documented |
| `go run`/binary token leakage | Security | No password flags; redaction; perms; security-review gate |
| Streaming/multipart memory blowups | Crashes on big files | Stream from disk; bounded part buffers; tests with large temp files |
| Client/API drift | Broken commands | Same module + shared `pkg/models`/`pkg/errors`; integration tests on real stack |
| Refresh-retry loops | Hammering `/auth/refresh` | Retry once, then fail to re-login; respect `Retry-After` |
| Dependency bloat in shared `go.mod` | Heavier server builds | Keep CLI deps minimal (prefer std-lib `tabwriter`); CLI-only deps don't affect service binaries' size materially |
| Scope creep (TUI, server admin) | Slips v1 | Explicit non-goals in design doc |

---

## 12. Definition of Done (v1.0)

- [ ] All commands in the [§5 mapping table](CLI_DESIGN.md) implemented & tested.
- [ ] Multi-context management with secure, per-context token storage.
- [ ] Transparent token refresh verified against a real stack.
- [ ] Streaming up/download + auto-multipart, proven with large files.
- [ ] `table`/`json`/`yaml` output, `--quiet`, completions, progress bars.
- [ ] Stable, documented exit codes mapped from `pkg/errors`.
- [ ] CI: build + `-race` tests + lint green; coverage bar met.
- [ ] GoReleaser publishes binaries + checksums; `go install` works.
- [ ] README + CLAUDE.md updated; man pages shipped.
- [ ] Security review passed.

---

## 13. Future Enhancements (post-v1)

- Interactive TUI (`bubbletea`) for browsing files/folders.
- Resumable uploads/downloads with on-disk part state.
- Directory sync (`dfmsctl sync ./dir`), watch mode.
- Config-driven aliases and output templates (Go templates like `kubectl -o
  go-template`).
- Plugin model (`dfmsctl-<name>` on `PATH`, like `git`/`kubectl`).
- Bulk operations and `--recursive` folder upload/download.
