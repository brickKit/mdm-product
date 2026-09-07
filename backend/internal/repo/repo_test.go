package repo

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	besdk "github.com/brickKit/be-sdk-go"
	_ "github.com/jackc/pgx/v5/stdlib" // §12.4：不用 lib/pq，驱动名注册为 "pgx"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("未设置 TEST_PG_DSN")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// uomID 按 code 查种子数据（003_seed_uoms.up.sql）的 UoM id——不硬编码
// 数字，避免迁移顺序变化时测试跟着假设错位。
func uomID(t *testing.T, db *sql.DB, code string) string {
	t.Helper()
	var id int64
	err := besdk.WithTx(context.Background(), db, "mdm_product_rw", "mdm_product",
		func(tx *sql.Tx) error {
			return tx.QueryRow(`SELECT id FROM uoms WHERE code = $1`, code).Scan(&id)
		})
	if err != nil {
		t.Fatalf("查种子 UoM %q 失败（先跑 003_seed_uoms.up.sql）：%v", code, err)
	}
	return strconv.FormatInt(id, 10)
}

func TestCreate_sku留空时自动生成(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := New(db, "mdm_product_rw", "mdm_product")
	ea := uomID(t, db, "EA")

	p, err := r.Create(ctx, CreateInput{IdempotencyKey: "test-autosku-001", Name: "自动编号产品", BaseUOMID: ea})
	if err != nil {
		t.Fatal(err)
	}
	if p.SKU == "" {
		t.Fatal("sku 留空时应该自动生成，实际还是空字符串")
	}
	if !strings.HasPrefix(p.SKU, "P") || len(p.SKU) != 7 {
		t.Fatalf(`期望形如 "P" + 6 位数字，实际得到 %q`, p.SKU)
	}
}

func TestCreate_standardCost落库后规整成两位小数(t *testing.T) {
	// 同 mdm-customer 的 credit_limit 教训（Task 16 L3）：Create 必须用
	// RETURNING 读回 NUMERIC 落库后规整过的值，不能直接回填调用方传入的
	// 原始字符串——"0" 传进去，落库后是 "0.00"，两者不一致会让随后
	// Get/List 读到的值和 Create 的响应对不上。
	db := testDB(t)
	ctx := context.Background()
	r := New(db, "mdm_product_rw", "mdm_product")
	ea := uomID(t, db, "EA")

	p, err := r.Create(ctx, CreateInput{
		IdempotencyKey: "test-cost-zero-001", Name: "零成本产品", BaseUOMID: ea, StandardCost: "0"})
	if err != nil {
		t.Fatal(err)
	}
	if p.StandardCost != "0.00" {
		t.Fatalf("期望落库后是 0.00，实际 %q", p.StandardCost)
	}
}

func TestCreate_幂等(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := New(db, "mdm_product_rw", "mdm_product")
	ea := uomID(t, db, "EA")

	in := CreateInput{IdempotencyKey: "test-idem-001", SKU: "P-001", Name: "Widget", BaseUOMID: ea}
	a, err := r.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.Create(ctx, in)
	if err != nil {
		t.Fatalf("幂等重试报错了：%v", err)
	}
	if a.ID != b.ID {
		t.Fatalf("幂等失效：第一次 %s，第二次 %s", a.ID, b.ID)
	}
}

// TestCreate_事件与业务数据同事务 验证 §3.10 的 Outbox Pattern：业务写入
// 与 PublishOutbox 必须在同一事务里，任一方失败两边都不许留下痕迹。
func TestCreate_事件与业务数据同事务(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := New(db, "mdm_product_rw", "mdm_product")
	ea := uomID(t, db, "EA")

	testHookAfterOutbox = func() error { return errors.New("注入的失败，验证回滚") }
	t.Cleanup(func() { testHookAfterOutbox = nil })

	_, err := r.Create(ctx, CreateInput{
		IdempotencyKey: "test-rollback-001", SKU: "P-ROLLBACK", Name: "Rollback", BaseUOMID: ea})
	if err == nil {
		t.Fatal("期望注入的失败被返回")
	}

	var n int
	if err := besdk.WithTx(ctx, db, "mdm_product_rw", "mdm_product",
		func(tx *sql.Tx) error {
			return tx.QueryRow(
				`SELECT count(*) FROM event_outbox WHERE aggregate_id IN
					(SELECT id::text FROM products WHERE sku = $1)`,
				"P-ROLLBACK").Scan(&n)
		}); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("业务回滚了但 outbox 留了 %d 条——说明事件不在同一事务里（§3.10）", n)
	}

	var m int
	if err := besdk.WithTx(ctx, db, "mdm_product_rw", "mdm_product",
		func(tx *sql.Tx) error {
			return tx.QueryRow(`SELECT count(*) FROM products WHERE sku = $1`, "P-ROLLBACK").Scan(&m)
		}); err != nil {
		t.Fatal(err)
	}
	if m != 0 {
		t.Fatalf("期望业务行也回滚，实际还留着 %d 条", m)
	}
}

func TestBatchGet_缺失的id不报错(t *testing.T) {
	db := testDB(t)
	r := New(db, "mdm_product_rw", "mdm_product")
	got, missing, err := r.BatchGet(context.Background(), []string{"999999999"})
	if err != nil {
		t.Fatalf("BatchGet 对缺失 id 不该报错：%v", err)
	}
	if len(got) != 0 || len(missing) != 1 {
		t.Fatalf("期望 0 命中 1 缺失，得到 %d/%d", len(got), len(missing))
	}
}

