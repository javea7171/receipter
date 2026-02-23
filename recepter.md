# Receipter Build Plan

## Implementation Progress
- [x] Phase 0.1 Create vertical-slice folder structure.
- Details: Created `cmd/*`, `frontend/*` slice folders, `infrastructure/*`, `models/`, `resources/frontend_design/`.
- [x] Phase 0.2 Add dependencies and module setup.
- Details: Initialized module in `go.mod`; added/locked dependencies in `go.sum` (`chi`, `bun`, `sqlite3`).
- [x] Phase 0.3 Implement config loading.
- Details: Added env-based config in `cmd/receipter/main.go` (`APP_ADDR`, `SQLITE_PATH`).
- [x] Phase 0.4 Initialize DB handles for read/write split.
- Details: Implemented in `infrastructure/sqlite/db.go` with immediate write tx lock and pooled read connection.
- [x] Phase 0.5 Add explicit transaction helpers.
- Details: Added `WithReadTx` and `WithWriteTx` in `infrastructure/sqlite/tx.go`.
- [x] Phase 0.6 Build HTTP server skeleton.
- Details: Added `infrastructure/http/server.go` with middleware, auth middleware, embedded assets, start/stop lifecycle.
- [x] Phase 0.7 Build route registration and explicit dependency passing.
- Details: Added `infrastructure/http/routes.go` with constructor-style handler wiring like `Handler(s.DB, s.SessionCache, ...)`.
- [x] Phase 0.8 Create initial slice files with naming conventions.
- Details: Added slice files under `frontend/login`, `frontend/pallets/*`, `frontend/stock`, `frontend/exports`, `frontend/settings`, `frontend/adminUsers`.
- [x] Phase 0.9 Add auth/session/RBAC/cache/audit infrastructure.
- Details: Added `infrastructure/session/session.go`, `infrastructure/rbac/rbac.go`, `infrastructure/cache/*.go`, `infrastructure/audit/audit.go`.
- [x] Phase 0.10 Add `/health` endpoint.
- Details: Implemented in `infrastructure/http/server.go`.
- [x] Phase 1.1 Add initial schema migration.
- Details: Added `infrastructure/sqlite/migrations/001_init.sql` with required tables, constraints, and indexes.
- [x] Phase 1.2 Add migration runner.
- Details: Added `infrastructure/sqlite/migrate.go`, wired into startup in `cmd/receipter/main.go`.
- [x] Phase 2.1 Implement real password hashing and verification.
- Details: Added Argon2id hashing/verify in `infrastructure/argon/argon.go` with tests in `infrastructure/argon/argon_test.go`.
- [x] Phase 2.2 Replace scaffold login with credential validation.
- Details: Updated `frontend/login/createLogin_db.go` and `frontend/login/createLogin_handler.go` to validate credentials against stored Argon2 hashes.
- [x] Phase 2.3 Persist sessions to DB and keep cache in sync.
- Details: Added session insert/load/delete transaction paths in `frontend/login/createLogin_db.go`; logout now deletes DB session in `frontend/login/logout_handler.go`.
- [x] Phase 2.4 Make auth middleware resilient to cache misses.
- Details: Updated `infrastructure/http/server.go` so middleware/root route resolve session from DB on cache miss, then repopulate cache.
- [x] Phase 2.5 Seed admin with hashed password.
- Details: Updated `cmd/seedAdmin/main.go` to use `login.UpsertUserPasswordHash(...)` with `ADMIN_PASSWORD` env var (default `Admin123!Receipter`).
- [x] Phase 3.1 Generate A4 landscape PDF pallet labels.
- Details: Added PDF rendering in `frontend/pallets/labels/palletLabels_pdf.go` using `gofpdf` and wired handler output to `application/pdf`.
- [x] Phase 3.2 Generate Code128 barcode on pallet labels.
- Details: Implemented Code128 generation/scaling in `frontend/pallets/labels/palletLabels_pdf.go` using `github.com/boombuler/barcode/code128`.
- [x] Phase 5.1 Implement scanner modal start/stop flow.
- Details: Replaced placeholder with QuaggaJS modal controls in `frontend/pallets/receipt/palletReceipt_scan_modal_view.go`.
- [x] Phase 5.2 Wire scan buttons to barcode inputs.
- Details: `carton_barcode` and `item_barcode` scan buttons now open modal and populate the targeted field in `frontend/pallets/receipt/palletReceipt_templ.go`.
- [x] Phase 7.1 Add pallet close/reopen lifecycle endpoints.
- Details: Added handlers in `frontend/pallets/progress/palletProgress_handler.go` and route wiring in `infrastructure/http/routes.go`.
- [x] Phase 7.2 Add lifecycle actions to progress view.
- Details: Progress page now lists pallets with receipt links and close/reopen actions via `frontend/pallets/progress/palletProgress_templ.go`.
- [x] Phase 7.3 Audit lifecycle changes.
- Details: Close/reopen status mutations now write audit logs in `frontend/pallets/progress/palletProgress_db.go`.
- [x] Phase 4.1 Enforce pallet status role rules on receipting.
- Details: Added status/role enforcement in `frontend/pallets/receipt/palletReceipt_handler.go` so scanners can only receipt open pallets while admins can receipt closed pallets.
- [x] Phase 4.2 Show pallet status and read-only behavior in receipt page.
- Details: Added pallet status loading in `frontend/pallets/receipt/palletReceipt_db.go` and read-only rendering controls/messages in `frontend/pallets/receipt/palletReceipt_templ.go`.
- [x] Phase 3.3 Add pallet scan entry page route.
- Details: Added `GET /tasker/scan/pallet` in `infrastructure/http/routes.go` with handler/view in `frontend/pallets/labels/palletLabels_handler.go` and `frontend/pallets/labels/scanPallet_templ.go`.
- [x] Phase 7.4 Add pallet content label view.
- Details: Added `GET /tasker/pallets/{id}/content-label` route and content renderer/query in `frontend/pallets/labels/palletLabels_db.go`, `frontend/pallets/labels/palletContentLabel_templ.go`, and `frontend/pallets/labels/palletLabels_handler.go`.
- [x] Phase 5.3 Log export runs to DB.
- Details: Added export run inserts in `frontend/exports/exports_db.go` and wired post-export logging in `frontend/exports/exports_handler.go` using session user context.
- [x] Phase 8.1 Add transaction rollback/commit policy tests.
- Details: Added `infrastructure/sqlite/tx_test.go` covering write rollback on error, write commit on success, and read transaction write-blocking behavior.
- [x] Phase 8.2 Enforce query-only read connection.
- Details: Updated `infrastructure/sqlite/db.go` read DSN and startup pragma with `_query_only=1` and `PRAGMA query_only = ON`.
- [x] Phase 8.3 Add receipt merge-rule tests.
- Details: Added `frontend/pallets/receipt/palletReceipt_db_test.go` covering merge on same pallet+sku+batch+expiry, blank batch merge behavior, and non-merge on different batch.
- [x] Phase 8.4 Add lifecycle audit tests.
- Details: Added `frontend/pallets/progress/palletProgress_db_test.go` verifying close/reopen state transitions and audit log creation.
- [x] Phase 8.5 Add core HTTP integration flow test.
- Details: Added `infrastructure/http/server_integration_test.go` covering `login -> create pallet -> receipt -> close -> admin receipt on closed -> reopen -> pallet csv export`.
- [x] Security.1 Add CSRF protection for POST routes.
- Details: Added global CSRF middleware in `infrastructure/http/csrf.go` and registered it in `infrastructure/http/server.go`.
- [x] Security.2 Add automatic CSRF form token injection.
- Details: Added `frontend/shared/html/csrf_view.go` and injected script into form pages (`frontend/login/getLoginScreen.templ`, `frontend/pallets/progress/palletProgress.templ`, `frontend/pallets/receipt/palletReceipt.templ`, `frontend/stock/stockImport.templ`, `frontend/settings/settings.templ`).
- [x] Security.3 Tighten password policy.
- Details: Added password policy validator in `frontend/login/password_policy.go` (+ tests in `frontend/login/password_policy_test.go`), enforced in `frontend/login/createLogin_db.go`, and updated default seeded admin password in `cmd/seedAdmin/main.go`.
- [x] Security.4 Add CSRF acceptance/rejection integration tests.
- Details: Added `TestCSRFPostWithoutTokenRejected` and `TestCSRFPostWithTokenAccepted` in `infrastructure/http/server_integration_test.go`.
- [x] Receipts.1 Add damaged flag and damaged qty to schema.
- Details: Updated `infrastructure/sqlite/migrations/001_init.sql` and `models/models.go` with `damaged` and `damaged_qty` fields.
- [x] Receipts.2 Add stock photo capture fields to schema.
- Details: Updated `infrastructure/sqlite/migrations/001_init.sql` and `models/models.go` with `stock_photo_blob`, `stock_photo_mime`, `stock_photo_name`.
- [x] Receipts.3 Add damaged and photo inputs to receipt UI.
- Details: Updated `frontend/pallets/receipt/palletReceipt_templ.go` with a damaged button/qty fields and stock photo capture input/button.
- [x] Receipts.4 Persist damaged/photo data and expose stored photos.
- Details: Updated save/load logic in `frontend/pallets/receipt/palletReceipt_db.go` and `frontend/pallets/receipt/palletReceipt_handler.go`; added `GET /tasker/api/pallets/{id}/receipts/{receiptID}/photo` route in `infrastructure/http/routes.go`.
- [x] Receipts.5 Add damaged/photo validation tests.
- Details: Added `TestSaveReceipt_DamagedQtyCannotExceedQty`, `TestParseOptionalPhotoRejectsNonImage`, `TestParseOptionalPhotoRejectsOver5MB`, and `TestParseOptionalPhotoAcceptsImage` in `frontend/pallets/receipt/palletReceipt_db_test.go`.
- [x] Exports.1 Add export run persistence integration test.
- Details: Added `TestExportRunLogged` in `infrastructure/http/server_integration_test.go` verifying `export_runs` inserts for `receipts_csv`.
- [x] Access.1 Add closed-pallet scanner/admin integration permission test.
- Details: Added `TestClosedPalletReceiptPermissions_ScannerDeniedAdminAllowed` in `infrastructure/http/server_integration_test.go`.
- [x] Access.2 Fix RBAC wildcard path matching and add tests.
- Details: Updated wildcard matching in `infrastructure/rbac/rbac.go` and added coverage in `infrastructure/rbac/rbac_test.go`.
- [x] Stock.1 Add stock import invalid-header and happy-path tests.
- Details: Added `TestImportCSV_InvalidHeader` and `TestImportCSV_HappyPathAndUpdatePath` in `frontend/stock/stockImport_db_test.go`.
- [x] Dev.1 Configure Air live reload with templ generation.
- Details: Added `.air.toml` with pre-build steps to run local CSS build (`npm run build:css`) and `templ generate -path . -lazy`, then rebuild/run `cmd/receipter`; watcher includes `.templ`/`.css` changes, excludes `node_modules`, and avoids loops on generated assets.
- [x] Dev.2 Add local DaisyUI/Tailwind pipeline.
- Details: Added `package.json` scripts, installed `daisyui@latest` with Tailwind CLI, moved source CSS to `frontend/styles/app.css`, and build output to `infrastructure/http/assets/app.css`; added `.gitignore` for `node_modules/`.
- [x] Dev.3 Render HTML via templ components (no string write paths).
- Details: Reworked slice handlers to call `Component.Render(...)` from `.templ` components (`login`, `progress`, `receipt`, `stock`, `settings`, `exports`, `adminUsers`, `labels`), generated `*_templ.go`, and removed obsolete `render*` string view files.
- [x] Verification build/test.
- Details: `go test ./...` passes.
- [x] Verification runtime smoke test.
- Details: Started server and confirmed `GET /health` returns `{\"status\":\"ok\"}`.

