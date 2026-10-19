package product

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/brickKit/be-sdk-go/v3/audit"
	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/calls"
	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/errs"
	"github.com/brickKit/be-sdk-go/v3/grpcx"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/identity"
	"github.com/brickKit/be-sdk-go/v3/ids"
	"github.com/brickKit/be-sdk-go/v3/runtime"
	"github.com/brickKit/be-sdk-go/v3/search"
	orgv3 "github.com/brickKit/mdm-org/contract/v3/gen/go/be/mdm/org/v3"
	productv3 "github.com/brickKit/mdm-product/contract/v3/gen/go/be/mdm/product/v3"
)

// The component's own reasons (the SDK's catalog has the shared ones).
var (
	errVersion = errs.Define(ID, "VERSION_CONFLICT", errs.Aborted, 409, errs.RetryNo, "The record changed since that version")
	errCode    = errs.Define(ID, "CODE_TAKEN", errs.AlreadyExists, 409, errs.RetryNo, "The code belongs to another record")
	errState   = errs.Define(ID, "STATE_CONFLICT", errs.FailedPrecondition, 409, errs.RetryNo,
		"The record's state does not allow this")
	errMoved = errs.Define(ID, "HAS_MOVEMENTS", errs.FailedPrecondition, 409, errs.RetryNo,
		"Inventory has movements of the product: its tracking and base unit no longer change")
	errLegalEntity = errs.Define(ID, "LEGAL_ENTITY_NOT_USABLE", errs.FailedPrecondition, 409, errs.RetryNo,
		"The legal entity does not exist or is not active")
	errUnit = errs.Define(ID, "NO_CONVERSION", errs.FailedPrecondition, 409, errs.RetryNo,
		"No conversion between these units for this product")
)

