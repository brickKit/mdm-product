[English](AGENTS.md) · [中文](AGENTS.zh.md)

# mdm/product

The AI guide to developing this component. How to use it, its boundaries and contracts: BRICKKIT.md. Why it is shaped this way: `docs/design.md`. Dependencies, configuration and deployment: component.yaml.

## Code map

| Path | Owns |
|---|---|
| `backend/module/module.go` | The only entry, `New(ctx, rt)`: builds repo → service → HTTP + gRPC, starts the outbox pump and the partition loop. Same function standalone and in a shell |
| `backend/cmd/server/main.go` | One line, `besdk.RunStandalone(module.New)` |
| `backend/cmd/migrate/main.go` | One line, `migrate.Main(migrations.FS)`: the migration container's entry (`./migrate up`) |
| `backend/internal/repo/repo.go` | Shared types, sentinel errors (`ErrVersionConflict`, `ErrNotFound`, `ErrInvalidCursor`, `ErrSKUTaken`, `ErrInvalidReference`), `classifyWriteErr`, the column list and scan functions |
| `backend/internal/repo/write.go` | The three write commands; each runs idempotency lookup → write → record key → outbox in one transaction (`updateWithVersion` shared by `Update` and `SetStatus`) |
| `backend/internal/repo/read.go` | `BatchGet`, `List` (keyword `q`, ranking, keyset paging), the SQL builder `listSQL` |
| `backend/internal/repo/convert.go` | `ConvertQuantity`: the arithmetic and the rounding run in SQL on `NUMERIC` |
| `backend/internal/repo/cursor.go` | The list cursor: base64 of `created_at` and `id`, plus the match rank once paging reaches the contains-only matches |
| `backend/internal/service/` | Input validation (decimal strings, status values) and the error → gRPC status mapping (`status.go`), shared by REST and gRPC |
| `backend/internal/http/http.go` | REST routes, each registered with its permission key; `parseListInput` |
| `backend/internal/grpc/grpc.go` | `mdm.product.v1.ProductService` and the enum mapping |
| `backend/internal/partition/` | Background loop creating weekly partitions of `event_outbox` / `event_inbox` four weeks ahead |
| `migrations/` | SQL migrations (tables, outbox, the seeded units of measure), embedded by `migrations/embed.go` |
| `contracts/` | proto, OpenAPI, event schema |
| `gen/mdm/product/` | Generated Go code: a nested Go module, tagged on its own as gen/mdm/product/v1.x.y; never edited by hand |
| `scripts/` | `seed.sh` / `seed-clean.sh` for local demo data |

| Feature | Start here | Then |
|---|---|---|
| Keyword search `q` on the list | `backend/internal/repo/read.go` (`listSQL`) | `backend/internal/repo/cursor.go`, `backend/internal/repo/search_test.go` |
| A new REST query parameter | `backend/internal/http/http.go` (`parseListInput`) | `contracts/product.openapi.yaml`, `backend/internal/http/http_test.go` |
| Which permission key a route needs | `backend/internal/http/http.go` (`RegisterRoutes`) | `assembly.yaml` (`permissions`), `backend/internal/http/authz_test.go` |
| A new field on the product | `migrations/` (a new migration) | `contracts/` (append only), `buf generate`, `backend/internal/repo/repo.go` (`productColumns`, `scanProductRow`) |
| Unit conversion or rounding | `backend/internal/repo/convert.go` | `migrations/003_seed_uoms.up.sql`, the `TestConvertQuantity_` tests in `backend/internal/repo/repo_test.go` |
| An error answering with the wrong status | `backend/internal/service/status.go` | the sentinel errors and `classifyWriteErr` in `backend/internal/repo/repo.go` |

## Build and test

```bash
# tests run against the test database brickkit_test_db, never brickkit_db
# (in the BrickEnterprise project: make test-db-init ID=mdm/product at the project root prepares it)
export TEST_PG_DSN="postgres://<user>:<password>@localhost:5432/brickkit_test_db?sslmode=disable"
make test                    # every package ends in "ok"; refuses to run without TEST_PG_DSN
go test ./... -count=1 -v | grep -c -- '--- SKIP'   # 0: no test skipped
make check-version dag-check contract-check import-scan module-check   # each prints one ✓ line
make docs-check              # "0 with errors, 0 warnings"
# migrations, as the login role:
PG_HOST=localhost PG_PORT=5432 PG_DATABASE=brickkit_test_db PG_USER=mdm_product_rw \
  PG_PASSWORD=<its password> PG_SCHEMA=mdm_product make migrate-idempotent   # ✓ 迁移幂等
```

`contract-check` prints the tag it compares against (`buf breaking --against '.git#tag=…'`); it must be the last release, not `main`. After `buf generate` (contracts changed), the contract package needs a new tag: see Pitfalls. On a real machine, from the project root: `make verify ID=mdm/product ROUTE=/mdm/product/products FOCUS=1` builds the image, starts only what this component needs, checks migration, health and the permission check, runs a focus run, and tears down.

## Design decisions

