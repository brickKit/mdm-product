package http

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/jackc/pgx/v5/stdlib"

	besdk "github.com/brickKit/be-sdk-go"
	"github.com/brickKit/mdm-product/v2/backend/internal/repo"
	"github.com/brickKit/mdm-product/v2/backend/internal/service"
)

// 这里测的是 REST 层把查询参数接对了：handler 直接挂在测试自己的 engine 上，
// 不经过权限中间件。

func testRepo(t *testing.T) (*repo.Repo, *sql.DB) {
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
	return repo.New(db, "mdm_product_rw", "mdm_product"), db
}

// eaUOM 取迁移播种的基准单位"个"（EA）的 id。
func eaUOM(t *testing.T, db *sql.DB) string {
	t.Helper()
	var id int64
	if err := besdk.WithTx(context.Background(), db, "mdm_product_rw", "mdm_product", func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT id FROM uoms WHERE code = 'EA'`).Scan(&id)
	}); err != nil {
		t.Fatal(err)
	}
	return strconv.FormatInt(id, 10)
}

type listBody struct {
	Products []productDTO `json:"products"`
	Next     string       `json:"next_cursor"`
}

func getList(t *testing.T, svc *service.Service, query url.Values) (int, listBody) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	eng := gin.New()
	eng.GET("/products", listHandler(svc))
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/products?"+query.Encode(), nil))
	var body listBody
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("响应不是合法 JSON：%v（%s）", err, w.Body.String())
		}
	}
	return w.Code, body
}

// TestList_REST按created_after放宽默认时间窗口：契约里 GET /products 带
// created_after / created_before，不传时默认最近 90 天；调用方要看更早建档的
// 产品就显式放宽。一个 200 天前建档的产品，给了时间范围就必须查得到。
func TestList_REST按created_after放宽默认时间窗口(t *testing.T) {
	r, db := testRepo(t)
	ctx := context.Background()
	svc := service.New(r, slog.Default())

	key := fmt.Sprintf("http-window-%x", time.Now().UnixNano())
	p, err := r.Create(ctx, repo.CreateInput{IdempotencyKey: key, Name: "老产品", BaseUOMID: eaUOM(t, db)})
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-200 * 24 * time.Hour)
	if err := besdk.WithTx(ctx, db, "mdm_product_rw", "mdm_product", func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE products SET created_at = $1 WHERE id = $2`, old, p.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	from := old.Add(-time.Hour).UTC().Format(time.RFC3339)
	to := old.Add(time.Hour).UTC().Format(time.RFC3339)
	code, body := getList(t, svc, url.Values{"created_after": {from}, "created_before": {to}, "page_size": {"200"}})
	if code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d", code)
	}
	found := false
	for _, got := range body.Products {
		if got.ID == p.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("给了 created_after=%s created_before=%s，200 天前的产品 %s 应该在结果里（实际 %d 条）",
			from, to, p.ID, len(body.Products))
	}
}

// TestList_REST的时间参数格式不对返回400：时间参数写错是调用方的错，不能悄悄
// 忽略、按默认窗口返回一页看起来正常的结果。
func TestList_REST的时间参数格式不对返回400(t *testing.T) {
	r, _ := testRepo(t)
	svc := service.New(r, slog.Default())
	for _, k := range []string{"created_after", "created_before"} {
		code, _ := getList(t, svc, url.Values{k: {"昨天"}})
		if code != http.StatusBadRequest {
			t.Fatalf("%s 不是 RFC 3339 时间应返回 400，实际 %d", k, code)
		}
	}
}
