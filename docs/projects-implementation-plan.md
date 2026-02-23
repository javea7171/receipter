# Project-Scoped Receipting Implementation Plan

## Objective
Introduce first-class `projects` so operations are scoped per client project.

Example target workflow:
1. Admin creates a project for client `Boba Formosa`.
2. Admin opens that project as the entry point, then views/creates pallets under it (label includes client name).
3. Admin imports stock codes for that project only.
4. Exports include data for that project only.

This repo is greenfield, so schema can be changed in place.

## Greenfield Assumption (Important)
- This is a greenfield project.
- We can alter the schema in place without production backward-compatibility constraints.
- We can update `infrastructure/sqlite/migrations/001_init.sql` directly and reshape tables as needed.
- If local/dev data must be preserved, use backfill steps below; otherwise a clean rebuild of the DB is acceptable.

## Scope
In scope:
- New `projects` domain model.
- Project selection context in app.
- Project-scoped pallets, stock imports, stock search, and exports.
- Pallet label update to show client name.
- Migration/backfill strategy for existing rows.
- Tests for project isolation.

Out of scope for initial cut:
- Advanced project permissions (e.g. per-user project membership matrix).
- Cross-project analytics UI.

## Current State (baseline)
- `pallets`, `stock_items`, `stock_import_runs`, `export_runs` are global.
- `stock_items.sku` is globally unique.
- Exports are global or per pallet id, not project filtered.
- Pallet labels do not include client/project identity.

## Target Data Model

### New table: `projects`
Recommended columns:
- `id INTEGER PRIMARY KEY AUTOINCREMENT`
- `name TEXT NOT NULL` (project name, e.g. `Receipt Run Feb 2026`)
- `description TEXT NOT NULL` (human-readable project summary)
- `project_date DATE NOT NULL` (explicit project date shown in UI)
- `client_name TEXT NOT NULL` (e.g. `Boba Formosa`)
- `code TEXT NOT NULL UNIQUE` (short stable key/slug, optional but useful)
- `status TEXT NOT NULL CHECK (status IN ('active', 'inactive')) DEFAULT 'active'`
- `created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP`
- `updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP`

### Add `project_id` to core tables
- `pallets.project_id INTEGER NOT NULL REFERENCES projects(id)`
- `stock_items.project_id INTEGER NOT NULL REFERENCES projects(id)`
- `stock_import_runs.project_id INTEGER NOT NULL REFERENCES projects(id)`
- `export_runs.project_id INTEGER REFERENCES projects(id)` (nullable for legacy/system events if needed)

Optional (recommended) for query simplicity:
- `pallet_receipts.project_id INTEGER NOT NULL REFERENCES projects(id)`
  - Allows direct filtering and consistency checks.
  - If omitted, project can still be derived through `pallets`.

### Constraints and indexes
- Replace global SKU uniqueness with project-scoped uniqueness:
  - Drop `idx_stock_items_sku` unique assumption.
  - Add unique index: `(project_id, sku)`.
- Add indexes:
  - `pallets(project_id, id)`
  - `stock_items(project_id, sku)`
  - `pallet_receipts(project_id, pallet_id)` (if column added)
  - `stock_import_runs(project_id, created_at)`
  - `export_runs(project_id, created_at)`

## Schema Plan (in-place, greenfield)

### Phase A: Schema introduce + backfill
1. Create `projects` table.
2. Insert one default project for existing data:
   - `name='Default Project'`
   - `client_name='Default Client'`
   - `code='default'`
3. Add nullable `project_id` columns first.
4. Backfill all existing rows to default project id.
5. Enforce `NOT NULL` and foreign keys after backfill.
6. Replace SKU uniqueness with `(project_id, sku)` unique index.

Notes:
- SQLite has limited `ALTER TABLE` behaviors; use table rebuild pattern where needed:
  - create temp table with new schema
  - copy data
  - drop old table
  - rename temp table
- Keep migration idempotent and transactional where possible.
- Because this is greenfield, we do not need to preserve strict backward compatibility across old schema versions.

### Phase B: App code transition
Ship code that always requires a resolved project context before write/query operations.

## Application Behavior Changes

### 1) Project context resolution
Introduce a single project context resolver used by handlers:
- Priority:
  1. explicit query/path selection (if present)
  2. session `active_project_id`
  3. fallback to first `active` project for admin
- If no project available:
  - redirect to project selection/creation screen.

Implementation options:
- Add `active_project_id` to server-side session store; or
- persist in signed cookie + validate against DB each request.

Recommended: server-side session field for consistency.

