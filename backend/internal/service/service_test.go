package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	besdk "github.com/brickKit/be-sdk-go"
	"github.com/brickKit/mdm-product/v2/backend/internal/repo"
	_ "github.com/jackc/pgx/v5/stdlib"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// runKey 给幂等键与 sku 加上本次运行独有的后缀。测试库跨运行保留数据：固定的
// 幂等键从第二次运行起只会命中幂等重放、不再走写路径，固定的 sku 会撞唯一索引。
var runKeySeq atomic.Int64

func runKey(prefix string) string {
	return fmt.Sprintf("%s-%x-%d", prefix, time.Now().UnixNano(), runKeySeq.Add(1))
}

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
		IdempotencyKey: runKey("svc-l3-cost-zero"), SKU: runKey("P-L3-COST-ZERO"), Name: "零成本测试",
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
		IdempotencyKey: runKey("svc-update-001"), SKU: runKey("P-SVC-UPDATE"), Name: "旧名字", BaseUOMID: ea})
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.Update(ctx, UpdateInput{
		IdempotencyKey: runKey("svc-update-002"), ID: p.ID, Version: p.Version + 1, Name: "新名字"})
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

// TestSetStatus_两个方向都允许流转：DISABLED 不是终态，历史订单 / 库存流水
// 仍引用这条产品记录，停用之后可以重新启用。
func TestSetStatus_两个方向都允许流转(t *testing.T) {
	svc, r, db := newTestService(t)
	ctx := context.Background()
	ea := uomID(t, db, "EA")

	p, err := r.Create(ctx, repo.CreateInput{
		IdempotencyKey: runKey("svc-status-001"), SKU: runKey("P-SVC-STATUS"), Name: "状态流转测试", BaseUOMID: ea})
	if err != nil {
		t.Fatal(err)
	}

	disabled, err := svc.SetStatus(ctx, SetStatusInput{
		IdempotencyKey: runKey("svc-status-002"), ID: p.ID, Version: p.Version, Status: "DISABLED"})
	if err != nil {
		t.Fatalf("ACTIVE → DISABLED 应该允许：%v", err)
	}

	reactivated, err := svc.SetStatus(ctx, SetStatusInput{
		IdempotencyKey: runKey("svc-status-003"), ID: p.ID, Version: disabled.Version, Status: "ACTIVE"})
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
		IdempotencyKey: runKey("svc-event-001"), SKU: runKey("P-SVC-EVENT"), Name: "事件测试", BaseUOMID: ea})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := svc.SetStatus(ctx, SetStatusInput{
		IdempotencyKey: runKey("svc-event-002"), ID: p.ID, Version: p.Version, Status: "DISABLED"})
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

// TestList_非法游标映射成InvalidArgument：游标被截断、篡改或根本不是本组件发的，
// 是调用方传错了参数，应当回 400，不能当成服务端故障回 500。
func TestList_非法游标映射成InvalidArgument(t *testing.T) {
	svc, _, _ := newTestService(t)
	for _, cursor := range []string{"!!!不是base64", "bm90LWEtY3Vyc29y"} {
		_, err := svc.List(context.Background(), repo.ListInput{Cursor: cursor})
		if err == nil {
			t.Fatalf("cursor=%q 应该报错", cursor)
		}
		if got := status.Code(ToStatus(err)); got != codes.InvalidArgument {
			t.Fatalf("cursor=%q 应映射成 InvalidArgument，实际 %v（%v）", cursor, got, err)
		}
	}
}

