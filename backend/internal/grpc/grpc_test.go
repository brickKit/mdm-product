package grpc

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	besdk "github.com/brickKit/be-sdk-go"
	productv1 "github.com/brickKit/mdm-product/gen/mdm/product/v1"

	"github.com/brickKit/mdm-product/v2/backend/internal/repo"
	"github.com/brickKit/mdm-product/v2/backend/internal/service"
)

// 这里测的是 gRPC 层把请求字段接对了（枚举映射、过滤参数），业务规则由
// service / repo 的测试守。

func testServer(t *testing.T) (productv1.ProductServiceServer, *repo.Repo, string) {
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
	var ea int64
	if err := besdk.WithTx(context.Background(), db, "mdm_product_rw", "mdm_product", func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT id FROM uoms WHERE code = 'EA'`).Scan(&ea)
	}); err != nil {
		t.Fatal(err)
	}
	r := repo.New(db, "mdm_product_rw", "mdm_product")
	return New(service.New(r, slog.Default())), r, strconv.FormatInt(ea, 10)
}

func key(prefix string) string { return fmt.Sprintf("%s-%x", prefix, time.Now().UnixNano()) }

// TestSetStatus_未指定状态时拒绝：PRODUCT_STATUS_UNSPECIFIED 是调用方漏填了
// status，不能悄悄当成 ACTIVE 把一个停用的产品重新启用。
func TestSetStatus_未指定状态时拒绝(t *testing.T) {
	srv, r, ea := testServer(t)
	ctx := context.Background()
	p, err := r.Create(ctx, repo.CreateInput{IdempotencyKey: key("grpc-unspecified"), Name: "漏填状态测试", BaseUOMID: ea})
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := r.SetStatus(ctx, repo.SetStatusInput{
		IdempotencyKey: key("grpc-unspecified-off"), ID: p.ID, Version: p.Version, Status: "DISABLED"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = srv.SetStatus(ctx, &productv1.SetStatusRequest{
		IdempotencyKey: key("grpc-unspecified-set"), Id: p.ID, Version: disabled.Version,
		Status: productv1.ProductStatus_PRODUCT_STATUS_UNSPECIFIED})
	if got := status.Code(err); got != codes.InvalidArgument {
		t.Fatalf("status 未指定应返回 InvalidArgument，实际 %v（%v）", got, err)
	}
	got, _, err := r.BatchGet(ctx, []string{p.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Status != "DISABLED" {
		t.Fatalf("漏填状态不该改动产品，实际：%+v", got)
	}
}

// TestList_gRPC把q传给列表：BFF 的产品搜索走 gRPC List，q 必须接到 repo。
func TestList_gRPC把q传给列表(t *testing.T) {
	srv, r, ea := testServer(t)
	ctx := context.Background()
	tok := key("gq")

	hit, err := r.Create(ctx, repo.CreateInput{IdempotencyKey: tok + "-hit", SKU: tok, Name: "按 sku 搜到的产品", BaseUOMID: ea})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Create(ctx, repo.CreateInput{IdempotencyKey: tok + "-miss", Name: "别的产品", BaseUOMID: ea}); err != nil {
		t.Fatal(err)
	}

	res, err := srv.List(ctx, &productv1.ListRequest{Q: tok})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Products) != 1 || res.Products[0].Id != hit.ID {
		t.Fatalf("q=%s 期望只返回产品 %s，实际返回 %d 条", tok, hit.ID, len(res.Products))
	}
}
