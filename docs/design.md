[English](design.md) · [中文](design.zh.md)

# mdm/product design

Conclusions only, for whoever changes this component's design. How to use it: `BRICKKIT.md`; how the code is laid out: `AGENTS.md`.

## Boundaries

This component is the one record of what a product is: SKU, name, category, base unit of measure, tracking policy (`tracking_type`), standard cost, status, and the units of measure with the factors between them.

| Not here | Owner | Why |
|---|---|---|
| Stock quantities, the actual batch and serial numbers | `erp/inventory` | This component says whether a product is received with a batch or serial number (master data), not which one (a transaction) |
| Selling prices, price lists, discounts | `erp/sales` | Pricing lives with the sale; `standard_cost` is a cost for finance, not a price for customers |
| Actual cost (moving average, FIFO) | `erp/inventory`, `erp/finance` | Actual cost is computed from movements; mixing it with the maintained standard cost would make cost-variance analysis meaningless |
| Bills of materials, routings | a manufacturing component | This component describes one product, not how products are made of each other |

Deleting a product is disabling it (`POST /products/{id}/status`, its own permission key `mdm.product.set_status`); there is no delete endpoint, because orders and stock movements keep referring to the product. Enabling and disabling is separated from editing because a disabled product disappears from every picker: a permission to fix a name should not carry that.

## Owned data

| Table | Partitioned | Notes |
|---|---|---|
| `products` | no | `sku` unique (`products_sku_uniq`); `tracking_type` `NONE` / `BATCH` / `SERIAL`; `standard_cost NUMERIC(18,2)`; `status` `ACTIVE` / `DISABLED`; `version` for optimistic locking; foreign keys to `uoms` (base unit) and `product_categories` |
| `uoms` | no | `code`, `category` (`count`, `weight`, …), `is_base` (exactly one base unit per category), `rounding NUMERIC(18,6)` |
| `uom_conversions` | no | one row per non-base unit: `factor NUMERIC(18,6)`, 1 unit = `factor` base units of its category |
| `product_categories` | no | category tree as a materialised path (`path`, prefix index): a subtree is one prefix match, no recursive query |
| `command_idempotency` | no | `idempotency_key` primary key, the result ID of the first execution |
| `event_outbox`, `event_inbox` | weekly, by `created_at` | the outbox the pump publishes from; partitions are created four weeks ahead by the component itself |

There is no terminal state: `DISABLED` means "not usable for new business", and a product can be re-enabled. `sku` is generated as `P` + six digits from the ID sequence when the caller leaves it empty, and taken as given otherwise; a taken SKU is a conflict (`409` / `ALREADY_EXISTS`), not a server error.

Units are reference data seeded by the migration `003_seed_uoms`: `EA` (each, base of `count`), `BOX` (= 12 EA), `KG` (base of `weight`, rounding 0.001), `G` (= 0.001 KG). There is no API to manage units or categories: new unit categories are rare, and a full authorised CRUD for them costs more than a migration. A customer with other packaging changes the seeded factors in its own fork.

**Unit conversion** (`ConvertQuantity`, the only rule here with no reference implementation to check against):

- Factors are `NUMERIC(18,6)`, not floats: a conversion factor is the same precision problem as money, and it multiplies into order quantities and amounts.
- One direction only (`factor` towards the base unit); the inverse is computed, never stored, so two stored values can never disagree.
- Units of different categories are refused (`InvalidArgument`); there is no guessing.
- The result is rounded **up** (`CEIL`) to the target unit's `rounding`: when issuing, "2.3 boxes needed" must become 3 boxes. Callers that receive goods and want a different rounding handle it themselves; the default errs on the side of over-stocking, never under-shipping.
- Arithmetic and rounding run in SQL on `NUMERIC`; `trim_scale` only drops trailing zeros from the displayed result.

## Contract surface

gRPC `mdm.product.v1.ProductService`, for other components:

