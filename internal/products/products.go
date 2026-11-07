package products

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	core "github.com/brickKit/mdm-product/v3/internal/core"
	units "github.com/brickKit/mdm-product/v3/internal/units"

	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/calls"
	"github.com/brickKit/be-sdk-go/v3/canon"
	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/errs"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/ids"
	"github.com/brickKit/be-sdk-go/v3/list"
	"github.com/brickKit/be-sdk-go/v3/search"
	orgv3 "github.com/brickKit/mdm-org/contract/v3/gen/go/be/mdm/org/v3"
)

func searchOf(code, name string, names map[string]string) string {
	vals := []string{code, name}
	for _, n := range names {
		vals = append(vals, n)
	}
	return search.Text(vals...)
}

type productIn struct {
	Version     *int64             `json:"version"`
	Code        string             `json:"code"`
	Kind        string             `json:"kind"`
	Name        *string            `json:"name"`
	Names       *map[string]string `json:"names"`
	CategoryID  json.RawMessage    `json:"category_id"` // absent: unchanged; null: none
	BaseUnit    *string            `json:"base_unit"`
	Tracking    *string            `json:"tracking"`
	Conversions *[]core.Conversion `json:"conversions"`
}

// apply checks what is given against the catalog and the rules, and puts it into p.
func (in productIn) apply(ctx context.Context, tx db.Tx, p *core.Product) error {
	if in.Name != nil {
		if strings.TrimSpace(*in.Name) == "" {
			return core.Invalid("name", "name cannot be empty")
		}
		p.Name = *in.Name
	}
	if in.Names != nil {
		if err := core.CheckNames(*in.Names); err != nil {
			return err
		}
		p.Names = *in.Names
	}
	if len(in.CategoryID) > 0 {
		if string(in.CategoryID) == "null" {
			p.CategoryID = nil
		} else {
			var id string
			if json.Unmarshal(in.CategoryID, &id) != nil || !core.UUIDForm.MatchString(id) {
				return core.Invalid("category_id", "category_id is a category's ID, or null")
			}
			c, err := core.GetCategory(ctx, tx, id, false)
			if errors.Is(err, core.ErrNone) || err == nil && !c.Active {
				return core.Invalid("category_id", "no active category "+id)
			}
			if err != nil {
				return err
			}
			p.CategoryID = &id
		}
	}
	if in.Tracking != nil && *in.Tracking != p.Tracking {
		if *in.Tracking != "none" && *in.Tracking != "batch" && *in.Tracking != "serial" {
			return core.Invalid("tracking", "tracking is none, batch or serial")
		}
		if p.HasMovements {
			return core.ErrMoved.New("the tracking policy of " + p.Code + " no longer changes: create a new product")
		}
		if p.Kind != "stock" && *in.Tracking != "none" {
			return core.Invalid("tracking", "only a stock product is tracked by batch or serial")
		}
		p.Tracking = *in.Tracking
	}
	if in.BaseUnit != nil && *in.BaseUnit != p.BaseUnit {
		if p.HasMovements {
			return core.ErrMoved.New("the base unit of " + p.Code + " no longer changes: create a new product")
		}
		if _, err := units.UnitOf(ctx, tx, *in.BaseUnit); err != nil {
			return err
		}
		p.BaseUnit = *in.BaseUnit
	}
	if in.Conversions != nil {
		seen := map[string]bool{}
		for i, cv := range *in.Conversions {
			field := fmt.Sprintf("conversions[%d]", i)
			if seen[cv.Unit] || cv.Unit == p.BaseUnit {
				return core.Invalid(field+".unit", "one conversion a unit, and not the base unit")
			}
			seen[cv.Unit] = true
			if _, err := units.UnitOf(ctx, tx, cv.Unit); err != nil {
				return err
			}
			f, err := core.Decimal(cv.Factor)
			if err != nil || f.Sign() <= 0 {
				return core.Invalid(field+".factor", "factor is a positive decimal string")
			}
			(*in.Conversions)[i].Factor = core.Canonical(f)
		}
		p.Conversions = *in.Conversions
	}
	return nil
}