var (
	codeForm = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_.-]{0,47}$`)
	uuidForm = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// Module is the product component.
type Module struct {
	productv3.UnimplementedSourceServiceServer
	productv3.UnimplementedProductServiceServer
	rt                   *runtime.Runtime
	deployment, instance string
}

// New builds the module from its runtime.
func New(rt *runtime.Runtime) (*Module, error) {
	return &Module{rt: rt, deployment: rt.Config.String("DEPLOYMENT_ID"), instance: rt.Config.String("IMPLEMENTATION_INSTANCE")}, nil
}

// Routes registers the user surface (contract/openapi.yaml).
func (m *Module) Routes(r *httpx.Routes) error {
	add := func(method, path, op string, h httpx.Handler) {
		r.Add(httpx.Route{Method: method, Path: path, Operation: op, Handle: h})
	}
	add("GET", "/api/product/products", OpRead, m.listProducts)
	add("POST", "/api/product/products", OpEdit, m.createProduct)
	add("GET", "/api/product/products/:id", OpRead, m.getProduct)
	add("PATCH", "/api/product/products/:id", OpEdit, m.updateProduct)
	add("POST", "/api/product/products/:id/state", OpEdit, m.productState)
	add("PUT", "/api/product/products/:id/legal-entities/:legal_entity_id", OpEdit, m.putAvailability)
	add("GET", "/api/product/products/:id/convert", OpRead, m.convertHTTP)
	add("GET", "/api/product/eligibility", OpRead, m.eligibilityHTTP)
	add("GET", "/api/product/units", OpRead, m.listUnits)
	add("GET", "/api/product/categories", OpRead, m.listCategories)
	add("POST", "/api/product/categories", OpCategory, m.createCategory)
	add("PATCH", "/api/product/categories/:id", OpCategory, m.updateCategory)
	// in the contract, not in step 6 (D75)
	add("GET", "/api/product/products/:id/barcodes", OpRead, unsupported)
	add("POST", "/api/product/products/:id/barcodes", OpEdit, unsupported)
	add("POST", "/api/product/products/:id/images", OpEdit, unsupported)
	return nil
}

func unsupported(c *httpx.Context) error {
	if _, _, err := check(c, c.Operation()); err != nil {
		return err
	}
	return errs.New(errs.ReasonCapabilityUnsupported, "not in this version of mdm/product (barcodes and images come later)")
}

// Services registers the system surface: every component of the deployment reads products.
func (m *Module) Services(s *grpcx.Services) error {
	s.Register(&productv3.SourceService_ServiceDesc, m, map[string]grpcx.Method{
		"Snapshot": {Operation: OpSystem, Callers: []string{grpcx.AnyComponent}},
		"Read":     {Operation: OpSystem, Callers: []string{grpcx.AnyComponent}}})
	s.Register(&productv3.ProductService_ServiceDesc, m, map[string]grpcx.Method{
		"CheckEligibility": {Operation: OpSystem, Callers: []string{grpcx.AnyComponent}},
		"Resolve":          {Operation: OpSystem, Callers: []string{grpcx.AnyComponent}},
		"Convert":          {Operation: OpSystem, Callers: []string{grpcx.AnyComponent}}})
	return nil
}

// check admits the request's subject to op and returns the context to work in and the acting principal.
func check(c *httpx.Context, op string) (context.Context, string, error) {
	s, _ := sdkauthz.From(c.Ctx())
	ctx, _, err := s.Check(c.Ctx(), op, sdkauthz.Facts{})
	if err != nil {
		return ctx, "", err
	}
	u, _ := identity.UserFrom(ctx)
	return ctx, u.Subject, nil
}

func invalid(field, why string) error {
	return errs.New(errs.ReasonRequestInvalid, why).WithViolations(errs.Violation{Field: field, Reason: "INVALID", Description: why})
}

func notFound(what string) error { return errs.New(errs.ReasonNotFound, what+" not found") }

// change records the business change with the record's new state (protocol 03), in the transaction that made it.
func (m *Module) change(ctx context.Context, tx db.Tx, actor, op, typ, id string, after any) error {
	_, err := m.rt.Audit.Write(ctx, tx, audit.Record{Category: audit.BusinessChange, Operation: op, Outcome: audit.Succeeded,
		Actor: actor, Target: map[string]any{"type": typ, "id": id}, Changes: map[string]any{"after": after}})
	return err
}

func same(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

func checkNames(names map[string]string) error {
	for tag, n := range names {
		if tag == "" || len(tag) > 35 || strings.TrimSpace(n) == "" {
			return invalid("names", "names maps a language tag to a non-empty name")
		}
	}
	return nil
}

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
	Conversions *[]Conversion      `json:"conversions"`
}

// apply checks what is given against the catalog and the rules, and puts it into p.
func (in productIn) apply(ctx context.Context, tx db.Tx, p *Product) error {
	if in.Name != nil {
		if strings.TrimSpace(*in.Name) == "" {
			return invalid("name", "name cannot be empty")
		}
		p.Name = *in.Name
	}
	if in.Names != nil {
		if err := checkNames(*in.Names); err != nil {
			return err
		}
		p.Names = *in.Names
	}
	if len(in.CategoryID) > 0 {
		if string(in.CategoryID) == "null" {
			p.CategoryID = nil
		} else {
			var id string
			if json.Unmarshal(in.CategoryID, &id) != nil || !uuidForm.MatchString(id) {
				return invalid("category_id", "category_id is a category's ID, or null")
			}
			c, err := getCategory(ctx, tx, id, false)
			if errors.Is(err, errNone) || err == nil && !c.Active {
				return invalid("category_id", "no active category "+id)
			}
			if err != nil {
				return err
			}
			p.CategoryID = &id
		}
	}
	if in.Tracking != nil && *in.Tracking != p.Tracking {
		if *in.Tracking != "none" && *in.Tracking != "batch" && *in.Tracking != "serial" {
			return invalid("tracking", "tracking is none, batch or serial")
		}
		if p.HasMovements {
			return errMoved.New("the tracking policy of " + p.Code + " no longer changes: create a new product")
		}
		if p.Kind != "stock" && *in.Tracking != "none" {
			return invalid("tracking", "only a stock product is tracked by batch or serial")
		}
		p.Tracking = *in.Tracking
	}
	if in.BaseUnit != nil && *in.BaseUnit != p.BaseUnit {
		if p.HasMovements {
			return errMoved.New("the base unit of " + p.Code + " no longer changes: create a new product")
		}
		if _, err := unitOf(ctx, tx, *in.BaseUnit); err != nil {
			return err
		}
		p.BaseUnit = *in.BaseUnit
	}
	if in.Conversions != nil {
		seen := map[string]bool{}
		for i, cv := range *in.Conversions {
			field := fmt.Sprintf("conversions[%d]", i)
			if seen[cv.Unit] || cv.Unit == p.BaseUnit {
				return invalid(field+".unit", "one conversion a unit, and not the base unit")
			}
			seen[cv.Unit] = true
			if _, err := unitOf(ctx, tx, cv.Unit); err != nil {
				return err
			}
			f, err := decimal(cv.Factor)
			if err != nil || f.Sign() <= 0 {
				return invalid(field+".factor", "factor is a positive decimal string")
			}
			(*in.Conversions)[i].Factor = canonical(f)
		}
		p.Conversions = *in.Conversions
	}
	return nil
}

// write stores p's columns and replaces its conversions.
func write(ctx context.Context, tx db.Tx, p Product, insert bool) error {
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

func (m *Module) createProduct(c *httpx.Context) error {
	ctx, actor, err := check(c, OpEdit)
	if err != nil {
		return err
	}
	var in productIn
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	switch {
	case !codeForm.MatchString(in.Code):
		return invalid("code", "code is upper case letters, digits, _, . and -, at most 48")
	case in.Kind != "stock" && in.Kind != "service" && in.Kind != "non_stock":
		return invalid("kind", "kind is stock, service or non_stock")
	case in.Name == nil || in.BaseUnit == nil:
		return invalid("name", "name and base_unit are required")
	}
	var out Product
	err = m.rt.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		var existing string
		if err := tx.QueryRow(ctx, `SELECT id::text FROM product WHERE code = $1 FOR UPDATE`, in.Code).Scan(&existing); err == nil {
			cur, err := getProduct(ctx, tx, existing, false)
			if err != nil {
				return err
			}
			if cur.Kind == in.Kind && cur.Name == *in.Name && cur.BaseUnit == *in.BaseUnit {
				out = cur // the same creation again returns it
				return nil
			}
			return errCode.New("product code " + in.Code + " is taken")
		}
		p := Product{ID: ids.New().String(), Code: in.Code, Kind: in.Kind, Tracking: "none", State: "active",
			Names: map[string]string{}, Conversions: []Conversion{}, BaseUnit: *in.BaseUnit}
		if _, err := unitOf(ctx, tx, p.BaseUnit); err != nil {
			return err
		}
		if err := in.apply(ctx, tx, &p); err != nil {
			return err
		}
		if err := write(ctx, tx, p, true); err != nil {
			return err
		}
		var err error
		if out, err = getProduct(ctx, tx, p.ID, false); err != nil {
			return err
		}
		if err := m.emit(ctx, tx, ProductChanged, out.ID, out.Version, out); err != nil {
			return err
		}
		return m.change(ctx, tx, actor, OpEdit, Products, out.ID, out)
	})
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}

func (m *Module) product(ctx context.Context, id string) (Product, error) {
	if !uuidForm.MatchString(id) {
		return Product{}, notFound("product")
	}
	var p Product
	err := m.rt.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		var err error
		p, err = getProduct(ctx, tx, id, false)
		return err
	})
	if errors.Is(err, errNone) {
		return p, notFound("product")
	}
	return p, err
}

func (m *Module) getProduct(c *httpx.Context) error {
	ctx, _, err := check(c, OpRead)
	if err != nil {
		return err
	}
	p, err := m.product(ctx, c.Param("id"))
	if err != nil {
		return err
	}
	return c.JSON(200, p)
}

// changeProduct locks the product, applies the change and writes, publishes and audits it if it changed.
func (m *Module) changeProduct(c *httpx.Context, version *int64, apply func(ctx context.Context, tx db.Tx, p *Product) error) error {
	ctx, actor, err := check(c, OpEdit)
	if err != nil {
		return err
	}
	id := c.Param("id")
	if !uuidForm.MatchString(id) {
		return notFound("product")
	}
	var out Product
	err = m.rt.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		cur, err := getProduct(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if version != nil && *version != cur.Version {
			return errVersion.New(fmt.Sprintf("the product is at version %d", cur.Version))
		}
		next := cur
		next.Conversions = append([]Conversion(nil), cur.Conversions...)
		next.Availability = append([]Availability(nil), cur.Availability...)
		if err := apply(ctx, tx, &next); err != nil {
			return err
		}
		if same(cur, next) {
			out = cur
			return nil
		}
		if err := write(ctx, tx, next, false); err != nil {
			return err
		}
		if !same(cur.Availability, next.Availability) {
			for _, a := range next.Availability {
				if _, err := tx.Exec(ctx, `INSERT INTO product_availability (product_id, legal_entity_id, sellable, purchasable)
					VALUES ($1, $2, $3, $4) ON CONFLICT (product_id, legal_entity_id) DO UPDATE
					SET sellable = excluded.sellable, purchasable = excluded.purchasable`, id, a.LegalEntityID, a.Sellable,
					a.Purchasable); err != nil {
					return err
				}
			}
		}
		if out, err = getProduct(ctx, tx, id, false); err != nil {
			return err
		}
		if err := m.emit(ctx, tx, ProductChanged, out.ID, out.Version, out); err != nil {
			return err
		}
		return m.change(ctx, tx, actor, OpEdit, Products, out.ID, out)
	})
	if errors.Is(err, errNone) {
		return notFound("product")
	}
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}

func (m *Module) updateProduct(c *httpx.Context) error {
	var in productIn
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	if in.Code != "" || in.Kind != "" {
		return invalid("code", "code and kind do not change: another kind of product is another product")
	}
	return m.changeProduct(c, in.Version, func(ctx context.Context, tx db.Tx, p *Product) error {
		if p.State == "retired" {
			return errState.New("a retired product does not change")
		}
		return in.apply(ctx, tx, p)
	})
}

// productState moves a product through protocol 07's lifecycle; a retired product stays retired.
func (m *Module) productState(c *httpx.Context) error {
	var in struct {
		State   string `json:"state"`
		Version *int64 `json:"version"`
	}
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	if in.State != "active" && in.State != "suspended" && in.State != "retired" {
		return invalid("state", "state is active, suspended or retired")
	}
	return m.changeProduct(c, in.Version, func(_ context.Context, _ db.Tx, p *Product) error {
		if p.State == "retired" && in.State != "retired" {
			return errState.New("a retired product stays retired")
		}
		p.State = in.State
		return nil
	})
}

// legalEntityActive asks mdm/org now, outside any transaction (protocol 07: unreachable is not "does not exist").
func (m *Module) legalEntityActive(ctx context.Context, id string) error {
	conn, ok := m.rt.GRPC(Org)
	if !ok {
		return errs.New(errs.ReasonDependencyUnavailable, "mdm/org is not reachable: the legal entity cannot be checked")
	}
	res, err := orgv3.NewSourceServiceClient(conn).Read(ctx, &orgv3.ReadRequest{
		Aggregate: orgv3.Aggregate_AGGREGATE_LEGAL_ENTITY, Ids: []string{id}}, calls.Idempotent())
	if err != nil {
		return err
	}
	if len(res.GetResults()) != 1 || res.GetResults()[0].GetState() != orgv3.ReadState_READ_STATE_EXISTS {
		return errLegalEntity.New("no legal entity " + id)
	}
	var le struct{ State string }
	if err := json.Unmarshal(res.GetResults()[0].GetData(), &le); err != nil {
		return err
	}
	if le.State != "active" {
		return errLegalEntity.New("the legal entity is " + le.State)
	}
	return nil
}

// putAvailability opens the product for sale or purchase in a legal entity that mdm/org says is active now.
func (m *Module) putAvailability(c *httpx.Context) error {
	le := c.Param("legal_entity_id")
	if !uuidForm.MatchString(le) {
		return invalid("legal_entity_id", "legal_entity_id is a legal entity's ID")
	}
	var in struct {
		Sellable    *bool `json:"sellable"`
		Purchasable *bool `json:"purchasable"`
	}
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	if in.Sellable == nil || in.Purchasable == nil {
		return invalid("sellable", "sellable and purchasable are required")
	}
	if *in.Sellable || *in.Purchasable { // closing needs no proof that the legal entity is usable
		if err := m.legalEntityActive(c.Ctx(), le); err != nil {
			return err
		}
	}
	return m.changeProduct(c, nil, func(_ context.Context, _ db.Tx, p *Product) error {
		if p.State != "active" {
			return errState.New("the product is " + p.State)
		}
		a := Availability{LegalEntityID: le, Sellable: *in.Sellable, Purchasable: *in.Purchasable}
		for i := range p.Availability {
			if p.Availability[i].LegalEntityID == le {
				p.Availability[i] = a
				return nil
			}
		}
		p.Availability = append(p.Availability, a)
		sortAvailability(p.Availability)
		return nil
	})
}
