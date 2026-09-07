// Package repo 是 mdm-product 的数据访问层：products/product_categories/
// uoms/uom_conversions 四张表 + Outbox 写入。跨组件读走 besdk.BatchGetRouted，
// List 的时间窗口走 besdk.ListWindow——这一层只管"接对了没有"，SDK 通用
// 逻辑本身的正确性由 be-sdk-go 自己的测试守（同 mdm-customer 的判据）。
package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	besdk "github.com/brickKit/be-sdk-go"
)

// Product 是 products 表的一行。StandardCost 一律用 string 传 decimal
// ——double 跨语言序列化会丢精度（设计计划 §3）。
type Product struct {
	ID           string
	SKU          string
	Name         string
	CategoryID   string // 空字符串表示未分类
	BaseUOMID    string
	TrackingType string // NONE / BATCH / SERIAL
	StandardCost string
	Status       string
	Version      int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// CreateInput 对应 CreateRequest（contracts/mdm/product/v1/product.proto）。
type CreateInput struct {
	IdempotencyKey string
	SKU            string // 留空则自动生成 "P" + 6 位自增数字（设计计划 §9）
	Name           string
	CategoryID     string
	BaseUOMID      string
	TrackingType   string
	StandardCost   string
}

// Repo 持有共享池 + 本组件的 role/schema，写操作一律经 besdk.WithTx 切换。
type Repo struct {
	db     *sql.DB
	role   string
	schema string
}

func New(db *sql.DB, role, schema string) *Repo {
	return &Repo{db: db, role: role, schema: schema}
}

// testHookAfterOutbox 仅供本包测试用（未导出，其他包摸不到）：Create 在
// PublishOutbox 成功之后、提交事务之前调它——测试借此验证"业务数据 +
// 事件必须同事务"，不必往公开 API 上挂一个永久的故障注入方法
// （同 mdm-customer 的判据，见 repo_test.go）。
var testHookAfterOutbox func() error

// Create 幂等：同一个 idempotency_key 重试返回同一条，不新建
// （command_idempotency 唯一约束去重）。
func (r *Repo) Create(ctx context.Context, in CreateInput) (*Product, error) {
	var out *Product
	err := besdk.WithTx(ctx, r.db, r.role, r.schema, func(tx *sql.Tx) error {
		existingID, err := lookupIdempotency(ctx, tx, in.IdempotencyKey)
		if err != nil {
			return err
		}
		if existingID != "" {
			p, err := getByID(ctx, tx, existingID)
			if err != nil {
				return err
			}
			out = p
			return nil
		}

		standardCost := in.StandardCost
		if standardCost == "" {
			standardCost = "0"
		}
		trackingType := in.TrackingType
		if trackingType == "" {
			trackingType = "NONE"
		}

		baseUOMID, err := strconv.ParseInt(in.BaseUOMID, 10, 64)
		if err != nil {
			return fmt.Errorf("base_uom_id 不合法: %w", err)
		}
		var categoryID sql.NullInt64
		if in.CategoryID != "" {
			cid, err := strconv.ParseInt(in.CategoryID, 10, 64)
			if err != nil {
				return fmt.Errorf("category_id 不合法: %w", err)
			}
			categoryID = sql.NullInt64{Int64: cid, Valid: true}
		}

		var id int64
		var sku string
		var createdAt, updatedAt time.Time
		var version int64

		if in.SKU == "" {
			// 留空则自动生成："P" + 6 位自增数字，同 mdm-customer 的 code
			// 生成方式（用 products_id_seq 的下一个值同时决定 id 与 sku）。
			if err := tx.QueryRowContext(ctx, `SELECT nextval('products_id_seq')`).Scan(&id); err != nil {
				return fmt.Errorf("生成产品编号: %w", err)
			}
			sku = fmt.Sprintf("P%06d", id)
			if err := tx.QueryRowContext(ctx, `
				INSERT INTO products (id, sku, name, category_id, base_uom_id, tracking_type, standard_cost)
				VALUES ($1, $2, $3, $4, $5, $6, $7)
				RETURNING created_at, updated_at, version, standard_cost`,
				id, sku, in.Name, categoryID, baseUOMID, trackingType, standardCost,
			).Scan(&createdAt, &updatedAt, &version, &standardCost); err != nil {
				return fmt.Errorf("insert products: %w", err)
			}
		} else {
			// 显式传入：唯一索引 products_sku_uniq 本身就会在冲突时报错。
			sku = in.SKU
			if err := tx.QueryRowContext(ctx, `
				INSERT INTO products (sku, name, category_id, base_uom_id, tracking_type, standard_cost)
				VALUES ($1, $2, $3, $4, $5, $6)
				RETURNING id, created_at, updated_at, version, standard_cost`,
				sku, in.Name, categoryID, baseUOMID, trackingType, standardCost,
			).Scan(&id, &createdAt, &updatedAt, &version, &standardCost); err != nil {
				return fmt.Errorf("insert products: %w", err)
			}
		}
		idStr := strconv.FormatInt(id, 10)

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO command_idempotency (idempotency_key, command, result_id) VALUES ($1, $2, $3)`,
			in.IdempotencyKey, "Create", idStr); err != nil {
			return fmt.Errorf("insert command_idempotency: %w", err)
		}

		payload, err := json.Marshal(map[string]any{
			"id": idStr, "sku": sku, "name": in.Name, "base_uom_id": in.BaseUOMID,
			"tracking_type": trackingType, "standard_cost": standardCost,
			"status": "ACTIVE", "version": version,
		})
		if err != nil {
			return err
		}
		if err := besdk.PublishOutbox(tx, r.schema, besdk.Event{
			Subject: "mdm.product.created.v1", AggregateID: idStr, Version: version, Payload: payload,
		}); err != nil {
			return err
		}

		if testHookAfterOutbox != nil {
			if err := testHookAfterOutbox(); err != nil {
				return err
			}
		}

		out = &Product{
			ID: idStr, SKU: sku, Name: in.Name, CategoryID: in.CategoryID,
			BaseUOMID: in.BaseUOMID, TrackingType: trackingType, StandardCost: standardCost,
			Status: "ACTIVE", Version: version, CreatedAt: createdAt, UpdatedAt: updatedAt,
		}
		return nil
	})
	return out, err
}

// lookupIdempotency 返回空字符串表示没查到（不是错误）。
func lookupIdempotency(ctx context.Context, tx *sql.Tx, key string) (string, error) {
	var resultID string
	err := tx.QueryRowContext(ctx,
		`SELECT result_id FROM command_idempotency WHERE idempotency_key = $1`, key).Scan(&resultID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("查 command_idempotency: %w", err)
	}
	return resultID, nil
}

func getByID(ctx context.Context, tx *sql.Tx, id string) (*Product, error) {
	row := tx.QueryRowContext(ctx,
		`SELECT id, sku, name, category_id, base_uom_id, tracking_type, standard_cost,
			created_at, updated_at, version, status
			FROM products WHERE id = $1`, id)
	p, err := scanProductRow(row)
	if err != nil {
		return nil, fmt.Errorf("查 products: %w", err)
	}
	return p, nil
}

// rowScanner 是 *sql.Row 与 *sql.Rows 的公共部分——scanProductRow 两边
// 都要用（Update/SetStatus 的 RETURNING 走 QueryRowContext，BatchGet
// 走 QueryContext）。
type rowScanner interface {
	Scan(dest ...any) error
}

func scanProductRow(row rowScanner) (*Product, error) {
	var rawID, rawBaseUOMID int64
	var categoryID sql.NullInt64
	var p Product
	if err := row.Scan(&rawID, &p.SKU, &p.Name, &categoryID, &rawBaseUOMID, &p.TrackingType,
		&p.StandardCost, &p.CreatedAt, &p.UpdatedAt, &p.Version, &p.Status); err != nil {
		return nil, err
	}
	p.ID = strconv.FormatInt(rawID, 10)
	p.BaseUOMID = strconv.FormatInt(rawBaseUOMID, 10)
	if categoryID.Valid {
		p.CategoryID = strconv.FormatInt(categoryID.Int64, 10)
	}
	return &p, nil
}

// ErrVersionConflict 是乐观锁冲突：请求带的 version 与库里当前值不一致。
var ErrVersionConflict = errors.New("version 冲突：与库里当前值不一致")

// ErrNotFound：按 id 查不到。
var ErrNotFound = errors.New("not found")

// UpdateInput 对应 UpdateRequest。
type UpdateInput struct {
	IdempotencyKey string
	ID             string
	Version        int64
	Name           string
	CategoryID     string
	StandardCost   string
}

func (r *Repo) Update(ctx context.Context, in UpdateInput) (*Product, error) {
	var out *Product
	err := besdk.WithTx(ctx, r.db, r.role, r.schema, func(tx *sql.Tx) error {
		existingID, err := lookupIdempotency(ctx, tx, in.IdempotencyKey)
		if err != nil {
			return err
		}
		if existingID != "" {
			p, err := getByID(ctx, tx, existingID)
			if err != nil {
				return err
			}
			out = p
			return nil
		}

		standardCost := in.StandardCost
		if standardCost == "" {
			standardCost = "0"
		}
		var categoryID sql.NullInt64
		if in.CategoryID != "" {
			cid, err := strconv.ParseInt(in.CategoryID, 10, 64)
			if err != nil {
				return fmt.Errorf("category_id 不合法: %w", err)
			}
			categoryID = sql.NullInt64{Int64: cid, Valid: true}
		}

		row := tx.QueryRowContext(ctx, `
			UPDATE products
			SET name = $1, category_id = $2, standard_cost = $3, version = version + 1, updated_at = now()
			WHERE id = $4 AND version = $5
			RETURNING id, sku, name, category_id, base_uom_id, tracking_type, standard_cost,
				created_at, updated_at, version, status`,
			in.Name, categoryID, standardCost, in.ID, in.Version)
		p, err := scanProductRow(row)
		if err == sql.ErrNoRows {
			return ErrVersionConflict
		}
		if err != nil {
			return fmt.Errorf("update products: %w", err)
		}

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO command_idempotency (idempotency_key, command, result_id) VALUES ($1, $2, $3)`,
			in.IdempotencyKey, "Update", p.ID); err != nil {
			return fmt.Errorf("insert command_idempotency: %w", err)
		}

		payload, err := json.Marshal(map[string]any{
			"id": p.ID, "sku": p.SKU, "name": p.Name, "base_uom_id": p.BaseUOMID,
			"tracking_type": p.TrackingType, "standard_cost": p.StandardCost,
			"status": p.Status, "version": p.Version,
		})
		if err != nil {
			return err
		}
		if err := besdk.PublishOutbox(tx, r.schema, besdk.Event{
			Subject: "mdm.product.updated.v1", AggregateID: p.ID, Version: p.Version, Payload: payload,
		}); err != nil {
			return err
		}

		out = p
		return nil
	})
	return out, err
}