// write stores p's columns and replaces its conversions.
func write(ctx context.Context, tx db.Tx, p core.Product, insert bool) error {
	names, _ := json.Marshal(p.Names)
	var err error
	if insert {
		_, err = tx.Exec(ctx, `INSERT INTO product (id, code, kind, name, names, category_id, base_unit, tracking, search,
			search_version) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`, p.ID, p.Code, p.Kind, p.Name, names,
			p.CategoryID, p.BaseUnit, p.Tracking, searchOf(p.Code, p.Name, p.Names), search.Version)
	} else {
		_, err = tx.Exec(ctx, `UPDATE product SET name = $2, names = $3, category_id = $4, base_unit = $5, tracking = $6,
			state = $7, search = $8, search_version = $9, version = version + 1, updated_at = now() WHERE id = $1`, p.ID,
			p.Name, names, p.CategoryID, p.BaseUnit, p.Tracking, p.State, searchOf(p.Code, p.Name, p.Names), search.Version)
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM product_conversion WHERE product_id = $1`, p.ID); err != nil {
		return err
	}
	for _, cv := range p.Conversions {
		if _, err := tx.Exec(ctx, `INSERT INTO product_conversion (product_id, unit, factor) VALUES ($1, $2, $3::numeric)`,
			p.ID, cv.Unit, cv.Factor); err != nil {
			return err
		}
	}
	return nil
}

func CreateProduct(m *core.Base, c *httpx.Context) error {
	ctx, actor, err := core.Check(c, core.OpEdit)
	if err != nil {
		return err
	}
	var in productIn
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	switch {
	case !core.CodeForm.MatchString(in.Code):
		return core.Invalid("code", "code is upper case letters, digits, _, . and -, at most 48")
	case in.Kind != "stock" && in.Kind != "service" && in.Kind != "non_stock":
		return core.Invalid("kind", "kind is stock, service or non_stock")
	case in.Name == nil || in.BaseUnit == nil:
		return core.Invalid("name", "name and base_unit are required")
	}
	var out core.Product
	err = m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		var existing string
		if err := tx.QueryRow(ctx, `SELECT id::text FROM product WHERE code = $1 FOR UPDATE`, in.Code).Scan(&existing); err == nil {
			cur, err := core.GetProduct(ctx, tx, existing, false)
			if err != nil {
				return err
			}
			if cur.Kind == in.Kind && cur.Name == *in.Name && cur.BaseUnit == *in.BaseUnit {
				out = cur // the same creation again returns it
				return nil
			}
			return core.ErrCode.New("product code " + in.Code + " is taken")
		}
		p := core.Product{ID: ids.New().String(), Code: in.Code, Kind: in.Kind, Tracking: "none", State: "active",
			Names: map[string]string{}, Conversions: []core.Conversion{}, BaseUnit: *in.BaseUnit}
		if _, err := units.UnitOf(ctx, tx, p.BaseUnit); err != nil {
			return err
		}
		if err := in.apply(ctx, tx, &p); err != nil {
			return err
		}
		if err := write(ctx, tx, p, true); err != nil {
			return err
		}
		var err error
		if out, err = core.GetProduct(ctx, tx, p.ID, false); err != nil {
			return err
		}
		if err := core.Emit(m, ctx, tx, core.ProductChanged, out.ID, out.Version, out); err != nil {
			return err
		}
		return core.Change(m, ctx, tx, actor, core.OpEdit, core.Products, out.ID, out)
	})
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}

func product(m *core.Base, ctx context.Context, id string) (core.Product, error) {
	if !core.UUIDForm.MatchString(id) {
		return core.Product{}, core.NotFound("product")
	}
	var p core.Product
	err := m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		var err error
		p, err = core.GetProduct(ctx, tx, id, false)
		return err
	})
	if errors.Is(err, core.ErrNone) {
		return p, core.NotFound("product")
	}
	return p, err
}

func GetProduct(m *core.Base, c *httpx.Context) error {
	ctx, _, err := core.Check(c, core.OpRead)
	if err != nil {
		return err
	}
	p, err := product(m, ctx, c.Param("id"))
	if err != nil {
		return err
	}
	return c.JSON(200, p)
}

// changeProduct locks the product, applies the change and writes, publishes and audits it if it changed.
func changeProduct(m *core.Base, c *httpx.Context, version *int64, apply func(ctx context.Context, tx db.Tx, p *core.Product) error) error {
	ctx, actor, err := core.Check(c, core.OpEdit)
	if err != nil {
		return err
	}
	id := c.Param("id")
	if !core.UUIDForm.MatchString(id) {
		return core.NotFound("product")
	}
	var out core.Product
	err = m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		cur, err := core.GetProduct(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if version != nil && *version != cur.Version {
			return core.ErrVersion.New(fmt.Sprintf("the product is at version %d", cur.Version))
		}
		next := cur
		// copies that keep an empty list empty (not nil): a nil one encodes as null and would read as a change
		next.Conversions = slices.Clone(cur.Conversions)
		next.Availability = slices.Clone(cur.Availability)
		if err := apply(ctx, tx, &next); err != nil {
			return err
		}
		if core.Same(cur, next) {
			out = cur
			return nil
		}
		if err := write(ctx, tx, next, false); err != nil {
			return err
		}
		if !core.Same(cur.Availability, next.Availability) {
			for _, a := range next.Availability {
				if _, err := tx.Exec(ctx, `INSERT INTO product_availability (product_id, legal_entity_id, sellable, purchasable)
					VALUES ($1, $2, $3, $4) ON CONFLICT (product_id, legal_entity_id) DO UPDATE
					SET sellable = excluded.sellable, purchasable = excluded.purchasable`, id, a.LegalEntityID, a.Sellable,
					a.Purchasable); err != nil {
					return err
				}
			}
		}
		if out, err = core.GetProduct(ctx, tx, id, false); err != nil {
			return err
		}
		if err := core.Emit(m, ctx, tx, core.ProductChanged, out.ID, out.Version, out); err != nil {
			return err
		}
		return core.Change(m, ctx, tx, actor, core.OpEdit, core.Products, out.ID, out)
	})
	if errors.Is(err, core.ErrNone) {
		return core.NotFound("product")
	}
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}

func UpdateProduct(m *core.Base, c *httpx.Context) error {
	var in productIn
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	if in.Code != "" || in.Kind != "" {
		return core.Invalid("code", "code and kind do not change: another kind of product is another product")
	}
	return changeProduct(m, c, in.Version, func(ctx context.Context, tx db.Tx, p *core.Product) error {
		if p.State == "retired" {
			return core.ErrState.New("a retired product does not change")
		}
		return in.apply(ctx, tx, p)
	})
}

// ProductState moves a product through protocol 07's lifecycle; a retired product stays retired.
func ProductState(m *core.Base, c *httpx.Context) error {
	var in struct {
		State   string `json:"state"`
		Version *int64 `json:"version"`
	}
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	if in.State != "active" && in.State != "suspended" && in.State != "retired" {
		return core.Invalid("state", "state is active, suspended or retired")
	}
	return changeProduct(m, c, in.Version, func(_ context.Context, _ db.Tx, p *core.Product) error {
		if p.State == "retired" && in.State != "retired" {
			return core.ErrState.New("a retired product stays retired")
		}
		p.State = in.State
		return nil
	})
}

// legalEntityActive asks mdm/org now, outside any transaction (protocol 07: unreachable is not "does not exist").
func legalEntityActive(m *core.Base, ctx context.Context, id string) error {
	conn, ok := m.RT.GRPC(core.Org)
	if !ok {
		return errs.New(errs.ReasonDependencyUnavailable, "mdm/org is not reachable: the legal entity cannot be checked")
	}
	res, err := orgv3.NewSourceServiceClient(conn).Read(ctx, &orgv3.ReadRequest{
		Aggregate: orgv3.Aggregate_AGGREGATE_LEGAL_ENTITY, Ids: []string{id}}, calls.Idempotent())
	if err != nil {
		return err
	}
	if len(res.GetResults()) != 1 || res.GetResults()[0].GetState() != orgv3.ReadState_READ_STATE_EXISTS {
		return core.ErrLegalEntity.New("no legal entity " + id)
	}
	var le struct{ State string }
	if err := json.Unmarshal(res.GetResults()[0].GetData(), &le); err != nil {
		return err
	}
	if le.State != "active" {
		return core.ErrLegalEntity.New("the legal entity is " + le.State)
	}
	return nil
}

// PutAvailability opens the product for sale or purchase in a legal entity that mdm/org says is active now.
func PutAvailability(m *core.Base, c *httpx.Context) error {
	le := c.Param("legal_entity_id")
	if !core.UUIDForm.MatchString(le) {
		return core.Invalid("legal_entity_id", "legal_entity_id is a legal entity's ID")
	}
	var in struct {
		Sellable    *bool `json:"sellable"`
		Purchasable *bool `json:"purchasable"`
	}
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	if in.Sellable == nil || in.Purchasable == nil {
		return core.Invalid("sellable", "sellable and purchasable are required")
	}
	if *in.Sellable || *in.Purchasable { // closing needs no proof that the legal entity is usable
		if err := legalEntityActive(m, c.Ctx(), le); err != nil {
			return err
		}
	}
	return changeProduct(m, c, nil, func(_ context.Context, _ db.Tx, p *core.Product) error {
		if p.State != "active" {
			return core.ErrState.New("the product is " + p.State)
		}
		a := core.Availability{LegalEntityID: le, Sellable: *in.Sellable, Purchasable: *in.Purchasable}
		for i := range p.Availability {
			if p.Availability[i].LegalEntityID == le {
				p.Availability[i] = a
				return nil
			}
		}
		p.Availability = append(p.Availability, a)
		core.SortAvailability(p.Availability)
		return nil
	})
}

// The lists of the user surface (protocol 14: cursor pages ordered by code then ID, the filters and keyword declared
// here). Every field is ordinary, so a row is listed whole or not at all.
var productList = list.Spec{Operation: core.OpRead, Alias: "p", IDColumn: "id", IDKind: list.UUID,
	Columns: []list.Column{
		{Field: "code", Column: "code", Kind: list.Text, Sortable: true},
		{Field: "state", Column: "state", Kind: list.Text, Filterable: true},
		{Field: "kind", Column: "kind", Kind: list.Text, Filterable: true},
		{Field: "category_id", Column: "category_id", Kind: list.UUID, Filterable: true}},
	Search: []list.SearchColumn{{Name: "names", Column: "search", Fields: []string{"code", "name", "names"}}}}

func ListProducts(m *core.Base, c *httpx.Context) error {
	limit, err := list.ParseLimit(c.Query("limit"))
	if err != nil {
		return err
	}
	if v := c.Query("category_id"); v != "" && !core.UUIDForm.MatchString(v) {
		return core.Invalid("category_id", "category_id is a category's ID")
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
	out, next, err := list.Page(ctx, m.RT.DB, q, "product", core.ProductCols, core.ScanProduct,
		func(p core.Product) ([]string, string) { return []string{p.Code}, p.ID })
	if err != nil {
		return err
	}
	return c.JSON(200, map[string]any{"products": out, "next_cursor": next})
}

// Eligibility is protocol 07's evidence for a new reference to a product.
type Eligibility struct {
	Eligible       bool   `json:"eligible"`
	Reason         string `json:"reason"`
	ProductVersion int64  `json:"product_version,omitempty"`
	Stocked        bool   `json:"stocked"`
	EvaluatedAt    string `json:"evaluated_at"`
}

func EligibilityOf(m *core.Base, ctx context.Context, productID, legalEntity, purpose string) (Eligibility, error) {
	if purpose != "sell" && purpose != "purchase" {
		return Eligibility{}, core.Invalid("purpose", "purpose is sell or purchase")
	}
	e := Eligibility{EvaluatedAt: canon.Time(time.Now())}
	if !core.UUIDForm.MatchString(productID) || !core.UUIDForm.MatchString(legalEntity) {
		e.Reason = "not_found"
		return e, nil
	}
	err := m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		p, err := core.GetProduct(ctx, tx, productID, false)
		if errors.Is(err, core.ErrNone) {
			e.Reason = "not_found"
			return nil
		}
		if err != nil {
			return err
		}
		e.ProductVersion, e.Stocked = p.Version, p.Stocked
		if p.State != "active" {
			e.Reason = "product_" + p.State
			return nil
		}
		for _, a := range p.Availability {
			if a.LegalEntityID != legalEntity {
				continue
			}
			switch {
			case purpose == "sell" && !a.Sellable:
				e.Reason = "not_sellable"
			case purpose == "purchase" && !a.Purchasable:
				e.Reason = "not_purchasable"
			default:
				e.Eligible, e.Reason = true, "eligible"
			}
			return nil
		}
		e.Reason = "not_opened"
		return nil
	})
	return e, err
}

func EligibilityHTTP(m *core.Base, c *httpx.Context) error {
	ctx, _, err := core.Check(c, core.OpRead)
	if err != nil {
		return err
	}
	e, err := EligibilityOf(m, ctx, c.Query("product_id"), c.Query("legal_entity_id"), c.Query("purpose"))
	if err != nil {
		return err
	}
	return c.JSON(200, e)
}
