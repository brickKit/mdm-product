package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"

	besdk "github.com/brickKit/be-sdk-go"
)

// 三个写命令共用同一个形状，全部在一个 besdk.WithTx 事务里：
//  1. 查 command_idempotency：同一个 idempotency_key 已经执行过，就原样返回
//     上次的结果，不再写任何东西（网络重试、前端重复提交都落在这里）；
//  2. 写业务行；
//  3. 记幂等键——idempotency_key 是主键，两个并发的同键请求里后提交的那个
//     在这里撞主键、整个事务回滚，所以最终只落一条（它自己会收到错误）；
//  4. 写 Outbox（与业务行同事务：任一步失败，事件与业务行一起回滚）。

// testHookAfterOutbox 只给本包测试用：Create 在写完 Outbox、提交之前调它，
// 测试借此验证"业务数据与事件同事务"，生产代码里永远是 nil。
var testHookAfterOutbox func() error

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

// replayProduct 是第 1 步：幂等键命中时返回上次的产品（ok=true）。
func replayProduct(ctx context.Context, tx *sql.Tx, key string) (*Product, bool, error) {
	existingID, err := lookupIdempotency(ctx, tx, key)
	if err != nil || existingID == "" {
		return nil, false, err
	}
	p, err := getByID(ctx, tx, existingID)
	if err != nil {
		return nil, false, err
	}
	return p, true, nil
}

