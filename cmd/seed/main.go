// Command seed fills a running mdm/product with the seed data for people (be-assembly-standard's seed and test data
// conventions):
//
//   - access, by the authorization admin: the roles maintain (the seed user, test.mdm.admin) and read (test.mdm.viewer,
//     the sales accounts, test.unit.admin.cn); the seed user waits until mdm/product takes its grant;
//   - nine categories, two of them with subcategories (tea: green and black; coffee: beans and capsules);
//   - 146 products, enough to page through as a whole and within the larger categories: stock items with one, two or
//     three packaging levels (piece, pack, box; kilogram) and batch or serial tracking, services by the hour or the
//     day, non-stock items;
//   - availability in every usable legal entity: all opened in CN01, in ES01 and US01 some sellable and purchasable,
//     some only purchasable, some not opened at all; ten opened in HK01 while it was active (the seed resumes HK01,
//     opens them and suspends it again, only while they are missing);
//   - the negative data the eligibility answers need: a suspended and a retired product, products not sellable or not
//     opened in a legal entity, products of a suspended legal entity.
//
// Codes and change keys are the fixed idempotency keys, so a second run creates nothing; the legal entities are looked
// up by code in mdm/org (its seed runs first: make seed). The first seed's 48 products keep their codes.
//
//	SEED_IAM_URL (http://localhost:18200)    the identity member's development exchange
//	SEED_AUTHZ_URL (http://localhost:18223)  the authorization member's effect point
//	SEED_ADMIN (test.alice)                  the authorization admin (the install claim)
//	SEED_USER (test.bob), SEED_PASSWORD      the user that writes the data; the test accounts' password
//	MDM_PRODUCT_URL (http://localhost:18082), MDM_ORG_URL (http://localhost:18083)
package main

import (
	"fmt"
	"net/url"
	"os"
	"time"

	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/seed"
)

const prefix = "「本地测试」"

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "seed mdm/product: "+format+"\n", a...)
	os.Exit(1)
}

func check(err error) {
	if err != nil {
		fail("%v", err)
	}
}

type client struct{ seed.Client }

func (c client) do(method, path string, body any) (int, map[string]any) {
	code, out, err := c.Do(method, path, body)
	check(err)
	return code, out
}

func (c client) must(method, path string, body any) map[string]any {
	out, err := c.Must(method, path, body)
	check(err)
	return out
}

// readers are the seed accounts that read products.
var readers = []string{"test.mdm.viewer", "test.sales.cn", "test.sales.es", "test.sales.multi", "test.unit.admin.cn"}

// access grants mdm/product's roles and answers the seed user's token once its grant has reached mdm/product.
func access(base string) string {
	pw := os.Getenv("SEED_PASSWORD")
	if pw == "" {
		fail("SEED_PASSWORD is not set (the test accounts' password in Casdoor)")
	}
	iam, user, admin := env("SEED_IAM_URL", "http://localhost:18200"), env("SEED_USER", "test.bob"), env("SEED_ADMIN", "test.alice")
	accounts, err := seed.Accounts(iam, pw, append([]string{admin, user, "test.mdm.admin"}, readers...)...)
	check(err)
	a, err := seed.NewAuthz(env("SEED_AUTHZ_URL", "http://localhost:18223"), accounts[admin].Token, "mdm/product")
	check(err)
	read := []sdkauthz.Segment{seed.Segment("mdm.product", "mdm.product.read"), seed.Segment("mdm.product_category", "mdm.product_category.read")}
	_, err = a.GrantRoles([]seed.RoleSpec{
		{Key: "maintain", Name: prefix + "产品维护", To: []string{user, "test.mdm.admin"}, Segments: append(read,
			seed.Segment("mdm.product", "mdm.product.edit"), seed.Segment("mdm.product_category", "mdm.product_category.manage"))},
		{Key: "read", Name: prefix + "产品只读", To: readers, Segments: read},
	}, accounts)
	check(err)
	check(seed.NewClient(base, accounts[user].Token).WaitGranted("POST", "/api/product/products", 30*time.Second))
	return accounts[user].Token
}

// legalEntity is the ID of mdm/org's seeded legal entity with the code.
func legalEntity(org client, code string) string {
	_, out := org.do("GET", "/api/org/legal-entities?limit=200&q="+url.QueryEscape(code), nil)
	for _, e := range out["legal_entities"].([]any) {
		if e := e.(map[string]any); e["code"] == code {
			return e["id"].(string)
		}
	}
	fail("mdm/org has no legal entity %s: run its seed first", code)
	return ""
}

type item struct {
	code, name, kind, cat, unit, tracking string
	conv                                  [][2]string // packaging: unit and how many base units it holds
}

