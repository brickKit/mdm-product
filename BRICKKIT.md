# mdm/product

## Purpose

The one record of what each product is: SKU, name, category, base unit of measure, whether it is tracked by batch or serial number, standard cost and status (active or disabled), plus the units of measure and the factors that convert between them. Every component that needs a product reads it from here, by ID, and keeps at most a summary copy updated from this component's events.

**Owns**

- Product master data: `sku` (unique; generated as `P` + six digits when the caller leaves it empty), name, category, base unit, `tracking_type` (`NONE`, `BATCH` or `SERIAL`), standard cost (a decimal string), status.
- Units of measure and their conversion factors, and the conversion itself (`ConvertQuantity`: rounds up to the target unit's rounding). The units are seeded by the migrations (`EA`, `BOX`, `KG`, `G`; 1 box = 12 each, 1 g = 0.001 kg); there is no API to manage them.
- Product categories (a table with a materialised path); there is no API to manage them yet.
- The events `mdm.product.created.v1`, `mdm.product.updated.v1`, `mdm.product.disabled.v1`.

**Does not own**

- Stock quantities and the actual batch or serial numbers: `erp/inventory`. This component says whether a product must be received with a batch or serial number, not which one.
- Selling prices, price lists and discounts: `erp/sales`.
- Actual cost (moving average or FIFO, computed from stock movements): `erp/inventory` and `erp/finance`. The standard cost here is a value people maintain.
- Who may see which rows: there are no row-level scopes here; product master data is visible to everyone who may open the product pages.

## Before you deploy

- **PostgreSQL**: a schema `mdm_product`, plus `mdm_product_archive` if your schema convention creates one (this component never writes to it). A login role `mdm_product_rw` with `USAGE` and `CREATE` on `mdm_product`, and its password. The migration runs as this role and creates the tables (and seeds the four units of measure), so the role owns them; that matters because the running component creates weekly partitions of `event_outbox` / `event_inbox` itself, which needs ownership. BrickKit creates none of this; in the BrickEnterprise assembly project `make dev-env` writes the password into `.env` and `make db-init` creates the schemas, role and grants.
- **NATS** reachable at `NATS_URL`: the component publishes its events through an outbox table and a background pump; it starts without NATS reachable, and the events wait in the outbox.
- **Authorization** (`infra/authz`) and **identity** (`infra/iam-casdoor`, or any IAM serving a JWKS) reachable at `AUTHZ_BUNDLE_URL` and `IAM_JWKS_URL` for the REST routes to answer anything but errors. They are configuration, not dependencies: the component starts without them.
- **Permission keys**: the role that may enable and disable products needs `mdm.product.set_status`; `mdm.product.edit` alone no longer allows it.
- Demo data (optional): `make seed` in the component directory creates fourteen sample products through the real gRPC API, once the component is running.

## Dependencies

None. Product master data is read by everyone and calls no one: other components call it over gRPC (`BatchGet` to fetch the products of one order or one page in one call); it never calls another component and consumes no events, so it can never sit in the middle of a call chain or a cycle. In particular it does not call `erp/inventory`: a product page that shows stock asks both components and combines the answers.

The authorization bundle and the JWKS are fetched from the URLs in the configuration, not through a dependency edge. Without them every protected route fails closed: no or invalid token → `401`; `IAM_JWKS_URL` empty or unreachable → `403`; the bundle never fetched yet → `503`; a valid user without the permission key → `403`. `/healthz` stays `200` throughout: it reports only that this process is alive.

## Configuration

| Variable | Meaning |
|---|---|
| `PG_HOST` | PostgreSQL host. Usually the project's shared value (`$var:PG_HOST`). |
| `PG_PORT` | PostgreSQL port; default `5432`. |
| `PG_DATABASE` | The database holding the `mdm_product` schema (`$var:PG_DATABASE`). |
| `PG_USER` | The login role, `mdm_product_rw` as a literal. Inside a shell the shell logs in with its own role and switches to this one per transaction (`SET LOCAL ROLE`), so the role name must be `<PG_SCHEMA>_rw`. |
| `PG_PASSWORD` | Password of `PG_USER`. Secret: write `${MDM_PRODUCT_DB_PASSWORD}` (or your secret store's reference), never the value. |
| `PG_SCHEMA` | Schema of all tables, the outbox and the migration state table (`schema_migrations_mdm_product`); default `mdm_product`. Write the literal anyway so every component's schema is visible in one place. |
| `NATS_URL` | NATS server the outbox pump publishes to (`$var:NATS_URL`). |
| `OTEL_BASE_URL` | OpenTelemetry collector base URL; empty (the default) exports nothing. |
| `AUTHZ_BUNDLE_URL` | URL of the authorization bundle the permission check polls, for example `http://infra-authz-2-0-0:8223/authz/bundle`. Required: without it every protected route answers `503`. Keep it in step with the authz version the project runs. |
| `IAM_JWKS_URL` | URL of the JWKS used to verify user tokens locally, for example `http://infra-iam-casdoor-2-0-0:8200/.well-known/jwks.json`. Required: without it every protected route answers `403`. |

## Contracts

- `contracts/mdm/product/v1/product.proto` — gRPC `mdm.product.v1.ProductService` for other components: `Create`, `Update`, `SetStatus` (each takes an `idempotency_key`; `Update` and `SetStatus` also take the current `version` and fail with `ABORTED` when it is stale, `NOT_FOUND` when the product does not exist; a taken `sku` → `ALREADY_EXISTS`; a unit or category that does not exist → `INVALID_ARGUMENT`), `Get`, `List` (keyword `q` on SKU or name, case-insensitive, prefix matches first; `status_filter`; `created_after` / `created_before`, default the last 90 days; cursor paging, no offset), `BatchGet` (missing IDs come back in `missing_ids`, not as an error), `GetSummary` (ID, SKU, name, base unit, tracking type, status, version — what a summary copy needs), `ConvertQuantity` (no write; rounds up to the target unit's rounding; units of different categories → `INVALID_ARGUMENT`). The Go package is the separate module `github.com/brickKit/mdm-product/gen/mdm/product`.
- `contracts/product.openapi.yaml` — REST under `/mdm/product`, every route behind a permission key: `GET /products` (`mdm.product.view`; the same `q`, `status_filter`, `created_after` / `created_before` and `cursor`, `page_size` as gRPC `List`), `GET /products/{id}` (`mdm.product.view`), `POST /products` (`mdm.product.create`), `PATCH /products/{id}` (`mdm.product.edit`), `POST /products/{id}/status` (`mdm.product.set_status`; "delete" is disabling), `POST /products/convert-quantity` (`mdm.product.view`). `BatchGet` and `GetSummary` are not on REST.
- `contracts/events/product.events.json` — events published through the outbox: `mdm.product.created.v1` (on create), `mdm.product.updated.v1` (on update and on re-enabling), `mdm.product.disabled.v1` (on disabling). Each carries the full product including `tracking_type` and the aggregate `version`; consumers apply an event only when its version is greater than the one they hold. No events are consumed.
- `assembly.yaml` — this project's metadata: the four permission keys above, the menu entry, the edge route `/mdm/product/**`, the schema and role, and `data_scopes: none`.

## Shell declaration

Not a shell. It can be hosted in a Go shell (in the BrickEnterprise project, `be/go-core`) or run on its own; the code is the same either way.
