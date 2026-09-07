package service

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"testing"

	besdk "github.com/brickKit/be-sdk-go"
	"github.com/brickKit/mdm-product/backend/internal/repo"
	_ "github.com/jackc/pgx/v5/stdlib"
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

func newTestService(t *testing.T) (*Service, *repo.Repo, *sql.DB) {
	t.Helper()
	db := testDB(t)
	r := repo.New(db, "mdm_product_rw", "mdm_product")
	return New(r, slog.Default()), r, db
}

func uomID(t *testing.T, db *sql.DB, code string) string {
	t.Helper()
	var id int64
	err := besdk.WithTx(context.Background(), db, "mdm_product_rw", "mdm_product",
		func(tx *sql.Tx) error {
			return tx.QueryRow(`SELECT id FROM uoms WHERE code = $1`, code).Scan(&id)
		})
	if err != nil {
		t.Fatalf("查种子 UoM %q 失败：%v", code, err)
	}
	return strconv.FormatInt(id, 10)
}

func TestCreate_name为空时拒绝且不落库(t *testing.T) {
	svc, _, db := newTestService(t)
	ctx := context.Background()
	ea := uomID(t, db, "EA")

	_, err := svc.Create(ctx, repo.CreateInput{
		IdempotencyKey: "svc-l3-name-empty", SKU: "P-L3-NAME-EMPTY", Name: "", BaseUOMID: ea})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("name 为空应该拒绝并返回 ErrInvalidArgument，实际：%v", err)
	}

	var n int
	if err := besdk.WithTx(ctx, db, "mdm_product_rw", "mdm_product",
		func(tx *sql.Tx) error {
			return tx.QueryRow(`SELECT count(*) FROM products WHERE sku = $1`, "P-L3-NAME-EMPTY").Scan(&n)
		}); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("校验应该发生在落库之前，实际库里已经有 %d 条", n)
	}
}

func TestCreate_baseUomId为空时拒绝(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	_, err := svc.Create(ctx, repo.CreateInput{
		IdempotencyKey: "svc-l3-uom-empty", SKU: "P-L3-UOM-EMPTY", Name: "缺单位测试"})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("base_uom_id 为空应该拒绝并返回 ErrInvalidArgument，实际：%v", err)
	}
}

func TestCreate_standardCost为负数时拒绝(t *testing.T) {
	svc, _, db := newTestService(t)
	ctx := context.Background()
	ea := uomID(t, db, "EA")

	_, err := svc.Create(ctx, repo.CreateInput{
		IdempotencyKey: "svc-l3-cost-neg", SKU: "P-L3-COST-NEG", Name: "负成本测试",
		BaseUOMID: ea, StandardCost: "-1"})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("standard_cost 为负数应该拒绝并返回 ErrInvalidArgument，实际：%v", err)
	}
}

// TestCreate_standardCost为0时允许 是上面那条的边界对照组：0 是合法值。
func TestCreate_standardCost为0时允许(t *testing.T) {
	svc, _, db := newTestService(t)
	ctx := context.Background()
	ea := uomID(t, db, "EA")

	p, err := svc.Create(ctx, repo.CreateInput{
		IdempotencyKey: "svc-l3-cost-zero", SKU: "P-L3-COST-ZERO", Name: "零成本测试",
		BaseUOMID: ea, StandardCost: "0"})
	if err != nil {
		t.Fatalf("standard_cost 为 0 应该允许，实际报错：%v", err)
	}
	if p.StandardCost != "0.00" {
		t.Fatalf("期望落库后是 0.00，实际 %q", p.StandardCost)
	}
}

func TestCreate_trackingType不合法时拒绝(t *testing.T) {
	svc, _, db := newTestService(t)
	ctx := context.Background()
	ea := uomID(t, db, "EA")

	_, err := svc.Create(ctx, repo.CreateInput{
		IdempotencyKey: "svc-l3-tracking-bad", SKU: "P-L3-TRACKING-BAD", Name: "追踪类型测试",
		BaseUOMID: ea, TrackingType: "WHATEVER"})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("非法 tracking_type 应该拒绝，实际：%v", err)
	}
}

