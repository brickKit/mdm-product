package product

import (
	"github.com/brickKit/be-sdk-go/v3/grpcx"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/runtime"
	productv3 "github.com/brickKit/mdm-product/contract/v3/gen/go/be/mdm/product/v3"
	categories "github.com/brickKit/mdm-product/v3/internal/categories"
	core "github.com/brickKit/mdm-product/v3/internal/core"
	products "github.com/brickKit/mdm-product/v3/internal/products"
	units "github.com/brickKit/mdm-product/v3/internal/units"
)

// Module is the product component as the launcher and the shells run it: it only assembles the business modules
// under internal/ (their map is internal/AGENTS.md); the gRPC services answer through them (services.go).
type Module struct {
	productv3.UnimplementedSourceServiceServer
	productv3.UnimplementedProductServiceServer
	b *core.Base
}

// New builds the module from its runtime.
func New(rt *runtime.Runtime) (*Module, error) {
	return &Module{b: &core.Base{RT: rt, Deployment: rt.Config.String("DEPLOYMENT_ID"),
		Instance: rt.Config.String("IMPLEMENTATION_INSTANCE")}}, nil
}

// Routes registers the user surface (contract/openapi.yaml).
func (m *Module) Routes(r *httpx.Routes) error {
	add := func(method, path, op string, h httpx.Handler) {
		r.Add(httpx.Route{Method: method, Path: path, Operation: op, Handle: h})
	}
	h := m.handler
	add("GET", "/api/product/products", core.OpRead, h(products.ListProducts))
	add("POST", "/api/product/products", core.OpEdit, h(products.CreateProduct))
	add("GET", "/api/product/products/:id", core.OpRead, h(products.GetProduct))
	add("PATCH", "/api/product/products/:id", core.OpEdit, h(products.UpdateProduct))
	add("POST", "/api/product/products/:id/state", core.OpEdit, h(products.ProductState))
	add("PUT", "/api/product/products/:id/legal-entities/:legal_entity_id", core.OpEdit, h(products.PutAvailability))
	add("GET", "/api/product/products/:id/convert", core.OpRead, h(units.ConvertHTTP))
	add("GET", "/api/product/eligibility", core.OpRead, h(products.EligibilityHTTP))
	add("GET", "/api/product/units", core.OpRead, h(units.ListUnits))
	add("GET", "/api/product/categories", core.OpRead, h(categories.ListCategories))
	add("POST", "/api/product/categories", core.OpCategory, h(categories.CreateCategory))
	add("PATCH", "/api/product/categories/:id", core.OpCategory, h(categories.UpdateCategory))
	// in the contract, not in step 6 (D75)
	add("GET", "/api/product/products/:id/barcodes", core.OpRead, core.Unsupported)
	add("POST", "/api/product/products/:id/barcodes", core.OpEdit, core.Unsupported)
	add("POST", "/api/product/products/:id/images", core.OpEdit, core.Unsupported)
	return nil
}

// Services registers the system surface: every component of the deployment resolves products.
func (m *Module) Services(s *grpcx.Services) error {
	s.Register(&productv3.SourceService_ServiceDesc, m, map[string]grpcx.Method{
		"Snapshot": {Operation: core.OpSystem, Callers: []string{grpcx.AnyComponent}},
		"Read":     {Operation: core.OpSystem, Callers: []string{grpcx.AnyComponent}}})
	s.Register(&productv3.ProductService_ServiceDesc, m, map[string]grpcx.Method{
		"CheckEligibility": {Operation: core.OpSystem, Callers: []string{grpcx.AnyComponent}},
		"Resolve":          {Operation: core.OpSystem, Callers: []string{grpcx.AnyComponent}},
		"Convert":          {Operation: core.OpSystem, Callers: []string{grpcx.AnyComponent}}})
	return nil
}

// handler adapts a module's handler, which takes the shared base first, to what the SDK registers.
func (m *Module) handler(f func(*core.Base, *httpx.Context) error) httpx.Handler {
	return func(c *httpx.Context) error { return f(m.b, c) }
}
