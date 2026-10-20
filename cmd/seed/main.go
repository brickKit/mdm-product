// Command seed fills a running mdm/product with the seed data for people (be-assembly-standard's seed and test data
// conventions): five categories; 48 products (stock with box conversions and batch or serial tracking, services,
// non-stock items; enough to page through) opened for sale and purchase in mdm/org's seeded legal entities CN01 and
// ES01; and the negative data the eligibility answers need: a suspended and a retired product, products not sellable
// in ES01 or not opened there at all. Codes are the fixed idempotency keys, so a second run creates nothing; the legal
// entities are looked up by code in mdm/org (its seed runs first: make seed).
//
//	SEED_IAM_URL (http://localhost:18200)  the identity member's development exchange
//	SEED_USER (test.bob), SEED_PASSWORD     a test user granted mdm/product's and mdm/org's operations
//	MDM_PRODUCT_URL (http://localhost:18082), MDM_ORG_URL (http://localhost:18083)
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
)

const prefix = "「本地测试」"

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

type client struct{ base, token string }

func (c client) do(method, path string, body any, header ...string) (int, map[string]any) {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, r)
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fail("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func (c client) must(method, path string, body any) map[string]any {
	code, out := c.do(method, path, body)
	if code != 200 {
		fail("%s %s: %d %v", method, path, code, out)
	}
	return out
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "seed mdm/product: "+format+"\n", a...)
	os.Exit(1)
}

func token() string {
	pw := os.Getenv("SEED_PASSWORD")
	if pw == "" {
		fail("SEED_PASSWORD is not set (the test users' password in Casdoor)")
	}
	b, _ := json.Marshal(map[string]string{"username": env("SEED_USER", "test.bob"), "password": pw})
	resp, err := http.Post(env("SEED_IAM_URL", "http://localhost:18200")+"/api/iam/dev/token", "application/json", bytes.NewReader(b))
	if err != nil {
		fail("the identity member is not reachable: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&out) != nil {
		fail("the development exchange refused the seed user: %d", resp.StatusCode)
	}
	return out.AccessToken
}

// legalEntity is the ID of mdm/org's seeded legal entity with the code.
func legalEntity(org client, code string) string {
	_, out := org.do("GET", "/api/org/legal-entities?limit=200&q="+url.QueryEscape(code), nil)
	for _, e := range out["legal_entities"].([]any) {
		if e.(map[string]any)["code"] == code {
			return e.(map[string]any)["id"].(string)
		}
	}
	fail("mdm/org has no legal entity %s: run its seed first", code)
	return ""
}

func main() {
	base := env("MDM_PRODUCT_URL", "http://localhost:18082")
	if r, err := http.Get(base + "/readyz"); err != nil || r.StatusCode != 200 { // probe before pouring
		fail("mdm/product at %s is not ready", base)
	}
	t := token()
	c, org := client{base, t}, client{env("MDM_ORG_URL", "http://localhost:18083"), t}
	cn, es := legalEntity(org, "CN01"), legalEntity(org, "ES01")
	cats := map[string]string{}
	for _, k := range []struct{ code, name string }{{"TEA", "茶叶"}, {"COFFEE", "咖啡"}, {"EQUIP", "设备"},
		{"SERVICE", "服务"}, {"SUPPLY", "办公用品"}} {
		cats[k.code] = c.must("POST", "/api/product/categories", map[string]any{"code": k.code, "name": prefix + k.name})["id"].(string)
	}
	type item struct {
		code, name, kind, cat, unit, tracking string
		box                                   string // pieces a box holds; "" no box
	}
	var items []item
	for i, tea := range []string{"龙井", "碧螺春", "铁观音", "普洱", "大红袍", "白毫银针", "祁门红茶", "滇红", "六安瓜片", "黄山毛峰",
		"安吉白茶", "君山银针"} {
		items = append(items, item{fmt.Sprintf("TEA-%02d-250G", i), tea + " 250 克", "stock", "TEA", "C62", "batch", "12"})
		items = append(items, item{fmt.Sprintf("TEA-%02d-BULK", i), tea + " 散装", "stock", "TEA", "KGM", "batch", ""})
	}
	for i, cof := range []string{"云南小粒", "埃塞俄比亚耶加雪菲", "哥伦比亚", "巴西", "危地马拉", "肯尼亚", "曼特宁", "蓝山"} {
		items = append(items, item{fmt.Sprintf("COF-%02d-500G", i), cof + "咖啡豆 500 克", "stock", "COFFEE", "C62", "batch", "6"})
	}
	for i, eq := range []string{"意式咖啡机", "磨豆机", "茶艺台", "冷藏柜", "电子秤", "封口机"} {
		items = append(items, item{fmt.Sprintf("EQ-%02d", i), eq, "stock", "EQUIP", "C62", "serial", ""})
	}
	for i, sv := range []string{"上门安装", "设备保养", "咖啡师培训", "茶艺培训", "物流配送", "远程技术支持"} {
		items = append(items, item{fmt.Sprintf("SVC-%02d", i), sv, "service", "SERVICE", "HUR", "none", ""})
	}
	for i, su := range []string{"包装纸箱", "封箱胶带", "打印纸", "标签纸"} {
		items = append(items, item{fmt.Sprintf("SUP-%02d", i), su, "non_stock", "SUPPLY", "C62", "none", ""})
	}
	ids, states := map[string]string{}, map[string]string{}
	for _, it := range items {
		body := map[string]any{"code": it.code, "kind": it.kind, "name": prefix + it.name, "base_unit": it.unit,
			"category_id": cats[it.cat], "tracking": it.tracking}
		if it.box != "" {
			body["conversions"] = []map[string]any{{"unit": "BX", "factor": it.box}}
		}
		out := c.must("POST", "/api/product/products", body)
		ids[it.code], states[it.code] = out["id"].(string), out["state"].(string)
	}
	open := func(code, le string, sell, buy bool) {
		if states[code] != "active" { // suspended or retired by an earlier run, after its availabilities were set
			return
		}
		c.must("PUT", "/api/product/products/"+ids[code]+"/legal-entities/"+le, map[string]any{"sellable": sell, "purchasable": buy})
	}
	n := 0
	for i, it := range items {
		open(it.code, cn, true, it.kind != "service")
		n++
		switch {
		case i%5 == 0: // not opened in ES01 at all (not_opened)
		case i%5 == 1:
			open(it.code, es, false, true) // bought but not sold in ES01 (not_sellable)
			n++
		default:
			open(it.code, es, true, it.kind != "service")
			n++
		}
	}
	state := func(product, to string) {
		if code, out := c.do("POST", "/api/product/products/"+ids[product]+"/state", map[string]any{"state": to}); code != 200 &&
			!(code == 409 && out["reason"] == "STATE_CONFLICT") {
			fail("%s -> %s: %d %v", product, to, code, out)
		}
	}
	state("TEA-11-BULK", "suspended")
	state("SUP-03", "retired")
	fmt.Printf("seed mdm/product: 5 categories, %d products, %d availabilities, 1 suspended, 1 retired\n", len(items), n)
}