// recordIdempotency 是第 3 步。
func recordIdempotency(ctx context.Context, tx *sql.Tx, key, command, resultID string) error {
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO command_idempotency (idempotency_key, command, result_id) VALUES ($1, $2, $3)`,
		key, command, resultID); err != nil {
		return fmt.Errorf("insert command_idempotency: %w", err)
	}
	return nil
}

// publish 是第 4 步。三个事件的 payload 是同一组字段（全量），下游按
// version 单调递增覆盖自己的摘要副本。tracking_type 必须在里面：
// erp/inventory 靠它判断这个产品收货时要不要批次号 / 序列号。
func (r *Repo) publish(tx *sql.Tx, subject string, p *Product) error {
	raw, err := json.Marshal(map[string]any{
		"id": p.ID, "sku": p.SKU, "name": p.Name, "base_uom_id": p.BaseUOMID,
		"tracking_type": p.TrackingType, "standard_cost": p.StandardCost,
		"status": p.Status, "version": p.Version,
	})
	if err != nil {
		return err
	}
	return besdk.PublishOutbox(tx, r.schema, besdk.Event{
		Subject: subject, AggregateID: p.ID, Version: p.Version, Payload: raw,
	})
}

// parseOptionalID 把可选的数字 id（category_id）转成可空的 BIGINT 参数。
func parseOptionalID(field, s string) (sql.NullInt64, error) {
	if s == "" {
		return sql.NullInt64{}, nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return sql.NullInt64{}, fmt.Errorf("%w：%s 不是数字：%q", ErrInvalidReference, field, s)
	}
	return sql.NullInt64{Int64: v, Valid: true}, nil
}

// CreateInput 对应 CreateRequest（contracts/mdm/product/v1/product.proto）。
type CreateInput struct {
	IdempotencyKey string
	SKU            string // 留空则自动生成 "P" + 6 位数字
	Name           string
	CategoryID     string
	BaseUOMID      string
	TrackingType   string // 留空按 NONE
	StandardCost   string // 留空按 0
}

// Create 新建产品，发 mdm.product.created.v1。
func (r *Repo) Create(ctx context.Context, in CreateInput) (*Product, error) {
	var out *Product
	err := besdk.WithTx(ctx, r.db, r.role, r.schema, func(tx *sql.Tx) error {
		if p, ok, err := replayProduct(ctx, tx, in.IdempotencyKey); err != nil || ok {
			out = p
			return err
		}

		p, err := insertProduct(ctx, tx, in)
		if err != nil {
			return err
		}
		if err := recordIdempotency(ctx, tx, in.IdempotencyKey, "Create", p.ID); err != nil {
			return err
		}
		if err := r.publish(tx, "mdm.product.created.v1", p); err != nil {
			return err
		}
		if testHookAfterOutbox != nil {
			if err := testHookAfterOutbox(); err != nil {
				return err
			}
		}
		out = p
		return nil
	})
	return out, err
}

// insertProduct 写 products 一行并返回落库后的值。
//
// 返回值一律从 RETURNING 读回，不回填调用方传入的原文：NUMERIC(18,2) 会把
// "0" 规整成 "0.00"，回填原文会让 Create 的返回值与随后 Get 同一条记录
// 读到的格式不一致。
func insertProduct(ctx context.Context, tx *sql.Tx, in CreateInput) (*Product, error) {
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
		return nil, fmt.Errorf("%w：base_uom_id 不是数字：%q", ErrInvalidReference, in.BaseUOMID)
	}
	categoryID, err := parseOptionalID("category_id", in.CategoryID)
	if err != nil {
		return nil, err
	}

	var row *sql.Row
	if in.SKU == "" {
		// 自动编号：一次 nextval 同时决定 id 与 sku，插入时显式给 id。
		var id int64
		if err := tx.QueryRowContext(ctx, `SELECT nextval('products_id_seq')`).Scan(&id); err != nil {
			return nil, fmt.Errorf("生成产品编号: %w", err)
		}
		row = tx.QueryRowContext(ctx, `
			INSERT INTO products (id, sku, name, category_id, base_uom_id, tracking_type, standard_cost)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING `+productColumns,
			id, fmt.Sprintf("P%06d", id), in.Name, categoryID, baseUOMID, trackingType, standardCost)
	} else {
		// 显式传入的 sku 由唯一索引 products_sku_uniq 去重。
		row = tx.QueryRowContext(ctx, `
			INSERT INTO products (sku, name, category_id, base_uom_id, tracking_type, standard_cost)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING `+productColumns,
			in.SKU, in.Name, categoryID, baseUOMID, trackingType, standardCost)
	}
	p, err := scanProductRow(row)
	if err != nil {
		return nil, fmt.Errorf("insert products: %w", classifyWriteErr(err))
	}
	return p, nil
}

// UpdateInput 对应 UpdateRequest。name / category_id / standard_cost 三个字段
// 整体替换：category_id 留空表示改成未分类，standard_cost 留空表示 0。
type UpdateInput struct {
	IdempotencyKey string
	ID             string
	Version        int64
	Name           string
	CategoryID     string
	StandardCost   string
}

// Update 改产品的名称、分类、标准成本（乐观锁），发 mdm.product.updated.v1。
func (r *Repo) Update(ctx context.Context, in UpdateInput) (*Product, error) {
	standardCost := in.StandardCost
	if standardCost == "" {
		standardCost = "0"
	}
	categoryID, err := parseOptionalID("category_id", in.CategoryID)
	if err != nil {
		return nil, err
	}
	return r.updateWithVersion(ctx, in.IdempotencyKey, "Update", in.ID, in.Version,
		"name = $3, category_id = $4, standard_cost = $5",
		[]any{in.Name, categoryID, standardCost},
		func(*Product) string { return "mdm.product.updated.v1" })
}

// SetStatusInput 对应 SetStatusRequest。
//
// DISABLED 不是终态：停用的产品仍被历史订单、库存流水引用，可以随时重新
// 启用，也永远不删、不归档。
type SetStatusInput struct {
	IdempotencyKey string
	ID             string
	Version        int64
	Status         string // ACTIVE | DISABLED
}

// SetStatus 启用 / 停用产品（乐观锁）。停用发 mdm.product.disabled.v1；
// 事件清单里没有 enabled，重新启用发 mdm.product.updated.v1（事件只增不改，
// 不为它另造一个 subject）。
func (r *Repo) SetStatus(ctx context.Context, in SetStatusInput) (*Product, error) {
	return r.updateWithVersion(ctx, in.IdempotencyKey, "SetStatus", in.ID, in.Version,
		"status = $3", []any{in.Status},
		func(p *Product) string {
			if p.Status == "DISABLED" {
				return "mdm.product.disabled.v1"
			}
			return "mdm.product.updated.v1"
		})
}

// updateWithVersion 是 Update 与 SetStatus 共用的事务：幂等重放、带
// version 条件的 UPDATE（$1 = id、$2 = version，set 里的占位符从 $3 起）、
// 记幂等键、按更新后的行选事件 subject 写 Outbox。UPDATE 一行都没命中时，
// 产品不存在是 ErrNotFound，存在就是乐观锁冲突。
func (r *Repo) updateWithVersion(ctx context.Context, key, command, id string, version int64,
	set string, setArgs []any, subject func(*Product) string) (*Product, error) {
	if !isNumericID(id) {
		return nil, fmt.Errorf("%w：产品 %q", ErrNotFound, id)
	}
	var out *Product
	err := besdk.WithTx(ctx, r.db, r.role, r.schema, func(tx *sql.Tx) error {
		if p, ok, err := replayProduct(ctx, tx, key); err != nil || ok {
			out = p
			return err
		}

		args := append([]any{id, version}, setArgs...)
		row := tx.QueryRowContext(ctx, `
			UPDATE products
			SET `+set+`, version = version + 1, updated_at = now()
			WHERE id = $1 AND version = $2
			RETURNING `+productColumns, args...)
		p, err := scanProductRow(row)
		if err == sql.ErrNoRows {
			return missingOrConflict(ctx, tx, id)
		}
		if err != nil {
			return fmt.Errorf("update products: %w", classifyWriteErr(err))
		}

		if err := recordIdempotency(ctx, tx, key, command, p.ID); err != nil {
			return err
		}
		if err := r.publish(tx, subject(p), p); err != nil {
			return err
		}
		out = p
		return nil
	})
	return out, err
}

// missingOrConflict 区分带 version 条件的 UPDATE 没命中的两种原因。
func missingOrConflict(ctx context.Context, tx *sql.Tx, id string) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM products WHERE id = $1)`, id).Scan(&exists); err != nil {
		return fmt.Errorf("查 products: %w", err)
	}
	if !exists {
		return fmt.Errorf("%w：产品 %s", ErrNotFound, id)
	}
	return ErrVersionConflict
}