func TestList_未传时间范围时自动注入90天窗口(t *testing.T) {
	q := buildListQuery(ListInput{PageSize: 20})
	if q.From.IsZero() || q.To.IsZero() {
		t.Fatal("框架层必须自动注入默认时间窗口，否则用户无条件查询会拖垮数据库（§11.4.1）")
	}
	days := q.To.Sub(q.From).Hours() / 24
	if days < 89 || days > 91 {
		t.Fatalf("默认窗口应约为 90 天，实际 %.1f 天", days)
	}
}

// TestList_最后一页返回空nextCursor而不是报错 是 L3：SQL 里"多取一条判断
// 有没有下一页"这段逻辑只有真的跑到最后一页才会走到 else 分支。
func TestList_最后一页返回空nextCursor而不是报错(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := New(db, "mdm_product_rw", "mdm_product")
	ea := uomID(t, db, "EA")

	p, err := r.Create(ctx, CreateInput{IdempotencyKey: "test-lastpage-001", Name: "分页边界测试", BaseUOMID: ea})
	if err != nil {
		t.Fatal(err)
	}

	res, err := r.List(ctx, ListInput{
		PageSize:      500,
		CreatedAfter:  p.CreatedAt.Add(-time.Second),
		CreatedBefore: p.CreatedAt.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("已经是最后一页不该报错：%v", err)
	}
	if res.NextCursor != "" {
		t.Fatalf("已经是最后一页，期望 next_cursor 为空，实际 %q", res.NextCursor)
	}
}

// ── ConvertQuantity：这份设计计划里唯一没有参考实现可抄的部分（§8），
// 正确性只能靠这几条测试保证。

func TestConvertQuantity_同类别正确换算并向上取整(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := New(db, "mdm_product_rw", "mdm_product")
	ea := uomID(t, db, "EA")
	box := uomID(t, db, "BOX")

	p, err := r.Create(ctx, CreateInput{IdempotencyKey: "test-conv-001", Name: "换算测试品", BaseUOMID: ea})
	if err != nil {
		t.Fatal(err)
	}

	// 1 箱 = 12 个（种子数据）。25 个 = 25/12 = 2.08... 箱，向上取整必须是 3。
	got, err := r.ConvertQuantity(ctx, p.ID, "25", ea, box)
	if err != nil {
		t.Fatalf("同类别换算不该报错：%v", err)
	}
	if got != "3" {
		t.Fatalf("25 个换算成箱，期望向上取整为 3，实际 %q", got)
	}
}

func TestConvertQuantity_整除时不多取整(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := New(db, "mdm_product_rw", "mdm_product")
	ea := uomID(t, db, "EA")
	box := uomID(t, db, "BOX")

	p, err := r.Create(ctx, CreateInput{IdempotencyKey: "test-conv-002", Name: "换算测试品2", BaseUOMID: ea})
	if err != nil {
		t.Fatal(err)
	}

	// 24 个恰好是 2 箱，向上取整不该多算成 3
	got, err := r.ConvertQuantity(ctx, p.ID, "24", ea, box)
	if err != nil {
		t.Fatal(err)
	}
	if got != "2" {
		t.Fatalf("24 个恰好整除 12，期望 2，实际 %q（说明 CEIL 把整除的情形也多加了 1，是错的）", got)
	}
}

func TestConvertQuantity_跨类别换算报错(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := New(db, "mdm_product_rw", "mdm_product")
	ea := uomID(t, db, "EA")   // count
	kg := uomID(t, db, "KG")   // weight

	p, err := r.Create(ctx, CreateInput{IdempotencyKey: "test-conv-003", Name: "跨类别测试品", BaseUOMID: ea})
	if err != nil {
		t.Fatal(err)
	}

	_, err = r.ConvertQuantity(ctx, p.ID, "5", ea, kg)
	if !errors.Is(err, ErrCrossCategoryConversion) {
		t.Fatalf("千克换个应该报跨类别错误，实际：%v", err)
	}
}

func TestConvertQuantity_产品不存在时报错(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := New(db, "mdm_product_rw", "mdm_product")
	ea := uomID(t, db, "EA")
	box := uomID(t, db, "BOX")

	_, err := r.ConvertQuantity(ctx, "999999999", "1", ea, box)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("产品不存在应该报 ErrNotFound，实际：%v", err)
	}
}

func TestConvertQuantity_同一基准单位换算factor为1(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := New(db, "mdm_product_rw", "mdm_product")
	kg := uomID(t, db, "KG")
	g := uomID(t, db, "G")

	p, err := r.Create(ctx, CreateInput{IdempotencyKey: "test-conv-004", Name: "重量测试品", BaseUOMID: kg})
	if err != nil {
		t.Fatal(err)
	}

	// 2.5 千克 = 2500 克，rounding=1 时应该恰好是 2500，不多不少
	got, err := r.ConvertQuantity(ctx, p.ID, "2.5", kg, g)
	if err != nil {
		t.Fatal(err)
	}
	if got != "2500" {
		t.Fatalf("2.5 千克换算成克，期望 2500，实际 %q", got)
	}
}