func TestUpdate_版本不一致时拒绝(t *testing.T) {
	svc, r, db := newTestService(t)
	ctx := context.Background()
	ea := uomID(t, db, "EA")

	p, err := r.Create(ctx, repo.CreateInput{
		IdempotencyKey: "svc-update-001", SKU: "P-SVC-UPDATE", Name: "旧名字", BaseUOMID: ea})
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.Update(ctx, UpdateInput{
		IdempotencyKey: "svc-update-002", ID: p.ID, Version: p.Version + 1, Name: "新名字"})
	if err == nil {
		t.Fatal("version 不一致时应该拒绝，实际没报错")
	}

	got, _, err := r.BatchGet(ctx, []string{p.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "旧名字" {
		t.Fatalf("version 冲突时不该写穿，实际：%+v", got)
	}
}

// TestSetStatus_两个方向都允许流转 同 mdm-customer 的判据：DISABLED 不是
// 终态，历史订单/库存流水仍引用这条产品记录（设计计划 §2、§7）。
func TestSetStatus_两个方向都允许流转(t *testing.T) {
	svc, r, db := newTestService(t)
	ctx := context.Background()
	ea := uomID(t, db, "EA")

	p, err := r.Create(ctx, repo.CreateInput{
		IdempotencyKey: "svc-status-001", SKU: "P-SVC-STATUS", Name: "状态流转测试", BaseUOMID: ea})
	if err != nil {
		t.Fatal(err)
	}

	disabled, err := svc.SetStatus(ctx, SetStatusInput{
		IdempotencyKey: "svc-status-002", ID: p.ID, Version: p.Version, Status: "DISABLED"})
	if err != nil {
		t.Fatalf("ACTIVE → DISABLED 应该允许：%v", err)
	}

	reactivated, err := svc.SetStatus(ctx, SetStatusInput{
		IdempotencyKey: "svc-status-003", ID: p.ID, Version: disabled.Version, Status: "ACTIVE"})
	if err != nil {
		t.Fatalf("DISABLED → ACTIVE 应该允许（不是终态），实际报错：%v", err)
	}
	if reactivated.Status != "ACTIVE" {
		t.Fatalf("期望状态回到 ACTIVE，实际 %q", reactivated.Status)
	}
}

func TestSetStatus_停用时发出disabled事件(t *testing.T) {
	svc, r, db := newTestService(t)
	ctx := context.Background()
	ea := uomID(t, db, "EA")

	p, err := r.Create(ctx, repo.CreateInput{
		IdempotencyKey: "svc-event-001", SKU: "P-SVC-EVENT", Name: "事件测试", BaseUOMID: ea})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := svc.SetStatus(ctx, SetStatusInput{
		IdempotencyKey: "svc-event-002", ID: p.ID, Version: p.Version, Status: "DISABLED"})
	if err != nil {
		t.Fatal(err)
	}

	var n int
	if err := besdk.WithTx(ctx, db, "mdm_product_rw", "mdm_product",
		func(tx *sql.Tx) error {
			return tx.QueryRow(
				`SELECT count(*) FROM event_outbox
					WHERE subject = 'mdm.product.disabled.v1' AND aggregate_id = $1 AND version >= $2`,
				p.ID, updated.Version).Scan(&n)
		}); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("期望落 1 条 mdm.product.disabled.v1，实际 %d 条", n)
	}
}

func TestConvertQuantity_入参空值时拒绝(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	if _, err := svc.ConvertQuantity(ctx, "1", "", "1", "2"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("qty 为空应该拒绝，实际：%v", err)
	}
	if _, err := svc.ConvertQuantity(ctx, "1", "5", "", "2"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("from_uom_id 为空应该拒绝，实际：%v", err)
	}
}
