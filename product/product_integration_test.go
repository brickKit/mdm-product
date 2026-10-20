//go:build integration

package product

// The product component against PostgreSQL and the bus over its real surfaces (L2). mdm/org is a stand-in that speaks
// its published contract over mTLS. Needs BE_SDK_TEST_PG_ADMIN and BE_SDK_TEST_NATS_URL, as be-sdk-go's integration
// tests. The member listens on 28082 (user) and 9092 (system); the stand-in on 19094.
// Run: go test -tags integration ./product/

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/brickKit/be-sdk-go/v3/config"
	"github.com/brickKit/be-sdk-go/v3/ids"
	"github.com/brickKit/be-sdk-go/v3/launcher"
	"github.com/brickKit/be-sdk-go/v3/sectest"
	orgv3 "github.com/brickKit/mdm-org/contract/v3/gen/go/be/mdm/org/v3"
	productv3 "github.com/brickKit/mdm-product/contract/v3/gen/go/be/mdm/product/v3"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const (
	itPort   = "28082"
	itBase   = "http://localhost:" + itPort
	itSystem = "localhost:9092"
	itOrg    = "localhost:19094"
	itSchema = "mdm_product_it"
)

// org is the stand-in mdm/org: every legal entity it knows is active.
type org struct {
	orgv3.UnimplementedSourceServiceServer
	known map[string]bool
}

func (o *org) Read(_ context.Context, in *orgv3.ReadRequest) (*orgv3.ReadResponse, error) {
	out := &orgv3.ReadResponse{Stamp: &orgv3.Stamp{}}
	for _, id := range in.GetIds() {
		r := &orgv3.ReadResult{Id: id, State: orgv3.ReadState_READ_STATE_PURGED}
		if o.known[id] {
			data, _ := json.Marshal(map[string]any{"id": id, "state": "active"})
			r = &orgv3.ReadResult{Id: id, State: orgv3.ReadState_READ_STATE_EXISTS, Version: 1, Data: data}
		}
		out.Results = append(out.Results, r)
	}
	return out, nil
}

type client struct {
	t     *testing.T
	token string
}