## 1. Locked Decisions
1. Access model is role-based: `scanner` and `admin`.
2. Pallet labels are `A4 landscape PDF`.
3. Receipt export must include `qty` and `pallet_id`.
4. Receipt lines merge only when `pallet_id + sku + batch + expiry` are identical.
5. Two lines with blank batch and blank expiry for the same item should also merge.
6. User-visible dates use UK format (`DD/MM/YYYY`).
7. Expiry is always a full date.
8. Carton and item barcodes are not unique; duplicates are allowed.
9. Closed pallets can still be edited by admins.
10. Stock import is CSV with header row: `sku,description`.
11. Audit logs are required for receipts and pallet status changes.
12. All DB reads and writes must run inside explicit transactions.
13. Architecture style is vertical slice.
14. Dependencies are passed explicitly into handlers from HTTP route registration.
15. Slice files follow a consistent pattern: `*_handler.go`, `*_db.go`, `*.templ`, generated `*_templ.go`, and `*_test.go` where relevant.

## 2. Tech Stack (Confirmed)
- Backend: Golang
- DB: SQLite3 with Bun ORM
- HTTP Router: Chi
- UI: templ + daisyUI
- Frontend interactivity: Datastar JS
- Scanner: QuaggaJS (`https://github.com/serratus/quaggaJS`)
- DB connection strategy:
- One dedicated write connection (`_txlock=immediate` behavior for immediate writes)
- Separate pooled read connection for queries

