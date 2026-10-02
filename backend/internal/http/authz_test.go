package http

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	besdk "github.com/brickKit/be-sdk-go"
	"github.com/brickKit/mdm-product/v2/backend/internal/repo"
	"github.com/brickKit/mdm-product/v2/backend/internal/service"
)

// 这里测的是路由与权限键的绑定：请求走 RegisterRoutes 注册的真实路由与 SDK 的
// 权限中间件，JWKS 与 authz bundle 由测试自己起的 HTTP 服务提供，token 用测试
// 自己的 RSA 密钥签发。判定逻辑本身（验签、并集展开）是 SDK 的事；这里只守
// "哪条路由要哪个键"。

// 两个测试角色：编辑者能看、能改，但不能启用 / 停用；状态管理员只能启用 / 停用。
var testBundleRoles = map[string][]string{
	"product-editor": {"mdm.product.view", "mdm.product.edit"},
	"product-status": {"mdm.product.set_status"},
}

type authzHarness struct {
	t   *testing.T
	key *rsa.PrivateKey
	eng *gin.Engine
}

func newAuthzHarness(t *testing.T, svc *service.Service) *authzHarness {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "test", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	}))
	t.Cleanup(jwks.Close)
	bundle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"roles": testBundleRoles, "stale_since": map[string]int64{}})
	}))
	t.Cleanup(bundle.Close)

	// InitShellAuthz 是 SDK 导出的、装配进程级验签器与 bundle 轮询的唯一入口
	// （RunStandalone 内部走的是同一个函数）。
	besdk.InitShellAuthz(t.Context(), besdk.NewConfig(map[string]string{
		"IAM_JWKS_URL": jwks.URL, "AUTHZ_BUNDLE_URL": bundle.URL,
	}), slog.Default())

	// engine 与生产同一个构造（besdk.NewGinEngine）：handler 里 c.Error 交回的
	// 业务错误由 SDK 的中间件翻成真实状态码，不会被当成 200。
	gin.SetMode(gin.TestMode)
	eng := besdk.NewGinEngine(besdk.NewShellRuntime(besdk.ShellModuleConfig{ComponentID: "mdm/product"}, nil, nil))
	RegisterRoutes(eng, svc)
	return &authzHarness{t: t, key: key, eng: eng}
}

func (h *authzHarness) token(role string) string {
	h.t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"sub": "test-" + role, "roles": []string{role},
		"iat": time.Now().Add(-time.Minute).Unix(), "exp": time.Now().Add(time.Hour).Unix(),
	})
	tok.Header["kid"] = "test"
	s, err := tok.SignedString(h.key)
	if err != nil {
		h.t.Fatal(err)
	}
	return s
}

func (h *authzHarness) do(method, path, role string, body any) *httptest.ResponseRecorder {
	h.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			h.t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.token(role))
	w := httptest.NewRecorder()
	h.eng.ServeHTTP(w, req)
	return w
}

// waitReady 等 JWKS 与 bundle 都拉到：之前权限中间件回 401 / 503。
func (h *authzHarness) waitReady(path string) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		w := h.do(http.MethodGet, path, "product-editor", nil)
		if w.Code == http.StatusOK {
			return
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("5 秒内权限判定没有就绪：GET %s 仍是 %d（%s）", path, w.Code, w.Body.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestSetStatus路由要set_status键_只有edit键得到403：启用 / 停用与编辑是两个
// 权限键，"能编辑就能停用"是错的。持有 mdm.product.edit、没有
// mdm.product.set_status 的用户调 POST /products/{id}/status 必须 403；
// 同一个人编辑照常 200；只有 set_status 的人启用 / 停用照常 200。
func TestSetStatus路由要set_status键_只有edit键得到403(t *testing.T) {
	r, db := testRepo(t)
	ctx := context.Background()
	svc := service.New(r, slog.Default())
	h := newAuthzHarness(t, svc)

	p, err := r.Create(ctx, repo.CreateInput{
		IdempotencyKey: fmt.Sprintf("http-authz-%x", time.Now().UnixNano()), Name: "权限测试产品", BaseUOMID: eaUOM(t, db)})
	if err != nil {
		t.Fatal(err)
	}
	base := "/mdm/product/products/" + p.ID
	h.waitReady(base)

	statusBody := func(version int64, s string) map[string]any {
		return map[string]any{"idempotency_key": fmt.Sprintf("http-authz-st-%x", time.Now().UnixNano()), "version": version, "status": s}
	}

	if w := h.do(http.MethodPost, base+"/status", "product-editor", statusBody(p.Version, "DISABLED")); w.Code != http.StatusForbidden {
		t.Fatalf("只有 mdm.product.edit 的用户启用 / 停用应得 403，实际 %d（%s）", w.Code, w.Body.String())
	}

	w := h.do(http.MethodPatch, base, "product-editor", map[string]any{
		"idempotency_key": fmt.Sprintf("http-authz-up-%x", time.Now().UnixNano()), "version": p.Version, "name": "改过名的权限测试产品"})
	if w.Code != http.StatusOK {
		t.Fatalf("有 mdm.product.edit 的用户编辑应得 200，实际 %d（%s）", w.Code, w.Body.String())
	}
	var edited productDTO
	if err := json.Unmarshal(w.Body.Bytes(), &edited); err != nil {
		t.Fatal(err)
	}

	if w := h.do(http.MethodPost, base+"/status", "product-status", statusBody(edited.Version, "DISABLED")); w.Code != http.StatusOK {
		t.Fatalf("有 mdm.product.set_status 的用户启用 / 停用应得 200，实际 %d（%s）", w.Code, w.Body.String())
	}
}
