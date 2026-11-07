package core

import (
	"time"

	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
)

// ID is the component's ID; Org is the dependency availability checks legal entities with.
const (
	ID  = "mdm/product"
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
	// OpResend is the redelivery of this component's stream (be-protocol P6.38: every component with streams serves
	// it; any component of the deployment may call it).
	OpResend = "mdm.product.system.resend"
	// OpRecover is a full recovery's checks (be-protocol chapter 14: the recovery tool only).
	OpRecover = "mdm.product.system.recover"
	OpSystem  = "mdm.product.system.read"
)

// Limits.
const (
	MaxBatch = 500              // IDs a Read or Resolve takes, records a Snapshot page gives (decision 0304)
	W        = 30 * time.Second // how long an eligibility answer is evidence (protocol 07)
)

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
			{Key: OpResend, ResourceType: System, Purpose: "read", RiskClass: "low"},
			{Key: OpRecover, ResourceType: System, Purpose: "read", RiskClass: "low"},
		}}
}