## 3. Vertical Slice Architecture
Principles:
1. Organize by feature/slice, not by technical layer.
2. Each slice owns its query handlers, command handlers, and templ views.
3. Infrastructure packages are shared cross-cutting concerns only.
4. Route registration passes dependencies directly to handler factories.
5. No hidden container/service locator/global mutable dependencies.

Target folder layout:
```text
receipter/
├── cmd/
│   ├── dotenv/
│   ├── seedAdmin/
│   └── receipter/
│       └── logs/
├── frontend/
│   ├── login/
│   ├── pallets/
│   │   ├── labels/
│   │   ├── receipt/
│   │   └── progress/
│   ├── stock/
│   ├── exports/
│   ├── settings/
│   ├── adminUsers/
│   ├── shared/
│   │   ├── context/
│   │   ├── html/
│   │   └── nav/
│   └── styles/
├── infrastructure/
│   ├── cache/
│   ├── http/
│   │   ├── assets/
│   │   ├── server.go
│   │   └── routes.go
│   ├── rbac/
│   ├── session/
│   ├── sqlite/
│   │   ├── db.go
│   │   ├── tx.go
│   │   └── migrations/
│   └── audit/
├── models/
└── resources/
    └── frontend_design/
```

Example slice file structure (`frontend/*`):
```text
frontend/
├── login/
│   ├── createLogin_db.go
│   ├── createLogin_handler.go
│   ├── getLoginScreen.templ
│   ├── getLoginScreen_handler.go
│   ├── getLoginScreen_templ.go
│   └── logout_handler.go
├── pallets/
│   ├── labels/
│   │   ├── palletLabels.templ
│   │   ├── palletLabels_db.go
│   │   ├── palletLabels_handler.go
│   │   └── palletLabels_templ.go
│   ├── progress/
│   │   ├── palletProgress.templ
│   │   ├── palletProgress_db.go
│   │   ├── palletProgress_handler.go
│   │   └── palletProgress_templ.go
│   └── receipt/
│       ├── palletReceipt.templ
│       ├── palletReceipt_db.go
│       ├── palletReceipt_db_test.go
│       ├── palletReceipt_handler.go
│       ├── palletReceipt_templ.go
│       ├── palletReceipt_types.go
│       └── palletReceipt_scan_modal_view.go
├── stock/
│   ├── stockImport.templ
│   ├── stockImport_db.go
│   ├── stockImport_db_test.go
│   ├── stockImport_handler.go
│   └── stockImport_templ.go
├── exports/
│   ├── exports.templ
│   ├── exports_db.go
│   ├── exports_db_test.go
│   ├── exports_handler.go
│   └── exports_templ.go
├── adminUsers/
│   ├── users.templ
│   ├── users_db.go
│   ├── users_db_test.go
│   ├── users_handler.go
│   └── users_templ.go
├── settings/
│   ├── settings.templ
│   ├── settings_db.go
│   ├── settings_handler.go
│   └── settings_templ.go
└── shared/
    ├── context/
    │   └── context.go
    ├── html/
    │   ├── layout.templ
    │   ├── layout_templ.go
    │   ├── navigation.templ
    │   └── navigation_templ.go
    └── nav/
        └── topnav.go
```