// items are the products: the first seed's 48 first (their codes are fixed identifiers), then the ones added later.
func items() []item {
	var out []item
	box := func(n string) [][2]string {
		if n == "" {
			return nil
		}
		return [][2]string{{"BX", n}}
	}
	for i, tea := range []string{"龙井", "碧螺春", "铁观音", "普洱", "大红袍", "白毫银针", "祁门红茶", "滇红", "六安瓜片", "黄山毛峰",
		"安吉白茶", "君山银针"} {
		out = append(out, item{fmt.Sprintf("TEA-%02d-250G", i), tea + " 250 克", "stock", "TEA", "C62", "batch", box("12")})
		out = append(out, item{fmt.Sprintf("TEA-%02d-BULK", i), tea + " 散装", "stock", "TEA", "KGM", "batch", nil})
	}
	for i, cof := range []string{"云南小粒", "埃塞俄比亚耶加雪菲", "哥伦比亚", "巴西", "危地马拉", "肯尼亚", "曼特宁", "蓝山"} {
		out = append(out, item{fmt.Sprintf("COF-%02d-500G", i), cof + "咖啡豆 500 克", "stock", "COFFEE", "C62", "batch", box("6")})
	}
	for i, eq := range []string{"意式咖啡机", "磨豆机", "茶艺台", "冷藏柜", "电子秤", "封口机"} {
		out = append(out, item{fmt.Sprintf("EQ-%02d", i), eq, "stock", "EQUIP", "C62", "serial", nil})
	}
	for i, sv := range []string{"上门安装", "设备保养", "咖啡师培训", "茶艺培训", "物流配送", "远程技术支持"} {
		out = append(out, item{fmt.Sprintf("SVC-%02d", i), sv, "service", "SERVICE", "HUR", "none", nil})
	}
	for i, su := range []string{"包装纸箱", "封箱胶带", "打印纸", "标签纸"} {
		out = append(out, item{fmt.Sprintf("SUP-%02d", i), su, "non_stock", "SUPPLY", "C62", "none", nil})
	}
	// added later: three packaging levels (piece, pack of 6, box of 24) for tins and capsules
	three := [][2]string{{"PK", "6"}, {"BX", "24"}}
	for i, tea := range []string{"龙井", "碧螺春", "安吉白茶", "六安瓜片", "黄山毛峰", "太平猴魁", "祁门红茶", "滇红", "正山小种",
		"金骏眉", "英德红茶", "宜兴红茶"} {
		cat := "TEA-GREEN"
		if i >= 6 {
			cat = "TEA-BLACK"
		}
		out = append(out, item{fmt.Sprintf("TEA-%02d-TIN", i), tea + " 罐装 100 克", "stock", cat, "C62", "batch", three})
	}
	for i, cof := range []string{"云南小粒", "耶加雪菲", "哥伦比亚", "巴西", "危地马拉", "肯尼亚", "曼特宁", "蓝山"} {
		out = append(out, item{fmt.Sprintf("COF-%02d-1KG", i), cof + "咖啡豆 1 公斤", "stock", "COFFEE-BEANS", "KGM", "batch", nil})
		out = append(out, item{fmt.Sprintf("COF-%02d-CAP", i), cof + "咖啡胶囊", "stock", "COFFEE-CAPSULE", "C62", "batch",
			[][2]string{{"PK", "10"}, {"BX", "100"}}})
	}
	for i, w := range []string{"紫砂壶", "玻璃茶壶", "白瓷盖碗", "青瓷茶杯", "公道杯", "茶漏", "茶盘", "茶夹", "茶荷", "茶针",
		"建盏", "汝窑杯", "随手泡", "旅行茶具", "冷泡壶", "茶叶罐", "咖啡杯", "手冲壶", "滤杯", "分享壶"} {
		out = append(out, item{fmt.Sprintf("TW-%02d", i), w, "stock", "TEAWARE", "C62", "none", box("12")})
	}
	for i, p := range []string{"凤梨酥", "蛋黄酥", "绿豆糕", "桃酥", "月饼", "曲奇", "玛德琳", "费南雪", "司康", "布朗尼",
		"抹茶饼干", "红茶饼干", "杏仁饼", "核桃酥", "芝麻饼", "牛轧糖"} {
		out = append(out, item{fmt.Sprintf("PST-%02d", i), p, "stock", "PASTRY", "C62", "batch", [][2]string{{"PK", "4"}, {"BX", "48"}}})
	}
	for i, p := range []string{"礼盒", "茶叶袋", "铝箔袋", "封口夹", "气泡膜", "珍珠棉", "牛皮纸袋", "丝带", "手提袋", "干燥剂",
		"标签贴", "防伪贴"} {
		out = append(out, item{fmt.Sprintf("PKG-%02d", i), p, "stock", "PACK", "C62", "none", [][2]string{{"CT", "200"}}})
	}
	for i, eq := range []string{"半自动咖啡机", "全自动咖啡机", "商用磨豆机", "制冰机", "奶泡机", "净水器", "茶饮机", "萃茶机",
		"真空包装机", "烘焙机"} {
		out = append(out, item{fmt.Sprintf("EQ-%02d", 6+i), eq, "stock", "EQUIP", "C62", "serial", nil})
	}
	for i, sv := range []string{"门店开业支持", "设备年度维保", "饮品研发顾问", "员工上岗培训", "陈列设计", "季度巡店"} {
		unit := "HUR"
		if i%2 == 0 {
			unit = "DAY"
		}
		out = append(out, item{fmt.Sprintf("SVC-%02d", 6+i), sv, "service", "SERVICE", unit, "none", nil})
	}
	for i, su := range []string{"清洁剂", "抹布", "一次性手套", "打印色带", "收银纸", "垃圾袋"} {
		out = append(out, item{fmt.Sprintf("SUP-%02d", 4+i), su, "non_stock", "SUPPLY", "C62", "none", nil})
	}
	return out
}