| rpc | Kind | Notes |
|---|---|---|
| `Create` | write | idempotent by `idempotency_key`; taken SKU → `ALREADY_EXISTS`; unknown unit or category → `INVALID_ARGUMENT` |
| `Update`, `SetStatus` | write | idempotent, plus the current `version`; a stale version → `ABORTED`, a missing product → `NOT_FOUND`; `SetStatus` accepts only `ACTIVE` / `DISABLED` (unspecified → `INVALID_ARGUMENT`) |
| `Get`, `List` | read | `List` pages by cursor, never by offset; default window the last 90 days; keyword `q` (below) |
| `BatchGet` | read | the batched read callers use instead of a loop of `Get`; missing or non-numeric IDs in `missing_ids` |
| `GetSummary` | read | ID, SKU, name, base unit, tracking type, status, version: what a summary copy stores |
| `ConvertQuantity` | read | no write; `qty` is a decimal string |

REST under `/mdm/product`, every route behind a key: `GET /products` and `GET /products/{id}` (`mdm.product.view`), `POST /products` (`mdm.product.create`), `PATCH /products/{id}` (`mdm.product.edit`; name, category and standard cost are replaced together), `POST /products/{id}/status` (`mdm.product.set_status`), `POST /products/convert-quantity` (`mdm.product.view`: a dry run that reads conversion factors, part of looking at a product). `BatchGet` and `GetSummary` are gRPC only: no end-user screen asks for a list of product IDs, and on the public API they would invite use as a bulk export. `ConvertQuantity` is on REST because the order-entry page must show "3 boxes = 36 each" live and the rule lives only here. There is no status endpoint for timed-out writes: no component writes here synchronously as part of its own transaction.

Caller mistakes answer `4xx`, never `500`: a malformed cursor or time parameter, a decimal that is not plain digits (no exponent, `NaN` or thousands separator), a non-numeric ID (treated as not found).