Dependency passing convention:
```go
func (s *Server) RegisterPalletRoutes(r chi.Router) {
    r.Get("/pallets/{id}/receipt", palletreceipt.ReceiptPageQueryHandler(s.DB, s.SessionCache))
    r.Post("/api/pallets/{id}/receipts", palletreceipt.CreateReceiptCommandHandler(s.DB, s.Audit))
}
```

## 4. MVP Outcomes
1. Generate A4 landscape pallet labels with Code128 barcode IDs starting at `1` and printed date.
2. Scan pallet barcode and open the receipt screen for that pallet.
3. Search stock by SKU or description and record quantity, batch, expiry, carton barcode, item barcode, and no-barcode flags.
4. Import stock master data from CSV with required header `sku,description`.
5. Export receipt data to CSV (per pallet and bulk) with columns:
`pallet_id,sku,description,qty,item_barcode,carton_barcode,expiry,batch_number`
6. Generate pallet content labels (pallet ID + contents).
7. Support pallet lifecycle: open -> closed -> reopened.
8. Show pallet progress page and export pallet status CSV.
9. Support mobile rear-camera scanning via QuaggaJS modal.
10. Enforce scanner/admin permissions and capture audit trail.

## 5. Delivery Plan

### Phase 0: Foundations + Skeleton
Tasks:
1. Create vertical-slice folder structure shown above.
2. Add dependencies (chi, bun, sqlite3 driver, templ, datastar, barcode library).
3. Implement config loading (port, db path, environment).
4. Initialize DB handles:
- `OpenWriteDB()` for single writer
- `OpenReadDB()` for pooled reads
5. Add transaction helpers:
- `WithReadTx(ctx, fn)`
- `WithWriteTx(ctx, fn)`
6. Build `infrastructure/http/server.go`:
- `Server` struct for shared dependencies
- middleware wiring
- start/stop lifecycle
7. Build `infrastructure/http/routes.go`:
- `RegisterLoginRoutes`
- `RegisterPalletRoutes`
- `RegisterStockRoutes`
- `RegisterExportRoutes`
8. Enforce constructor-style handler wiring per slice (explicit dependency passing).
9. Create initial files for each slice using the naming convention shown above.
10. Add auth/session middleware with role checks (`scanner`, `admin`).
11. Add `/health` endpoint.