// SetStatusInput 对应 SetStatusRequest。
//
// ⚠️ DISABLED 不是终态：历史订单/库存流水仍引用这条产品记录，不存在
// 可判定"无活跃业务"然后归档的时刻（设计计划 §2、§7，同 mdm-customer
// 的判据）。
type SetStatusInput struct {
	IdempotencyKey string
	ID             string
	Version        int64
	Status         string // "ACTIVE" | "DISABLED"
}

func (r *Repo) SetStatus(ctx context.Context, in SetStatusInput) (*Product, error) {
	var out *Product
	err := besdk.WithTx(ctx, r.db, r.role, r.schema, func(tx *sql.Tx) error {
		existingID, err := lookupIdempotency(ctx, tx, in.IdempotencyKey)
		if err != nil {
			return err
		}
		if existingID != "" {
			p, err := getByID(ctx, tx, existingID)
			if err != nil {
				return err
			}
			out = p
			return nil
		}

		row := tx.QueryRowContext(ctx, `
			UPDATE products
			SET status = $1, version = version + 1, updated_at = now()
			WHERE id = $2 AND version = $3
			RETURNING id, sku, name, category_id, base_uom_id, tracking_type, standard_cost,
				created_at, updated_at, version, status`,
			in.Status, in.ID, in.Version)
		p, err := scanProductRow(row)
		if err == sql.ErrNoRows {
			return ErrVersionConflict
		}
		if err != nil {
			return fmt.Errorf("update products: %w", err)
		}

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO command_idempotency (idempotency_key, command, result_id) VALUES ($1, $2, $3)`,
			in.IdempotencyKey, "SetStatus", p.ID); err != nil {
			return fmt.Errorf("insert command_idempotency: %w", err)
		}

		// 事件清单（contracts/events/product.events.json）只声明了
		// created/updated/disabled 三个 subject，没有 "enabled"——重新
		// 启用没有专门事件，复用 updated（同 mdm-customer 的判据：事件
		// 只增不删不改，决策 19）。
		subject := "mdm.product.updated.v1"
		if in.Status == "DISABLED" {
			subject = "mdm.product.disabled.v1"
		}
		payload, err := json.Marshal(map[string]any{
			"id": p.ID, "sku": p.SKU, "name": p.Name, "base_uom_id": p.BaseUOMID,
			"tracking_type": p.TrackingType, "standard_cost": p.StandardCost,
			"status": p.Status, "version": p.Version,
		})
		if err != nil {
			return err
		}
		if err := besdk.PublishOutbox(tx, r.schema, besdk.Event{
			Subject: subject, AggregateID: p.ID, Version: p.Version, Payload: payload,
		}); err != nil {
			return err
		}

		out = p
		return nil
	})
	return out, err
}

// BatchGet 是 gRPC BatchGet 的实现——BFF/erp-sales 防 N+1 的唯一合法
// 调用方式（§3.8）。冷热路由交给 besdk.BatchGetRouted；这里只管接对
// schema/table/scan 函数。
func (r *Repo) BatchGet(ctx context.Context, ids []string) (found []*Product, missing []string, err error) {
	var rows []*Product
	err = besdk.WithTx(ctx, r.db, r.role, r.schema, func(tx *sql.Tx) error {
		got, err := besdk.BatchGetRouted(ctx, tx, r.schema, "products", ids, scanProduct)
		if err != nil {
			return err
		}
		rows = got
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	foundIDs := make(map[string]bool, len(rows))
	for _, p := range rows {
		foundIDs[p.ID] = true
	}
	var missingIDs []string
	for _, id := range ids {
		if !foundIDs[id] {
			missingIDs = append(missingIDs, id)
		}
	}
	return rows, missingIDs, nil
}

func scanProduct(rows *sql.Rows) (string, *Product, error) {
	p, err := scanProductRow(rows)
	if err != nil {
		return "", nil, err
	}
	return p.ID, p, nil
}

// ListInput 对应 ListRequest。刻意没有 offset 字段——深分页在契约层面就
// 不可表达（决策 53）。
type ListInput struct {
	Cursor        string
	PageSize      int
	StatusFilter  string
	CreatedAfter  time.Time
	CreatedBefore time.Time
}

type ListResult struct {
	Products   []*Product
	NextCursor string
}

// buildListQuery 把 ListInput 接到 besdk.ListWindow——90 天默认窗口与
// 分页上限的算法本身是 SDK 的事，这里只负责别漏接。
func buildListQuery(in ListInput) besdk.Query {
	return besdk.ListWindow(besdk.Query{
		From:   in.CreatedAfter,
		To:     in.CreatedBefore,
		Cursor: in.Cursor,
		Limit:  in.PageSize,
	})
}

// cursor 编码 (created_at, id)：keyset 分页，不是 offset（决策 53）。
type cursorKey struct {
	CreatedAt time.Time
	ID        int64
}

func (r *Repo) List(ctx context.Context, in ListInput) (*ListResult, error) {
	q := buildListQuery(in)

	var ck *cursorKey
	if q.Cursor != "" {
		decoded, err := decodeCursor(q.Cursor)
		if err != nil {
			return nil, fmt.Errorf("非法 cursor：%w", err)
		}
		ck = &decoded
	}

	var out ListResult
	err := besdk.WithTx(ctx, r.db, r.role, r.schema, func(tx *sql.Tx) error {
		query := `SELECT id, sku, name, category_id, base_uom_id, tracking_type, standard_cost,
			created_at, updated_at, version, status
			FROM products
			WHERE created_at >= $1 AND created_at <= $2`
		args := []any{q.From, q.To}
		if in.StatusFilter != "" {
			args = append(args, in.StatusFilter)
			query += fmt.Sprintf(" AND status = $%d", len(args))
		}
		if ck != nil {
			args = append(args, ck.CreatedAt, ck.ID)
			query += fmt.Sprintf(" AND (created_at, id) < ($%d, $%d)", len(args)-1, len(args))
		}
		args = append(args, q.Limit+1) // 多取一条，用来判断是否还有下一页
		query += fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d", len(args))

		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("查 products: %w", err)
		}
		defer rows.Close()

		var products []*Product
		for rows.Next() {
			_, p, err := scanProduct(rows)
			if err != nil {
				return err
			}
			products = append(products, p)
		}
		if err := rows.Err(); err != nil {
			return err
		}

		if len(products) > q.Limit {
			last := products[q.Limit-1]
			var lastRawID int64
			lastRawID, err = strconv.ParseInt(last.ID, 10, 64)
			if err != nil {
				return err
			}
			out.NextCursor = encodeCursor(cursorKey{CreatedAt: last.CreatedAt, ID: lastRawID})
			products = products[:q.Limit]
		}
		out.Products = products
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ErrCrossCategoryConversion：换算的两个单位不属于同一个单位类别
// （千克换米没有意义）。跨类别直接报错，不做"智能推断"——这是照抄
// Odoo 的正确判断（设计计划 §2.1）。
var ErrCrossCategoryConversion = errors.New("换算的两个单位不属于同一个单位类别")

// ConvertQuantity 是纯函数、不落库的换算接口：(product_id, qty, from_uom,
// to_uom) → qty。用于"买箱卖个"这类跨单位场景。
//
// ⚠️ 舍入策略写死在这里，不留给调用方：按目标单位的 rounding 做 CEIL
// （向上取整）。出库场景下"需要 2.3 箱"必须取 3 箱（不够会缺货），向下
// 取整会让防超卖失效——入库场景由调用方自己决定要不要反向处理
// （设计计划 §2.1）。
//
// ⚠️ 算术在 SQL 里做，不在 Go 里做：换算因子与舍入都是 NUMERIC，Go 没有
// 内建的精确 decimal 类型，浮点在这里会引入误差——而这正是整个组件
// "不用浮点存换算因子"这条决定要避免的问题（设计计划 §2.1）。
func (r *Repo) ConvertQuantity(ctx context.Context, productID, qty, fromUOMID, toUOMID string) (string, error) {
	var result string
	err := besdk.WithTx(ctx, r.db, r.role, r.schema, func(tx *sql.Tx) error {
		var exists bool
		if err := tx.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM products WHERE id = $1)`, productID,
		).Scan(&exists); err != nil {
			return fmt.Errorf("查 products: %w", err)
		}
		if !exists {
			return ErrNotFound
		}

		var fromCategory, toCategory string
		if err := tx.QueryRowContext(ctx, `SELECT category FROM uoms WHERE id = $1`, fromUOMID).Scan(&fromCategory); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("%w: from_uom_id 不存在", ErrNotFound)
			}
			return fmt.Errorf("查 from_uom: %w", err)
		}
		if err := tx.QueryRowContext(ctx, `SELECT category FROM uoms WHERE id = $1`, toUOMID).Scan(&toCategory); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("%w: to_uom_id 不存在", ErrNotFound)
			}
			return fmt.Errorf("查 to_uom: %w", err)
		}
		if fromCategory != toCategory {
			return ErrCrossCategoryConversion
		}

		// factor：基准单位是 1；非基准单位查 uom_conversions。用
		// COALESCE 把"基准单位没有 uom_conversions 行"这个正常情况接住，
		// 不是靠 LEFT JOIN 之外再判一次 NULL。
		//
		// ⚠️ trim_scale()（PG 13+）去掉末尾的零——CEIL(...) * rounding 这
		// 一步会把结果的 scale 撑到 NUMERIC(18,6) 的全宽度（"3" 会变成
		// "3.000000"），trim_scale 只影响显示不影响数值，是 Go 层面测出来
		// 的：单元测试断言 "3" 时才发现原始查询吐出的是 "3.000000"。
		row := tx.QueryRowContext(ctx, `
			SELECT trim_scale(CEIL(
				($1::numeric * COALESCE(fc.factor, CASE WHEN fu.is_base THEN 1 END)
				             / COALESCE(tc.factor, CASE WHEN tu.is_base THEN 1 END))
				/ tu.rounding
			) * tu.rounding)
			FROM uoms fu
			JOIN uoms tu ON tu.id = $3
			LEFT JOIN uom_conversions fc ON fc.uom_id = fu.id
			LEFT JOIN uom_conversions tc ON tc.uom_id = tu.id
			WHERE fu.id = $2`,
			qty, fromUOMID, toUOMID,
		)
		if err := row.Scan(&result); err != nil {
			return fmt.Errorf("换算: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return result, nil
}
