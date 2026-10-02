// Package repo 是 mdm-product 的数据访问层：products / product_categories /
// uoms / uom_conversions 四张表，加上同事务写入的 Outbox 与幂等键表。
//
// 文件按职责拆开：write.go 是三个写命令（每个都在一个事务里做"查幂等键 →
// 写业务行 → 记幂等键 → 写 Outbox"），read.go 是按 id 批量取与列表分页，
// convert.go 是单位换算，cursor.go 是列表游标的编解码。本文件只放几边共用
// 的类型、哨兵错误与扫描函数。
//
// 每次访问数据库都经 besdk.WithTx：它在事务里 SET LOCAL ROLE 与
// SET LOCAL search_path，连接还回池里时不留下本组件的设置。
package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// Repo 持有共享连接池与本组件的 role / schema。
type Repo struct {
	db     *sql.DB
	role   string
	schema string
}

func New(db *sql.DB, role, schema string) *Repo {
	return &Repo{db: db, role: role, schema: schema}
}

// Product 是 products 表的一行。StandardCost 是十进制字符串：浮点数跨语言
// 序列化会丢精度。
type Product struct {
	ID           string
	SKU          string
	Name         string
	CategoryID   string // 空字符串表示未分类
	BaseUOMID    string
	TrackingType string // NONE / BATCH / SERIAL
	StandardCost string
	Status       string // ACTIVE / DISABLED
	Version      int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ErrVersionConflict 是乐观锁冲突：请求带的 version 与库里当前值不一致。
// service.ToStatus 把它映射成 Aborted / 409。
var ErrVersionConflict = errors.New("version 冲突：与库里当前值不一致")

// ErrNotFound：按 id 查不到。映射成 NotFound / 404。
var ErrNotFound = errors.New("not found")

// ErrInvalidCursor：列表游标解不开（被截断、篡改，或不是本组件发的）。
// 这是调用方传错了参数，映射成 InvalidArgument / 400，不是服务端故障。
var ErrInvalidCursor = errors.New("非法 cursor")

// productColumns 是 scanProductRow 期望的列顺序，所有 SELECT / RETURNING
// 都用它，免得列序与 Scan 的参数序对不上。
const productColumns = `id, sku, name, category_id, base_uom_id, tracking_type, standard_cost,
	created_at, updated_at, version, status`

// rowScanner 是 *sql.Row 与 *sql.Rows 的公共部分。
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

// scanProduct 是 besdk.BatchGetRouted 要的扫描函数形状。
func scanProduct(rows *sql.Rows) (string, *Product, error) {
	p, err := scanProductRow(rows)
	if err != nil {
		return "", nil, err
	}
	return p.ID, p, nil
}

func getByID(ctx context.Context, tx *sql.Tx, id string) (*Product, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+productColumns+` FROM products WHERE id = $1`, id)
	p, err := scanProductRow(row)
	if err != nil {
		return nil, fmt.Errorf("查 products: %w", err)
	}
	return p, nil
}