Acceptance:
- App starts and serves `/health`.
- Folder structure matches vertical-slice design.
- Slice files follow agreed naming pattern across `frontend/*`.
- Route registration passes dependencies explicitly to handlers.
- DB-facing code uses transaction wrappers only.

### Phase 1: Data Model + Migrations
Tasks:
1. Create migrations for:
- `users`: `id`, `username`, `password_hash`, `role`, timestamps
- `sessions`: `id`, `user_id`, `expires_at`, timestamps
- `stock_items`: `id`, `sku` (unique), `description`, timestamps
- `pallets`: `id`, `status` (`open|closed`), `created_at`, `closed_at`, `reopened_at`
- `pallet_receipts`: `id`, `project_id`, `pallet_id`, `sku`, `description`, `qty`, `batch_number`, `expiry_date`, `carton_barcode`, `item_barcode`, `no_outer_barcode`, `no_inner_barcode`, timestamps
- `stock_import_runs`
- `export_runs`
- `audit_logs`: `id`, `user_id`, `action`, `entity_type`, `entity_id`, `before_json`, `after_json`, `created_at`
2. Add indexes:
- `stock_items(sku)`
- `stock_items(description)`
- `pallet_receipts(pallet_id)`
- `pallets(status)`
- `audit_logs(entity_type, entity_id)`
3. Add constraints:
- `qty > 0`
- `status` enum constraint
- `expiry_date` required
- if `no_outer_barcode=true`, carton barcode may be empty
- if `no_inner_barcode=true`, item barcode may be empty
- no uniqueness constraints on carton/item barcodes

Acceptance:
- Migrations run on empty DB.
- Invalid records fail constraints.
- Data access path is transaction-only for reads and writes.

### Phase 2: Login/Admin Slice
Tasks:
1. Implement `frontend/login`:
- login page query handler
- login command handler
- logout handler
2. Implement minimal `frontend/adminUsers` for admin user management/seed verification.
3. Wire RBAC checks for scanner/admin route access.

Acceptance:
- Scanner/admin can authenticate.
- Role restrictions are enforced in middleware/routes.

### Phase 3: Pallet Label Slice
Tasks:
1. Implement `frontend/pallets/labels` query/command handlers.
2. Add service to allocate next pallet ID sequentially (starting at `1`).
3. Generate Code128 image for pallet ID.
4. Build A4 landscape PDF label template with:
- pallet ID
- barcode
- print date in `DD/MM/YYYY`
5. Add routes:
- `POST /pallets/new`
- `GET /pallets/{id}/label`
- `GET /scan/pallet`

Acceptance:
- User can generate and print A4 landscape PDF labels.
- Scanning pallet code opens correct receipt page.