- **Read by everyone, calls no one.** No dependencies, no consumed events: master data stays a leaf in the call graph. Callers fetch the products of an order or a page with one `BatchGet`, never a loop of `Get`.
- **No row-level scopes** (`data_scopes: none`): product master data is shared reference data.
- **Every write is idempotent and versioned.** `idempotency_key` is the primary key of `command_idempotency`, recorded in the same transaction as the write; `Update` / `SetStatus` carry the current `version`. Disabling is not terminal, so nothing is ever archived.
- **Enabling and disabling has its own key**, `mdm.product.set_status`, separate from `mdm.product.edit`.
- **Conversion arithmetic stays in `NUMERIC`**: factors are stored in one direction only (`uom_conversions.factor`, 1 unit = factor base units), the result is rounded up (`CEIL`) to the target unit's rounding, units of different categories are refused.
- **Search ranking lives in SQL**: `match_rank` (0 = SKU or name starts with `q`, 1 = only contains it) is part of the keyset; `strpos` / `starts_with` instead of `LIKE`. No index. The default 90-day window applies to search too.
- **Caller mistakes are 4xx**: a taken SKU (`ErrSKUTaken`), a unit or category that does not exist (`ErrInvalidReference`), a non-numeric ID (treated as not found), a malformed decimal or status value.

## Pitfalls

| Never | Symptom | Why |
|---|---|---|
| Add an entry to `dependencies.components` (above all `erp/inventory`) or consume an event | Everything builds and passes; the component stops being a leaf, and the next edge someone adds can close a cycle | Master data is read by all; stock shown on a product page is combined by the caller. `make dag-check` fails on any dependency |
| Add a column to `products` without updating `scanProductRow` in the same change | Every other test passes; `BatchGet` / `Get` fail with `sql: expected 12 destination arguments in Scan, not 11` | `besdk.BatchGetRouted` runs `SELECT *`, so the table's column order is the scan order; append new columns at the end and scan them |
| Drop `tracking_type` from an event payload | This component's tests stay green; `erp/inventory` stops knowing which products need a batch or serial number on receipt | Its summary copy is built from these events |
| Round a conversion down, or compute it in Go with `float64` | "Need 2.3 boxes" ships 2; small factors such as 0.001 drift | Issuing must never fall short; `NUMERIC` keeps the factor exact |
| Return the `standard_cost` the caller sent instead of reading it back | `Create` answers `"0"`, a later `Get` of the same row answers `"0.00"` | `NUMERIC(18,2)` normalises the value; `insertProduct` reads the row back with `RETURNING` |
| Change a comment or option in `contracts/mdm/product/v1/product.proto` without tagging the contract package | Builds pass locally (the `replace` hides it); a shell fetching `mdm-product/v2` compiles against the old `gen/` from the tag `go.mod` requires | Any change under `gen/mdm/product/` needs a new tag `gen/mdm/product/v1.x.y` and the root `go.mod` requiring it |
| Order the list by anything other than `match_rank, created_at DESC, id DESC`, or drop `rank` from the cursor | Paging skips or repeats rows exactly where prefix matches end and contains matches begin | The keyset condition in `listSQL` assumes this order; `TestList_q与游标一起翻页不重不漏` covers it |
| Bind `POST /products/{id}/status` back to `mdm.product.edit` | Tests elsewhere stay green; anyone who may rename a product may also make it disappear from every picker | `TestSetStatus路由要set_status键_只有edit键得到403` covers it |

## Before changing code

1. Is this about what the product is (here), about how much is in stock (`erp/inventory`) or about what it sells for (`erp/sales`)? Only the first goes here.
2. Am I adding a dependency, a consumed event, partitioning or a scope column? Stop: see Pitfalls and `docs/design.md`.
3. Contract change? Append only: a new field, a new rpc, a new query parameter. Never remove or retype a field, rpc, path or event subject.
4. Did `gen/` change? Then the contract package needs a new tag and `go.mod` must require it.
5. A new REST route is registered with `besdk.GET` / `POST` / `PATCH` and a permission key from `assembly.yaml`.
6. New rule → write the failing test first (real database for anything SQL), then the code.
7. Bump `metadata.version` before the first change after a release; update BRICKKIT.md and `docs/design.md` in the same commit as the code.

<!-- brickkit:managed:begin lang=en -->
<!-- maintained by brickkit (init, add, remove, upgrade, skills update): edits between these markers are overwritten -->

## BrickKit

This is a BrickKit component: `component.yaml` is all the platform reads. The rules it relies on:

- `configSchema` keys are the environment variable names the code reads. Never use a reserved name: `COMPONENT_ID`, `COMPONENT_VERSION`, `PORT`, `BRICKKIT_SERVED_MEMBERS`, `BRICKKIT_SERVED_MEMBERS_CONFIG`, or any `*_ENDPOINT`.
- Dependencies are exact versions. A dependency's address arrives as `<ID>_ENDPOINT`; an optional dependency that is absent has no variable at all, so read it with a fallback.
- `/healthz` checks only this process, never a dependency. The migration command runs from the same image and must fail on an argument it does not know.
- `BRICKKIT.md` travels to every project that uses this component and is read there without the repository: keep it in step with the code, with no relative links.
- Release: raise `metadata.version`, commit, push, `brickkit release`. `brickkit lint` checks the manifest and these docs — inside a project, run in this directory, it checks only this component (`--all` for the whole project).
- The full rules are in the `brickkit-component` skill (`.claude/skills/brickkit-component/SKILL.md` at the root of the project or repository where skills are installed; `brickkit skills update` installs it); for flags ask `brickkit <command> --help`; BrickKit's own documentation is `brickkit docs`.
<!-- brickkit:managed:end -->
