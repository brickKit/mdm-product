// Package product is mdm/product (boundary card mdm/product): products (stock, service, non-stock) with categories,
// the unit catalog and the product's own conversions, the tracking policy, and availability for sale and purchase by
// legal entity; the eligibility a new document needs, batch resolution and unit conversion; protocol 02's source
// methods and full-state events. Step 6 of the representative deployment experiment: barcodes and images answer
// CAPABILITY_UNSUPPORTED (D75); "inventory has movements" is a flag that step 7 sets from inventory's events.
package product

import (
	"embed"
	"io/fs"
	"time"

	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/component"
	"github.com/brickKit/be-sdk-go/v3/events"
	"github.com/brickKit/be-sdk-go/v3/runtime"
)

// Identity of the component.
const (
	ID       = "mdm/product"
	Version  = "3.0.10"
	GRPCPort = 9092
	// Org is the component the legal entities belong to: a required dependency.
	Org = "mdm/org"
)

// The stream and its event types (contract/events).
const (
	Stream          = "mdm.product"
	ProductChanged  = "mdm.product.changed.v1"
	CategoryChanged = "mdm.product_category.changed.v1"
)

// Resources and operations (contract/openapi.yaml names each route's operation).
const (
	Products   = "mdm.product"
	OpRead     = "mdm.product.read"
	OpEdit     = "mdm.product.edit"
	Categories = "mdm.product_category"
	OpCatRead  = "mdm.product_category.read"
	OpCategory = "mdm.product_category.manage"
	System     = "mdm.product.system"
	OpSystem   = "mdm.product.system.read"
)

// Limits.
const (
	MaxBatch = 500              // IDs a Read or Resolve takes, records a Snapshot page gives (decision 0304)
	W        = 30 * time.Second // how long an eligibility answer is evidence (protocol 07)
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func migrations() fs.FS {
	sub, _ := fs.Sub(migrationFiles, "migrations")
	return sub
}

// Definitions are the component's resources and operations. No dimensions in step 6, declared as such.
func Definitions() *sdkauthz.Definitions {
	none := map[string]sdkauthz.Dimension{}
	fields := func(names ...string) map[string]sdkauthz.Field {
		out := map[string]sdkauthz.Field{}
		for _, n := range names {
			out[n] = sdkauthz.Field{Class: sdkauthz.Ordinary}
		}
		return out
	}
	return &sdkauthz.Definitions{Component: ID,
		Resources: []sdkauthz.Resource{
			{Type: Products, ViewOperation: OpRead, Dimensions: none, Fields: fields("id", "code", "kind", "stocked", "name",
				"names", "category_id", "base_unit", "tracking", "has_movements", "conversions", "availability", "state",
				"version", "updated_at")},
			{Type: Categories, ViewOperation: OpCatRead, Dimensions: none, Fields: fields("id", "code", "name", "names",
				"parent_id", "active", "version", "updated_at")},
			{Type: System, ViewOperation: OpSystem, Dimensions: none, Fields: map[string]sdkauthz.Field{}},
		},
		Operations: []sdkauthz.Operation{
			{Key: OpRead, ResourceType: Products, Purpose: "read", RiskClass: "low"},
			{Key: OpEdit, ResourceType: Products, Purpose: "manage", RiskClass: "low", SideEffects: []string{"business_write"}, Audit: true},
			{Key: OpCatRead, ResourceType: Categories, Purpose: "read", RiskClass: "low"},
			{Key: OpCategory, ResourceType: Categories, Purpose: "manage", RiskClass: "low", SideEffects: []string{"business_write"}, Audit: true},
			{Key: OpSystem, ResourceType: System, Purpose: "read", RiskClass: "low"},
		}}
}

// Definition is the component as the launcher and the shells take it.
func Definition() component.Definition {
	return component.Definition{ID: ID, Version: Version, Migrations: migrations(), GRPCPort: GRPCPort,
		Streams: []events.Stream{{Name: Stream, Partitions: 4}}, Authz: Definitions(),
		New: func(rt *runtime.Runtime) (component.Module, error) { return New(rt) }}
}