// TestSetStatus_非法状态值拒绝且不落库：REST 的 status 是自由字符串，只有
// ACTIVE / DISABLED 两个合法值；别的值（含空串）要在落库之前拒绝，否则
// products.status 里会出现谁都不认识的状态。
func TestSetStatus_非法状态值拒绝且不落库(t *testing.T) {
	svc, r, db := newTestService(t)
	ctx := context.Background()
	p, err := r.Create(ctx, repo.CreateInput{
		IdempotencyKey: runKey("svc-badstatus"), Name: "非法状态测试", BaseUOMID: uomID(t, db, "EA")})
	if err != nil {
		t.Fatal(err)
	}

	for _, s := range []string{"DELETED", "disabled", ""} {
		_, err := svc.SetStatus(ctx, SetStatusInput{
			IdempotencyKey: runKey("svc-badstatus-set"), ID: p.ID, Version: p.Version, Status: s})
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("status=%q 应该拒绝并返回 ErrInvalidArgument，实际：%v", s, err)
		}
	}
	got, _, err := r.BatchGet(ctx, []string{p.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Status != "ACTIVE" || got[0].Version != p.Version {
		t.Fatalf("非法状态值不该落库，实际：%+v", got)
	}
}

// 下面几条测的是"调用方的错"映射成 4xx，而不是 500：重复的 sku、不存在或
// 不是数字的单位 / 分类 / 产品 id。500 会让前端只能显示"系统错误"，调用方
// 也分不清该改请求还是该重试。

func codeOf(err error) codes.Code { return status.Code(ToStatus(err)) }

func TestCreate_重复sku映射成AlreadyExists(t *testing.T) {
	svc, _, db := newTestService(t)
	ctx := context.Background()
	ea := uomID(t, db, "EA")
	sku := runKey("P-DUP")

	if _, err := svc.Create(ctx, repo.CreateInput{IdempotencyKey: runKey("svc-dup-a"), SKU: sku, Name: "甲", BaseUOMID: ea}); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Create(ctx, repo.CreateInput{IdempotencyKey: runKey("svc-dup-b"), SKU: sku, Name: "乙", BaseUOMID: ea})
	if got := codeOf(err); got != codes.AlreadyExists {
		t.Fatalf("sku %s 已被占用，第二次建应映射成 AlreadyExists，实际 %v（%v）", sku, got, err)
	}
}

func TestCreate_引用不存在或不合法的单位与分类映射成InvalidArgument(t *testing.T) {
	svc, _, db := newTestService(t)
	ctx := context.Background()
	ea := uomID(t, db, "EA")

	cases := []struct {
		name string
		in   repo.CreateInput
	}{
		{"base_uom_id 不存在", repo.CreateInput{BaseUOMID: "999999999"}},
		{"base_uom_id 不是数字", repo.CreateInput{BaseUOMID: "EA"}},
		{"category_id 不存在", repo.CreateInput{BaseUOMID: ea, CategoryID: "999999999"}},
		{"category_id 不是数字", repo.CreateInput{BaseUOMID: ea, CategoryID: "电子"}},
	}
	for _, c := range cases {
		in := c.in
		in.IdempotencyKey, in.Name = runKey("svc-badref"), "引用测试"
		_, err := svc.Create(ctx, in)
		if got := codeOf(err); got != codes.InvalidArgument {
			t.Fatalf("%s：应映射成 InvalidArgument，实际 %v（%v）", c.name, got, err)
		}
	}
}

func TestUpdate与SetStatus_产品不存在映射成NotFound(t *testing.T) {
	svc, r, db := newTestService(t)
	ctx := context.Background()
	p, err := r.Create(ctx, repo.CreateInput{IdempotencyKey: runKey("svc-nf"), Name: "存在的产品", BaseUOMID: uomID(t, db, "EA")})
	if err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"999999999", "abc"} {
		_, err := svc.Update(ctx, UpdateInput{IdempotencyKey: runKey("svc-nf-up"), ID: id, Version: 1, Name: "新名字"})
		if got := codeOf(err); got != codes.NotFound {
			t.Fatalf("Update id=%s：应映射成 NotFound，实际 %v（%v）", id, got, err)
		}
		_, err = svc.SetStatus(ctx, SetStatusInput{IdempotencyKey: runKey("svc-nf-st"), ID: id, Version: 1, Status: "DISABLED"})
		if got := codeOf(err); got != codes.NotFound {
			t.Fatalf("SetStatus id=%s：应映射成 NotFound，实际 %v（%v）", id, got, err)
		}
	}
	// 对照：产品存在、version 不对仍是乐观锁冲突。
	_, err = svc.Update(ctx, UpdateInput{IdempotencyKey: runKey("svc-nf-stale"), ID: p.ID, Version: p.Version + 1, Name: "新名字"})
	if got := codeOf(err); got != codes.Aborted {
		t.Fatalf("version 不对应映射成 Aborted，实际 %v（%v）", got, err)
	}
}