**Keyword search** (`GET /products?q=`, gRPC `ListRequest.q`), for the product pickers (order entry, opportunity entry, the mobile BFF's `searchProducts`, which calls gRPC):

- `q` is trimmed; empty means no filter, so the list is exactly the unfiltered one. It combines with `status_filter`.
- A row matches when `lower(sku)` or `lower(name)` contains `lower(q)`. Matching uses `strpos` and `starts_with`, not `LIKE`: `%` and `_` in user input are literal characters and need no escaping.
- Rank 0 when SKU or name starts with `q`, rank 1 when it only contains it. Order: rank, then `created_at` descending, then `id` descending.
- The rank is part of the keyset: the cursor carries it once paging reaches rank 1, so pages never skip or repeat a row at the boundary. Unfiltered lists encode `created_at` and `id`; ranked pages add the rank in front.
- The default 90-day window applies, as to every list in the project; a picker that must find older products passes `created_after`. REST reads `created_after` / `created_before` (RFC 3339; malformed → `400`).
- No index. The contains branch must scan the rows in the window whatever index exists, and a prefix index cannot serve an `OR` with it; the table is master-data sized (thousands to a few hundred thousand SKUs). If it ever grows, a trigram index (`pg_trgm`) is the answer, not a prefix index.

## Events

Published, all through the outbox in the same transaction as the write:

| Subject | When | Payload |
|---|---|---|
| `mdm.product.created.v1` | `Create` | id, SKU, name, base unit, tracking type, standard cost, status, version |
| `mdm.product.updated.v1` | `Update`, and `SetStatus` back to `ACTIVE` | the same fields |
| `mdm.product.disabled.v1` | `SetStatus` to `DISABLED` | the same fields (the contract requires only id and version) |

`tracking_type` must stay in every payload: `erp/inventory` keeps a summary copy from these events to decide whether a receipt needs a batch or serial number. There is no "enabled" subject: re-enabling is an update whose payload carries the status. A change of standard cost is an ordinary update too; a dedicated subject is added only when a consumer needs "cost changes only", because a subject, once published, can never be removed. Consumers apply an event only when its `version` is greater than the one they hold, which makes them immune to reordering and redelivery.

Consumed: none. Consuming an event would make master data depend on someone.

## Dependencies

None, by design. Expected but absent:

| Not a dependency | Why |
|---|---|
| any business component | master data is read by everyone and calls no one |
| `erp/inventory` | counter-intuitive but deliberate: this component does not know stock. A product page that shows stock asks both components and combines the answers (in the BFF or the frontend), so no backend edge is needed |
| `infra/iam-casdoor` | tokens are verified locally against the JWKS at `IAM_JWKS_URL`: configuration, not an edge |
| `infra/authz` | the permission bundle is polled from `AUTHZ_BUNDLE_URL`: configuration, not an edge |
| NATS | a base resource reached through `NATS_URL`, not a component |

## Place in the synchronous call graph

A leaf: only incoming edges. `erp/sales`, `crm/opportunity` and the mobile BFF call it over gRPC; it calls no one. It is one of the three hubs of the project (master data read by all; `erp/inventory` the only writer of stock movements; `erp/finance` mostly commanded and listening). Being read by both CRM and ERP does not break "no synchronous edge between CRM and ERP": this component is neither.

## Partitioning and archiving

`products`, `product_categories`, `uoms` and `uom_conversions` are not partitioned and never archived: master data does not grow with time, and with no terminal state there is never a moment when a row is "finished". A disabled product can never be deleted or archived either: orders from years ago and old stock movements still refer to its ID, and `BatchGet` not finding it would blank those documents. The schema `mdm_product_archive` may exist by project convention; nothing writes to it, and `BatchGet`'s archive fallback finds no table and returns the hot rows only. `event_outbox` / `event_inbox` are partitioned weekly and roll forward with the partitions.

## Data scopes

None (`data_scopes: none`). Product master data is reference data that sales (quoting), the warehouse (receiving and issuing) and finance (costing) must all see the same way; there is no "only my products". So the tables have no owner or department columns.

## Reference implementations

| Project | Version | Module consulted | What was borrowed | License | Usage |
|---|---|---|---|---|---|
| Odoo | 17.0 | `addons/product/models/product_uom.py` (`uom.uom`) | conversion within a unit category, with a base unit of factor 1; refusing conversions across categories | LGPL-3 | Borrowed reasoning |
| Odoo | 17.0 | `addons/stock/models/stock_lot.py`, the `product.tracking` field | tracking (`none` / `lot` / `serial`) is an attribute of the product, not of the batch | LGPL-3 | Borrowed reasoning |
| Apache OFBiz | 18.12 | `applications/product/entitydef/entitymodel.xml` | `Product` / `ProductCategory` / `UomConversion`: a check that no essential field is missing | Apache-2.0 | Borrowed reasoning |

**Deliberately avoided**: Odoo's `uom.uom` stores both `factor` and `factor_inv` as floats; floats plus a redundant inverse eventually disagree, and the error multiplies into order amounts. Here: one direction, `NUMERIC(18,6)`, computed on the fly. Also not followed: ERPNext's per-item conversion table (`UOM Conversion Detail`), which repeats the same conversions on every item; conversions are a property of the unit here.

No slot family: the two conversion models are two ways to model the same need, and the per-unit one is plainly better (one change applies everywhere).

## Open questions

| Question | Current answer |
|---|---|
| Who maintains `standard_cost`, and does a cost change need its own event? | Maintained by people through `Update`; it travels in `mdm.product.updated.v1`. A dedicated subject waits for a consumer that needs it |
| Configurable attributes (colour, size, variants)? | Not in the standard component: dynamic attribute tables lose typing for code and tools alike. Real variant needs become a fork or a slot family |
| Is rounding up right for every caller? | It is the default because under-shipping is the worse error. If receiving turns out to need another rounding, it becomes an explicit request parameter whose default stays `CEIL` |
| Categories have a table but no API, so `category_id` can only be empty today | Not yet needed by any screen; a category API is an append-only contract change |
| Should keyword search ignore the default 90-day window? | No, for now: the project convention applies to every list, and pickers pass `created_after`. Revisit if pickers find it awkward (the frontend work decides) |
| Two concurrent requests with the same `idempotency_key`: the loser gets an error, not the winner's result | Only one row lands, which is the promise; returning the winner's result to the loser would need a retry on the primary-key conflict |
