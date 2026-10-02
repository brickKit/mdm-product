package repo

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	besdk "github.com/brickKit/be-sdk-go"
)

// BatchGet 按 id 批量取产品，缺失的 id 进 missing、不报错。这是别的组件
// 防 N+1 的读法（建单时一次取回全部行的产品），只走 gRPC。热表 / 归档表的
// 路由交给 besdk.BatchGetRouted：本组件不归档，归档 schema 里没有
// products 表，SDK 查不到表时只用热表的结果。不是数字的 id 不进查询，直接
// 算缺失。
func (r *Repo) BatchGet(ctx context.Context, ids []string) (found []*Product, missing []string, err error) {
	query := make([]string, 0, len(ids))
	for _, id := range ids {
		if isNumericID(id) {
			query = append(query, id)
		}
	}
	var rows []*Product
	err = besdk.WithTx(ctx, r.db, r.role, r.schema, func(tx *sql.Tx) error {
		got, err := besdk.BatchGetRouted(ctx, tx, r.schema, "products", query, scanProduct)
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

// ListInput 对应 ListRequest。没有 offset：列表只能按游标翻页。
type ListInput struct {
	Q             string // 关键字：匹配 sku 或 name，见 List
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

// buildListQuery 把 ListInput 交给 besdk.ListWindow：没给时间范围时补上
// 默认窗口（最近 90 天），页大小取默认值或截到上限。算法是 SDK 的，这里
// 只保证接上了。
func buildListQuery(in ListInput) besdk.Query {
	return besdk.ListWindow(besdk.Query{
		From:   in.CreatedAfter,
		To:     in.CreatedBefore,
		Cursor: in.Cursor,
		Limit:  in.PageSize,
	})
}

// cursorKey 是游标里编码的排序键 (match_rank, created_at, id)：keyset 分页，
// 不是 offset。不带 q 时 Rank 恒为 0。
type cursorKey struct {
	Rank      int
	CreatedAt time.Time
	ID        int64
}

// List 分页列出产品：先按 match_rank（带 q 时前缀匹配为 0、只是包含为 1；
// 不带 q 时恒为 0），再按 created_at、id 倒序。多取一条判断有没有下一页；
// 最后一页的 NextCursor 是空字符串。
//
// q 去掉首尾空白后为空就等于不过滤。匹配用 strpos / starts_with 而不是
// LIKE：用户输入里的 % 与 _ 按字面匹配，不必转义。lower() 让大小写不敏感。
// 默认时间窗口照常生效（与不带 q 的列表一致），要搜更早建档的产品就显式给
// CreatedAfter。
func (r *Repo) List(ctx context.Context, in ListInput) (*ListResult, error) {
	q := buildListQuery(in)

	var ck *cursorKey
	if q.Cursor != "" {
		decoded, err := decodeCursor(q.Cursor)
		if err != nil {
			return nil, fmt.Errorf("%w：%v", ErrInvalidCursor, err)
		}
		ck = &decoded
	}

	var out ListResult
	err := besdk.WithTx(ctx, r.db, r.role, r.schema, func(tx *sql.Tx) error {
		query, args := listSQL(in, q, ck)
		rows, err := queryRanked(ctx, tx, query, args...)
		if err != nil {
			return err
		}
		if len(rows) > q.Limit {
			last := rows[q.Limit-1]
			lastRawID, err := strconv.ParseInt(last.p.ID, 10, 64)
			if err != nil {
				return err
			}
			out.NextCursor = encodeCursor(cursorKey{Rank: last.rank, CreatedAt: last.p.CreatedAt, ID: lastRawID})
			rows = rows[:q.Limit]
		}
		out.Products = make([]*Product, 0, len(rows))
		for _, row := range rows {
			out.Products = append(out.Products, row.p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// listSQL 拼 List 的查询。内层算 match_rank 并做全部过滤（时间窗口、状态、
// 关键字），外层按游标切页、排序、多取一条。
func listSQL(in ListInput, q besdk.Query, ck *cursorKey) (string, []any) {
	args := []any{q.From, q.To}
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	rank := "0"
	where := "created_at >= $1 AND created_at <= $2"
	if in.StatusFilter != "" {
		where += " AND status = " + arg(in.StatusFilter)
	}
	if kw := strings.TrimSpace(in.Q); kw != "" {
		k := "lower(" + arg(kw) + ")"
		where += fmt.Sprintf(" AND (strpos(lower(sku), %s) > 0 OR strpos(lower(name), %s) > 0)", k, k)
		rank = fmt.Sprintf("CASE WHEN starts_with(lower(sku), %s) OR starts_with(lower(name), %s) THEN 0 ELSE 1 END", k, k)
	}

	query := `SELECT ` + productColumns + `, match_rank FROM (
			SELECT ` + productColumns + `, ` + rank + ` AS match_rank
			FROM products
			WHERE ` + where + `
		) t`
	if ck != nil {
		r, c, i := arg(ck.Rank), arg(ck.CreatedAt), arg(ck.ID)
		query += fmt.Sprintf(" WHERE (match_rank > %s OR (match_rank = %s AND (created_at, id) < (%s, %s)))", r, r, c, i)
	}
	query += " ORDER BY match_rank, created_at DESC, id DESC LIMIT " + arg(q.Limit+1)
	return query, args
}

type rankedProduct struct {
	p    *Product
	rank int
}

// queryRanked 扫 listSQL 的结果：productColumns 之后多一列 match_rank。
func queryRanked(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]rankedProduct, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查 products: %w", err)
	}
	defer rows.Close()

	var out []rankedProduct
	for rows.Next() {
		var rank int
		p, err := scanProductRow(rankScanner{rows, &rank})
		if err != nil {
			return nil, err
		}
		out = append(out, rankedProduct{p: p, rank: rank})
	}
	return out, rows.Err()
}

// rankScanner 让 scanProductRow 能扫"产品列 + 末尾一列 match_rank"的行：
// 把 rank 的目标追加在产品列的目标之后。
type rankScanner struct {
	rows *sql.Rows
	rank *int
}

func (s rankScanner) Scan(dest ...any) error {
	return s.rows.Scan(append(dest, s.rank)...)
}
