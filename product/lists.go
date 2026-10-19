package product

import (
	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/list"
)

// The lists of the user surface (protocol 14: cursor pages ordered by code then ID, the filters and keyword declared
// here). Every field is ordinary, so a row is listed whole or not at all.
var (
	productList = list.Spec{Operation: OpRead, Alias: "p", IDColumn: "id", IDKind: list.UUID,
		Columns: []list.Column{
			{Field: "code", Column: "code", Kind: list.Text, Sortable: true},
			{Field: "state", Column: "state", Kind: list.Text, Filterable: true},
			{Field: "kind", Column: "kind", Kind: list.Text, Filterable: true},
			{Field: "category_id", Column: "category_id", Kind: list.UUID, Filterable: true}},
		Search: []list.SearchColumn{{Name: "names", Column: "search", Fields: []string{"code", "name", "names"}}}}
	categoryList = list.Spec{Operation: OpRead, Alias: "c", IDColumn: "id", IDKind: list.UUID,
		Columns: []list.Column{{Field: "code", Column: "code", Kind: list.Text, Sortable: true}}}
)

func (m *Module) listProducts(c *httpx.Context) error {
	limit, err := list.ParseLimit(c.Query("limit"))
	if err != nil {
		return err
	}
	if v := c.Query("category_id"); v != "" && !uuidForm.MatchString(v) {
		return invalid("category_id", "category_id is a category's ID")
	}
	var f []list.Filter
	for _, k := range []string{"state", "kind", "category_id"} {
		if v := c.Query(k); v != "" {
			f = append(f, list.Filter{Field: k, Op: "eq", Values: []string{v}})
		}
	}
	s, _ := sdkauthz.From(c.Ctx())
	ctx, q, err := list.Build(c.Ctx(), s, productList, list.Request{Filters: f, Q: c.Query("q"), Limit: limit,
		Cursor: c.Query("cursor"), Sort: []list.Order{{Field: "code"}}})
	if err != nil {
		return err
	}
	out, next, err := list.Page(ctx, m.rt.DB, q, "product", productCols, scanProduct,
		func(p Product) ([]string, string) { return []string{p.Code}, p.ID })
	if err != nil {
		return err
	}
	return c.JSON(200, map[string]any{"products": out, "next_cursor": next})
}

func (m *Module) listCategories(c *httpx.Context) error {
	limit, err := list.ParseLimit(c.Query("limit"))
	if err != nil {
		return err
	}
	s, _ := sdkauthz.From(c.Ctx())
	ctx, q, err := list.Build(c.Ctx(), s, categoryList, list.Request{Limit: limit, Cursor: c.Query("cursor"),
		Sort: []list.Order{{Field: "code"}}})
	if err != nil {
		return err
	}
	out, next, err := list.Page(ctx, m.rt.DB, q, "category", categoryCols, scanCategory,
		func(v Category) ([]string, string) { return []string{v.Code}, v.ID })
	if err != nil {
		return err
	}
	return c.JSON(200, map[string]any{"categories": out, "next_cursor": next})
}