func TestGet与BatchGet_不是数字的id当成不存在(t *testing.T) {
	svc, r, db := newTestService(t)
	ctx := context.Background()
	p, err := r.Create(ctx, repo.CreateInput{IdempotencyKey: runKey("svc-badid"), Name: "批量取测试", BaseUOMID: uomID(t, db, "EA")})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Get(ctx, "abc"); codeOf(err) != codes.NotFound {
		t.Fatalf("Get(abc) 应映射成 NotFound，实际 %v（%v）", codeOf(err), err)
	}
	found, missing, err := svc.BatchGet(ctx, []string{"abc", p.ID})
	if err != nil {
		t.Fatalf("BatchGet 对不是数字的 id 不该报错：%v", err)
	}
	if len(found) != 1 || found[0].ID != p.ID || len(missing) != 1 || missing[0] != "abc" {
		t.Fatalf("期望命中 %s、缺失 abc，实际 found=%d missing=%v", p.ID, len(found), missing)
	}
}

// TestConvertQuantity_不合法的数量与id映射成4xx：qty 不是十进制数是 400；
// 产品 / 单位 id 不是数字，和不存在一样是 404。
func TestConvertQuantity_不合法的数量与id映射成4xx(t *testing.T) {
	svc, r, db := newTestService(t)
	ctx := context.Background()
	ea, box := uomID(t, db, "EA"), uomID(t, db, "BOX")
	p, err := r.Create(ctx, repo.CreateInput{IdempotencyKey: runKey("svc-conv-bad"), Name: "换算入参测试", BaseUOMID: ea})
	if err != nil {
		t.Fatal(err)
	}

	for _, qty := range []string{"两箱", "1e3", "1,5", "NaN"} {
		if _, err := svc.ConvertQuantity(ctx, p.ID, qty, ea, box); codeOf(err) != codes.InvalidArgument {
			t.Fatalf("qty=%q 应映射成 InvalidArgument，实际 %v（%v）", qty, codeOf(err), err)
		}
	}
	for _, c := range [][3]string{{"abc", ea, box}, {p.ID, "EA", box}, {p.ID, ea, "BOX"}} {
		if _, err := svc.ConvertQuantity(ctx, c[0], "1", c[1], c[2]); codeOf(err) != codes.NotFound {
			t.Fatalf("product=%s from=%s to=%s 应映射成 NotFound，实际 %v（%v）", c[0], c[1], c[2], codeOf(err), err)
		}
	}
	// 对照：合法的小数照常换算。
	if got, err := svc.ConvertQuantity(ctx, p.ID, "24.0", ea, box); err != nil || got != "2" {
		t.Fatalf("24.0 个换算成箱应是 2，实际 %q（%v）", got, err)
	}
}

// TestCreate_standardCost不是十进制数时拒绝：NaN、科学计数法这类写法
// strconv.ParseFloat 认、PostgreSQL 的 NUMERIC 也认（NaN 会原样落库），但不是
// 金额字段约定的十进制字符串。
func TestCreate_standardCost不是十进制数时拒绝(t *testing.T) {
	svc, _, db := newTestService(t)
	ctx := context.Background()
	ea := uomID(t, db, "EA")
	for _, cost := range []string{"NaN", "1e3", "Inf", "12,5"} {
		_, err := svc.Create(ctx, repo.CreateInput{
			IdempotencyKey: runKey("svc-cost-bad"), Name: "成本格式测试", BaseUOMID: ea, StandardCost: cost})
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("standard_cost=%q 应该拒绝并返回 ErrInvalidArgument，实际：%v", cost, err)
		}
	}
}