### Phase 4: Pallet Receipt Slice
Tasks:
1. Implement `frontend/pallets/receipt`:
- pallet receipt page query handler
- receipt create/update command handler
- pallet line list query
2. Add stock search endpoint:
- `GET /api/stock/search?q=...`
3. Implement merge rule:
- if same pallet + SKU + batch + expiry exists, increment qty
- include blank batch/blank expiry merge case
4. Validate expiry as full date and render UK format.
5. Apply role rules:
- scanners can receipt active pallets
- admins can receipt active and closed pallets

Acceptance:
- User can receipt multiple items per pallet.
- Merge behavior matches locked rule.
- Permission checks match scanner/admin policy.

### Phase 5: Scanner UX Slice
Tasks:
1. Add scanner modal in `frontend/pallets/receipt`:
- camera start/stop
- rear camera preference
- close controls
2. Add scan buttons for carton and item barcode inputs.
3. On decode success, populate target input and close modal.
4. Keep manual entry fallback.

Acceptance:
- Mobile scanning works for both barcode fields.
- Modal always closes cleanly.

### Phase 6: Stock Import + Export Slices
Tasks:
1. `frontend/stock` import handlers:
- `GET /stock/import`
- `POST /stock/import`
- enforce header `sku,description`
- upsert by `sku`
- import summary output
2. `frontend/exports` handlers:
- `GET /exports/pallet/{id}.csv`
- `GET /exports/receipts.csv`
- columns:
`pallet_id,sku,description,qty,item_barcode,carton_barcode,expiry,batch_number`
- export dates as `DD/MM/YYYY`
3. Status export:
- `GET /exports/pallet-status.csv`
- include pallet id, status, line count, created/closed/reopened timestamps

Acceptance:
- Import rejects missing/invalid headers.
- Export columns and date format match requirements.

### Phase 7: Progress/Lifecycle Slice
Tasks:
1. Implement `frontend/pallets/progress`:
- open pallets
- closed pallets
- receipt counts
- links to pallet details
2. Add lifecycle endpoints:
- `POST /api/pallets/{id}/close`
- `POST /api/pallets/{id}/reopen`
3. Keep admin edit capability for closed pallets.
4. Add pallet content label generator:
- pallet ID
- contents list (SKU, qty, batch, expiry)

Acceptance:
- Close/reopen state changes are visible on progress page.
- Admin can still edit closed pallets.
- Content label reflects current pallet state.

### Phase 8: Audit, Hardening, and Test Coverage
Tasks:
1. Unit tests:
- pallet ID allocation
- receipt merge logic
- CSV import header validation
- export formatter and UK date output
- receipt validation logic
2. Integration tests:
- create pallet -> receipt -> close -> admin edit closed pallet -> reopen -> export
3. Add request logging + request IDs.
4. Add audit log writes for:
- receipt create/update
- pallet close/reopen
- stock import
5. Add transaction policy tests:
- fail when DB op is attempted outside tx wrapper
- rollback on handler/service failures
6. Add backup strategy for SQLite file.

Acceptance:
- Core flow is test-covered.
- Audit entries are generated for required actions.
- Transaction-only policy is test-verified.

## 6. Suggested Route Map
- `GET /login`
- `POST /login`
- `POST /logout`
- `GET /` dashboard/progress
- `POST /pallets/new`
- `GET /pallets/{id}`
- `GET /pallets/{id}/receipt`
- `GET /pallets/{id}/label`
- `GET /pallets/{id}/content-label`
- `POST /api/pallets/{id}/receipts`
- `POST /api/pallets/{id}/close`
- `POST /api/pallets/{id}/reopen`
- `GET /api/stock/search`
- `GET /stock/import`
- `POST /stock/import`
- `GET /exports/pallet/{id}.csv`
- `GET /exports/receipts.csv`
- `GET /exports/pallet-status.csv`

## 7. Definition of Done (MVP)
1. Folder layout and route wiring follow vertical-slice architecture.
2. Handler constructors take explicit dependencies from router/server wiring.
3. Pallet labels generate as A4 landscape PDFs with scannable Code128 IDs and UK-format date.
4. End-to-end receipting works via scan or manual entry.
5. Merge logic works only for same pallet+SKU+batch+expiry (including blank batch+expiry pairs).
6. CSV import requires header `sku,description`.
7. Per-pallet and bulk exports include `pallet_id` and `qty` with UK-format dates.
8. Scanner/admin permissions are enforced, including admin edits on closed pallets.
9. Audit logs exist for receipt and pallet status changes.
10. All DB reads and writes run inside explicit transactions.

## 8. Open Questions
None currently.