func main() {
	base := env("MDM_PRODUCT_URL", "http://localhost:18082")
	if !seed.Ready(base) { // probe before pouring
		fail("mdm/product at %s is not ready", base)
	}
	t := access(base)
	c, org := client{seed.NewClient(base, t)}, client{seed.NewClient(env("MDM_ORG_URL", "http://localhost:18083"), t)}
	les := map[string]string{}
	for _, code := range []string{"CN01", "ES01", "US01", "HK01"} {
		les[code] = legalEntity(org, code)
	}
	cats := map[string]string{}
	category := func(code, name, parent string) {
		body := map[string]any{"code": code, "name": prefix + name}
		if parent != "" {
			body["parent_id"] = cats[parent]
		}
		cats[code] = c.must("POST", "/api/product/categories", body)["id"].(string)
	}
	for _, k := range [][2]string{{"TEA", "茶叶"}, {"COFFEE", "咖啡"}, {"EQUIP", "设备"}, {"SERVICE", "服务"}, {"SUPPLY", "办公用品"},
		{"TEAWARE", "茶具"}, {"PASTRY", "茶点"}, {"PACK", "包装材料"}} {
		category(k[0], k[1], "")
	}
	for _, k := range [][3]string{{"TEA-GREEN", "绿茶", "TEA"}, {"TEA-BLACK", "红茶", "TEA"}, {"COFFEE-BEANS", "咖啡豆", "COFFEE"},
		{"COFFEE-CAPSULE", "咖啡胶囊", "COFFEE"}} {
		category(k[0], k[1], k[2])
	}
	all := items()
	ids, states, opened := map[string]string{}, map[string]string{}, map[string]map[string]bool{}
	for _, it := range all {
		body := map[string]any{"code": it.code, "kind": it.kind, "name": prefix + it.name, "base_unit": it.unit,
			"category_id": cats[it.cat], "tracking": it.tracking}
		if len(it.conv) > 0 {
			var conv []map[string]any
			for _, cv := range it.conv {
				conv = append(conv, map[string]any{"unit": cv[0], "factor": cv[1]})
			}
			body["conversions"] = conv
		}
		out := c.must("POST", "/api/product/products", body)
		ids[it.code], states[it.code] = out["id"].(string), out["state"].(string)
		opened[it.code] = map[string]bool{}
		avail, _ := out["availability"].([]any)
		for _, a := range avail {
			opened[it.code][a.(map[string]any)["legal_entity_id"].(string)] = true
		}
	}
	n := 0
	open := func(code, le string, sell, buy bool) {
		n++
		if states[code] != "active" { // suspended or retired by an earlier run, after its availability was set
			return
		}
		c.must("PUT", "/api/product/products/"+ids[code]+"/legal-entities/"+les[le], map[string]any{"sellable": sell, "purchasable": buy})
	}
	for i, it := range all {
		buy := it.kind != "service"
		open(it.code, "CN01", true, buy)
		switch i % 5 {
		case 0: // not opened in ES01 at all (not_opened)
		case 1:
			open(it.code, "ES01", false, true) // bought but not sold in ES01 (not_sellable)
		default:
			open(it.code, "ES01", true, buy)
		}
		switch i % 3 {
		case 0:
			open(it.code, "US01", true, buy)
		case 1:
			if it.kind != "service" {
				open(it.code, "US01", false, true)
			}
		}
	}
	// HK01: ten opened while it was active, then it was suspended (business came before the suspension)
	hk := all[:10]
	var missing []item
	for _, it := range hk {
		if !opened[it.code][les["HK01"]] {
			missing = append(missing, it)
		}
	}
	if len(missing) > 0 {
		org.must("POST", "/api/org/legal-entities/"+les["HK01"]+"/state", map[string]any{"state": "active"})
		for _, it := range missing {
			open(it.code, "HK01", true, true)
			n--
		}
		org.must("POST", "/api/org/legal-entities/"+les["HK01"]+"/state", map[string]any{"state": "suspended"})
	}
	n += len(hk)
	state := func(product, to string) {
		if code, out := c.do("POST", "/api/product/products/"+ids[product]+"/state", map[string]any{"state": to}); code != 200 &&
			!(code == 409 && out["reason"] == "STATE_CONFLICT") {
			fail("%s -> %s: %d %v", product, to, code, out)
		}
	}
	state("TEA-11-BULK", "suspended")
	state("SUP-03", "retired")
	fmt.Printf("seed mdm/product: %d categories, %d products, %d availabilities (10 in the suspended HK01), 1 suspended, 1 retired; "+
		"roles maintain, read\n", len(cats), len(all), n)
}
