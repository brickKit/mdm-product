package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	besdk "github.com/brickKit/be-sdk-go"
)

// ErrCrossCategoryConversion：换算的两个单位不属于同一个单位类别（千克换米
// 没有意义）。跨类别直接报错，不猜调用方的意思。
var ErrCrossCategoryConversion = errors.New("换算的两个单位不属于同一个单位类别")

// ConvertQuantity 是不落库的换算：(product_id, qty, from_uom, to_uom) → qty，
// 用于"买箱卖个"这类跨单位场景。
//
// 舍入写死在这里，不留给调用方：按目标单位的 rounding 向上取整（CEIL）。
// 出库时"需要 2.3 箱"必须取 3 箱，向下取整会少发货、让防超卖失效；入库要
// 不要反向处理由调用方自己决定。
//
// 算术在 SQL 里做，不在 Go 里做：换算因子与 rounding 都是 NUMERIC，Go 没有
// 内建的精确十进制类型，浮点会把误差乘进订单数量与金额。
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

		fromCategory, err := uomCategory(ctx, tx, "from_uom_id", fromUOMID)
		if err != nil {
			return err
		}
		toCategory, err := uomCategory(ctx, tx, "to_uom_id", toUOMID)
		if err != nil {
			return err
		}
		if fromCategory != toCategory {
			return ErrCrossCategoryConversion
		}

		// factor：基准单位是 1；非基准单位查 uom_conversions（1 个本单位 =
		// factor 个基准单位）。COALESCE 接住"基准单位没有 uom_conversions
		// 行"这个正常情况。
		//
		// trim_scale() 去掉末尾的零：CEIL(...) * rounding 会把 scale 撑到
		// NUMERIC(18,6) 的全宽度（"3" 变成 "3.000000"），trim_scale 只影响
		// 显示，不影响数值。
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

// uomCategory 取单位的类别；单位不存在报 ErrNotFound（field 写进错误信息）。
func uomCategory(ctx context.Context, tx *sql.Tx, field, id string) (string, error) {
	var category string
	err := tx.QueryRowContext(ctx, `SELECT category FROM uoms WHERE id = $1`, id).Scan(&category)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("%w: %s 不存在", ErrNotFound, field)
	}
	if err != nil {
		return "", fmt.Errorf("查 %s: %w", field, err)
	}
	return category, nil
}