### 2) Admin project management UI
Add admin-only pages:
- `GET /tasker/projects` list projects (primary entry point to access pallets).
- `POST /tasker/projects` create project.
- `POST /tasker/projects/{id}/activate` set active project in session.

Fields for create:
- project name
- project description
- project date
- client name
- optional code (auto-slug if omitted)
- status (`active` default)

Additional admin actions:
- activate project (`status='active'`)
- deactivate project (`status='inactive'`)

Project list filtering:
- Default filter: `active` only.
- Additional filters: `all`, `inactive`.
- Selected filter should persist in query string so links/bookmarks are stable.

Navigation flow:
- Projects page should be the normal entry point before pallets.
- Opening a project sets session `active_project_id` and navigates to that project's pallet progress screen.

### 3) Pallets scoped to project
- `CreateNextPallet` must attach `project_id` from active project.
- Progress list query filters by active project.
- Close/reopen/view/receipt endpoints verify pallet belongs to active project.
- Pallet label PDF includes `client_name` and optionally `project name/code`.

Project status behavior:
- On `inactive` projects, pallet and receipt pages are read-only.
- Scanner who scans/opens a pallet on an `inactive` project can view pallet details, but cannot add/edit receipts or perform other modifying actions.
- Admin should also be prevented from mutating inactive-project data unless project is re-activated (recommended for consistency).

Pallet ID strategy:
- Keep global numeric pallet IDs to preserve current scanner flow and URLs.
- Scope listing/actions by project filter and ownership checks.

### 4) Stock import + stock search scoped to project
- CSV import upsert key becomes `(project_id, sku)`.
- Stock list page shows only active project records.
- Single/bulk delete only affects current project records.
- Stock search (`/tasker/api/stock/search`) filters by active project.
- Receipt save should only resolve stock items from same project as pallet.

### 5) Exports scoped to project
- Exports page operates on active project.
- Receipt export query filters by project.
- Pallet status export filters by project.
- Optional: include project code in filename.
- `export_runs` rows include `project_id`.

## RBAC and Navigation
- Add admin menu item `Projects` (in addition to `Imports`/`Exports`).
- Keep scanner navigation minimal.
- Scanner behavior in project mode:
  - sees only pallets and scan for active project.
  - opening a pallet id outside active project returns `404` or `403` (pick one and standardize).
  - if scanner opens a pallet from an `inactive` project, UI is view-only.

Entry point update:
- Pallet browsing should begin from projects (not directly from a global pallet list).
- Existing pallet progress route can redirect to active project pallets when no project is explicitly selected.

## Audit Logging
Add project context to relevant audit records:
- Include project id in `entity_id` pattern or `after_json`.
- New actions:
  - `project.create`
  - `project.activate`
  - existing actions continue (`stock.import`, `stock.delete`, `pallet.*`, `receipt.*`) with project-aware payload.

## Testing Plan

### Unit tests
- Migration/backfill tests:
  - existing rows assigned default project.
  - composite `(project_id, sku)` uniqueness enforced.
- Stock import tests:
  - same `sku` allowed across different projects.
  - upsert updates only inside same project.

### Integration tests
- Project creation and activation.
- Project deactivation.
- Project listing defaults to `active`, with working `all` and `inactive` filters.
- Pallet creation appears only in active project.
- Scanner/admin cannot action pallet outside active project.
- Stock import/list/delete isolated by project.
- Export outputs include only active project data.
- Scanner can view (but not modify) scanned pallet details on inactive projects.

## Rollout Sequence
1. Add migration(s) and default project backfill.
2. Add project repository/service + context resolver.
3. Add project management UI and activate flow.
4. Project-scope pallets and receipt paths.
5. Project-scope stock import/search.
6. Project-scope exports.
7. Update labels with client name.
8. Add/refresh tests and validate full suite.

## Acceptance Criteria
- Admin can create a project for `Boba Formosa`.
- Project includes clear `name`, `description`, and `project_date`.
- New pallets created while that project is active carry that project and show client on label.
- Stock imported in that project is not visible in other projects.
- Exports only include rows from active project.
- Scanner flow still works with pallet id scan, but data access is project-consistent.
- If project is inactive, scanner can still view scanned pallet details but cannot modify receipt data.
- Projects screen is the primary entry point for viewing pallets, and defaults to showing active projects only with options for all/inactive.

## Open Decisions
1. Should scanners be constrained to one project each (membership table), or just current active project?
2. Do we want project-specific pallet numbering, or keep global IDs (recommended: keep global for now)?
3. For cross-project pallet ID access, return `404` (not found) or `403` (forbidden)?