func (c client) do(method, path string, body any) (int, map[string]any) {
	c.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, itBase+path, r)
	req.Header.Set("Authorization", c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(c.t, err)
	defer resp.Body.Close()
	var out map[string]any
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func TestProductEndToEnd(t *testing.T) {
	adminURL, natsURL := os.Getenv("BE_SDK_TEST_PG_ADMIN"), os.Getenv("BE_SDK_TEST_NATS_URL")
	if adminURL == "" || natsURL == "" {
		t.Fatal("BE_SDK_TEST_PG_ADMIN and BE_SDK_TEST_NATS_URL are needed")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, adminURL)
	require.NoError(t, err)
	defer admin.Close(ctx)
	_, err = admin.Exec(ctx, "DROP SCHEMA IF EXISTS "+itSchema+" CASCADE; CREATE SCHEMA "+itSchema)
	require.NoError(t, err)
	u, err := url.Parse(adminURL)
	require.NoError(t, err)
	pw, _ := u.User.Password()
	w := sectest.New(t)
	w.GrantAll(sectest.Alice, Definitions())

	leA, leB := ids.New().String(), ids.New().String()
	ln, err := net.Listen("tcp", itOrg)
	require.NoError(t, err)
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(w.TLS(t, Org).ServerConfig())))
	orgv3.RegisterSourceServiceServer(srv, &org{known: map[string]bool{leA: true, leB: true}})
	go func() { _ = srv.Serve(ln) }()
	defer srv.Stop()

	portFree(t, itPort, itSystem)
	env := w.Env(t, ID)
	for k, v := range map[string]string{"COMPONENT_ID": ID, "COMPONENT_VERSION": Version, "PORT": itPort,
		"STOP_DRAIN_DELAY": "50ms", "PG_HOST": u.Hostname(), "PG_PORT": u.Port(), "PG_DATABASE": strings.TrimPrefix(u.Path, "/"),
		"PG_USER": u.User.Username(), "PG_PASSWORD": pw, "PG_SCHEMA": itSchema, "PG_OWNER_USER": u.User.Username(),
		"PG_OWNER_PASSWORD": pw, "BUS_URL": natsURL, "IMPLEMENTATION_INSTANCE": ids.New().String(),
		"MDM_ORG_GRPC_ENDPOINT": "http://" + itOrg} {
		env[k] = v
	}
	var logs bytes.Buffer
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(logs.String())
		}
	})
	opts := launcher.Options{Stdout: &logs, Stderr: &logs,
		Env: config.Env{LookupEnv: func(k string) (string, bool) { v, ok := env[k]; return v, ok }, Read: os.ReadFile}}
	migrate := opts
	migrate.Args = []string{"migrate"}
	require.Equal(t, launcher.ExitOK, launcher.Run(ctx, Definition(), migrate))
	sctx, stop := context.WithCancel(ctx)
	done := make(chan int, 1)
	go func() { done <- launcher.Run(sctx, Definition(), opts) }()
	defer func() { stop(); <-done }()
	require.Eventually(t, func() bool {
		r, err := http.Get(itBase + "/readyz")
		if err == nil {
			r.Body.Close()
		}
		return err == nil && r.StatusCode == 200
	}, 20*time.Second, 100*time.Millisecond)
	alice := client{t, w.Bearer(sectest.Alice)}

	code, cat := alice.do("POST", "/api/product/categories", map[string]any{"code": "TEA", "name": "茶叶"})
	require.Equal(t, 200, code, cat)
	// the boundary card's story: a box holds the product's own number of pieces
	code, tea := alice.do("POST", "/api/product/products", map[string]any{"code": "TEA-GREEN-250G", "kind": "stock",
		"name": "绿茶 250 克", "base_unit": "C62", "category_id": cat["id"], "tracking": "batch",
		"conversions": []map[string]any{{"unit": "BX", "factor": "12.000"}}})
	require.Equal(t, 200, code, tea)
	assert.Equal(t, true, tea["stocked"])
	assert.Equal(t, "12", tea["conversions"].([]any)[0].(map[string]any)["factor"])
	id := tea["id"].(string)
	_, conv := alice.do("GET", "/api/product/products/"+id+"/convert?quantity=3&from_unit=BX&to_unit=C62", nil)
	assert.Equal(t, "36", conv["quantity"])
	assert.Equal(t, "product", conv["source"])
	_, conv = alice.do("GET", "/api/product/products/"+id+"/convert?quantity=40&from_unit=C62&to_unit=BX", nil)
	assert.Equal(t, "3", conv["quantity"], "a box is counted in whole boxes: 40/12 rounds to 3")
	code, noconv := alice.do("GET", "/api/product/products/"+id+"/convert?quantity=1&from_unit=KGM&to_unit=C62", nil)
	assert.Equal(t, 409, code)
	assert.Equal(t, "NO_CONVERSION", noconv["reason"])
	// a catalog conversion within the base unit's category
	_, rice := alice.do("POST", "/api/product/products", map[string]any{"code": "RICE-BULK", "kind": "stock",
		"name": "散装大米", "base_unit": "KGM"})
	_, conv = alice.do("GET", "/api/product/products/"+rice["id"].(string)+"/convert?quantity=2.5&from_unit=TNE&to_unit=KGM", nil)
	assert.Equal(t, "2500.000", conv["quantity"])
	assert.Equal(t, "catalog", conv["source"])

	// the story: a service needs no stock
	_, svc := alice.do("POST", "/api/product/products", map[string]any{"code": "SVC-INSTALL", "kind": "service",
		"name": "上门安装", "base_unit": "HUR"})
	assert.Equal(t, false, svc["stocked"])
	code, _ = alice.do("PATCH", "/api/product/products/"+svc["id"].(string), map[string]any{"tracking": "serial"})
	assert.Equal(t, 400, code, "a service is never tracked by batch or serial")
	// the same content again makes no new version, for a product with no conversions and no availability as well
	svcID := svc["id"].(string)
	code, same := alice.do("PATCH", "/api/product/products/"+svcID, map[string]any{"name": "上门安装"})
	require.Equal(t, 200, code, same)
	assert.EqualValues(t, 1, same["version"], "nothing changed: no new version")
	_, open1 := alice.do("PUT", "/api/product/products/"+svcID+"/legal-entities/"+leB, map[string]any{"sellable": true, "purchasable": false})
	_, open2 := alice.do("PUT", "/api/product/products/"+svcID+"/legal-entities/"+leB, map[string]any{"sellable": true, "purchasable": false})
	assert.EqualValues(t, 2, open1["version"])
	assert.EqualValues(t, 2, open2["version"], "the same availability again: no new version")

	// the story: once inventory has movements, the tracking policy no longer changes (the flag comes from inventory's
	// events in step 7; here it is set the way that consumer will)
	_, err = admin.Exec(ctx, "UPDATE "+itSchema+".product SET has_movements = true WHERE id = $1", id)
	require.NoError(t, err)
	code, moved := alice.do("PATCH", "/api/product/products/"+id, map[string]any{"tracking": "none"})
	assert.Equal(t, 409, code)
	assert.Equal(t, "HAS_MOVEMENTS", moved["reason"])
	code, renamed := alice.do("PATCH", "/api/product/products/"+id, map[string]any{"name": "绿茶 250 克（新包装）"})
	assert.Equal(t, 200, code, "the name still changes")
	assert.EqualValues(t, 2, renamed["version"])

	// availability by legal entity and the eligibility a sales order needs
	code, av := alice.do("PUT", "/api/product/products/"+id+"/legal-entities/"+leA, map[string]any{"sellable": true, "purchasable": false})
	require.Equal(t, 200, code, av)
	code, bad := alice.do("PUT", "/api/product/products/"+id+"/legal-entities/"+ids.New().String(), map[string]any{"sellable": true, "purchasable": true})
	assert.Equal(t, 409, code)
	assert.Equal(t, "LEGAL_ENTITY_NOT_USABLE", bad["reason"])
	elig := func(le, purpose string) map[string]any {
		_, e := alice.do("GET", "/api/product/eligibility?product_id="+id+"&legal_entity_id="+le+"&purpose="+purpose, nil)
		return e
	}
	assert.Equal(t, true, elig(leA, "sell")["eligible"])
	assert.Equal(t, "not_purchasable", elig(leA, "purchase")["reason"])
	assert.Equal(t, "not_opened", elig(leB, "sell")["reason"])
	_, _ = alice.do("POST", "/api/product/products/"+id+"/state", map[string]any{"state": "suspended"})
	assert.Equal(t, "product_suspended", elig(leA, "sell")["reason"])

	// lists and the keyword
	_, found := alice.do("GET", "/api/product/products?q=lc", nil) // 绿茶: l c
	assert.Len(t, found["products"], 1)
	_, page := alice.do("GET", "/api/product/products?limit=2", nil)
	assert.Len(t, page["products"], 2)
	assert.NotEmpty(t, page["next_cursor"])
	_, units := alice.do("GET", "/api/product/units", nil)
	assert.GreaterOrEqual(t, len(units["units"].([]any)), 20)

	// the system surface over mTLS
	conn, err := grpc.NewClient(itSystem, grpc.WithTransportCredentials(credentials.NewTLS(w.TLS(t, "erp/sales").ClientConfig(ID))))
	require.NoError(t, err)
	defer conn.Close()
	ps := productv3.NewProductServiceClient(conn)
	e, err := ps.CheckEligibility(ctx, &productv3.CheckEligibilityRequest{ProductId: svc["id"].(string), LegalEntityId: leA,
		Purpose: productv3.Purpose_PURPOSE_SELL})
	require.NoError(t, err)
	assert.False(t, e.Stocked)
	assert.Equal(t, "not_opened", e.Reason)
	c2, err := ps.Convert(ctx, &productv3.ConvertRequest{ProductId: id, Quantity: "2", FromUnit: "BX", ToUnit: "C62"})
	require.NoError(t, err)
	assert.Equal(t, "24", c2.Quantity)
	read, err := productv3.NewSourceServiceClient(conn).Read(ctx, &productv3.ReadRequest{Aggregate: productv3.Aggregate_AGGREGATE_PRODUCT,
		Ids: []string{id}})
	require.NoError(t, err)
	assert.Contains(t, string(read.Results[0].Data), `"has_movements":true`)
	assert.Len(t, read.Stamp.Positions, 4)
}

// portFree fails at once when a port the member needs is taken: a running cell (host ports 18xxx) or another member
// would answer in its place, and the test would assert against another process.
func portFree(t *testing.T, addrs ...string) {
	for _, a := range addrs {
		if !strings.Contains(a, ":") {
			a = "localhost:" + a
		}
		ln, err := net.Listen("tcp", a)
		require.NoError(t, err, "port %s is taken: is a cell or another member running?", a)
		_ = ln.Close()
	}
}
