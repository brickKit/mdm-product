package repo

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

// 关键字搜索 q（GET /products?q=、gRPC ListRequest.q）的规则：匹配 sku 或
// name，大小写不敏感；sku 或 name 以 q 开头的（前缀匹配）排在只是包含 q 的
// 前面，同一档内按建档时间倒序；去掉首尾空白后为空等于不过滤；% 与 _ 按字面
// 匹配；照常按游标翻页。
//
// 测试库被别的测试并发写入，所以每条测试用一个本次运行独有的 token 当关键字，
// 只断言含 token 的那几行。

type searchFixture struct {
	t   *testing.T
	r   *Repo
	ea  string
	tok string
}

func newSearchFixture(t *testing.T) (*searchFixture, *sql.DB) {
	db := testDB(t)
	return &searchFixture{
		t: t, r: New(db, "mdm_product_rw", "mdm_product"),
		ea: uomID(t, db, "EA"), tok: strings.ToLower(runKey("sq")),
	}, db
}

// mk 建一个产品；sku 为空时自动编号（"P" + 数字，不含 token）。
func (f *searchFixture) mk(sku, name string) *Product {
	f.t.Helper()
	p, err := f.r.Create(context.Background(), CreateInput{
		IdempotencyKey: runKey("search"), SKU: sku, Name: name, BaseUOMID: f.ea})
	if err != nil {
		f.t.Fatal(err)
	}
	return p
}

func (f *searchFixture) list(in ListInput) *ListResult {
	f.t.Helper()
	res, err := f.r.List(context.Background(), in)
	if err != nil {
		f.t.Fatal(err)
	}
	return res
}

func productIDs(ps []*Product) string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.ID)
	}
	return strings.Join(out, ",")
}

func TestList_q前缀匹配在前_同档按建档时间倒序(t *testing.T) {
	f, _ := newSearchFixture(t)
	containsName := f.mk("", "标准件-"+f.tok+"-螺栓")          // 只是 name 包含，最早
	containsSKU := f.mk("X-"+f.tok, "垫片")                 // 只是 sku 包含
	skuPrefix := f.mk(strings.ToUpper(f.tok)+"-01", "螺母") // sku 前缀（大写）
	namePrefix := f.mk("", f.tok+" 润滑油")                  // name 前缀，最晚
	f.mk("", "毫不相干的产品")

	got := productIDs(f.list(ListInput{Q: f.tok, PageSize: 50}).Products)
	want := productIDs([]*Product{namePrefix, skuPrefix, containsSKU, containsName})
	if got != want {
		t.Fatalf("期望前缀匹配（同档按建档时间倒序）在前、包含匹配在后：%s，实际 %s", want, got)
	}
}

func TestList_q大小写不敏感(t *testing.T) {
	f, _ := newSearchFixture(t)
	a := f.mk(strings.ToUpper(f.tok), "大写 sku 的产品")
	b := f.mk("", "Product "+strings.ToUpper(f.tok)+" Pro")

	for _, q := range []string{f.tok, strings.ToUpper(f.tok)} {
		if got, want := productIDs(f.list(ListInput{Q: q, PageSize: 50}).Products), a.ID+","+b.ID; got != want {
			t.Fatalf("q=%q 期望 %s，实际 %s", q, want, got)
		}
	}
}

func TestList_q为空白等于不过滤(t *testing.T) {
	f, _ := newSearchFixture(t)
	p := f.mk("", "不含关键字的产品")
	base := ListInput{PageSize: 200, CreatedAfter: p.CreatedAt.Add(-time.Second), CreatedBefore: p.CreatedAt.Add(time.Second)}

	plain := productIDs(f.list(base).Products)
	if !strings.Contains(","+plain+",", ","+p.ID+",") {
		t.Fatalf("不带 q 的列表里应有产品 %s：%s", p.ID, plain)
	}
	for _, q := range []string{"", "  \t "} {
		in := base
		in.Q = q
		if got := productIDs(f.list(in).Products); got != plain {
			t.Fatalf("q=%q 应等于不过滤：期望 %s，实际 %s", q, plain, got)
		}
	}
}

func TestList_q首尾空白被去掉(t *testing.T) {
	f, _ := newSearchFixture(t)
	p := f.mk("", f.tok+"产品")
	if got := productIDs(f.list(ListInput{Q: "  " + f.tok + " ", PageSize: 50}).Products); got != p.ID {
		t.Fatalf("q 带首尾空白应等同于去掉空白后的 q：期望 %s，实际 %s", p.ID, got)
	}
}

func TestList_q里的通配符按字面匹配(t *testing.T) {
	f, _ := newSearchFixture(t)
	f.mk("", f.tok+"产品")
	pct := f.mk("", f.tok+"%折扣装")

	for _, q := range []string{f.tok + "_", "%" + f.tok, f.tok + "%%"} {
		if n := len(f.list(ListInput{Q: q, PageSize: 50}).Products); n != 0 {
			t.Fatalf("q=%q 里的 %% / _ 应按字面匹配（没有产品含它），实际命中 %d 条", q, n)
		}
	}
	if got := productIDs(f.list(ListInput{Q: f.tok + "%", PageSize: 50}).Products); got != pct.ID {
		t.Fatalf("q=%q 应只命中名字里真有 %% 的产品 %s，实际 %s", f.tok+"%", pct.ID, got)
	}
}

func TestList_q与状态过滤同时生效(t *testing.T) {
	f, _ := newSearchFixture(t)
	on := f.mk("", f.tok+"在售")
	off := f.mk("", f.tok+"停用")
	if _, err := f.r.SetStatus(context.Background(), SetStatusInput{
		IdempotencyKey: runKey("search-off"), ID: off.ID, Version: off.Version, Status: "DISABLED"}); err != nil {
		t.Fatal(err)
	}
	if got := productIDs(f.list(ListInput{Q: f.tok, StatusFilter: "ACTIVE", PageSize: 50}).Products); got != on.ID {
		t.Fatalf("q + status_filter=ACTIVE 期望只有 %s，实际 %s", on.ID, got)
	}
}

// 翻页跨过"前缀 → 包含"的分界：3 条前缀 + 2 条包含，每页 2 条，逐页拼起来
// 必须恰好等于一次取全的结果，不重不漏、顺序一致。
func TestList_q与游标一起翻页不重不漏(t *testing.T) {
	f, _ := newSearchFixture(t)
	for i := 0; i < 2; i++ {
		f.mk("", fmt.Sprintf("包含%s第%d件", f.tok, i))
	}
	for i := 0; i < 3; i++ {
		f.mk("", fmt.Sprintf("%s前缀第%d件", f.tok, i))
	}

	all := f.list(ListInput{Q: f.tok, PageSize: 50})
	if len(all.Products) != 5 {
		t.Fatalf("期望 5 条命中，实际 %d", len(all.Products))
	}
	var paged []*Product
	cursor := ""
	for page := 0; page < 10; page++ {
		res := f.list(ListInput{Q: f.tok, PageSize: 2, Cursor: cursor})
		paged = append(paged, res.Products...)
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	if got, want := productIDs(paged), productIDs(all.Products); got != want {
		t.Fatalf("逐页拼起来应等于一次取全：期望 %s，实际 %s", want, got)
	}
}
