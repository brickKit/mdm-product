// Package product is mdm/product (boundary card mdm/product): products (stock, service, non-stock) with categories,
// the unit catalog and the product's own conversions, the tracking policy, and availability for sale and purchase by
// legal entity; the eligibility a new document needs, batch resolution and unit conversion; protocol 02's source
// methods and full-state events. Step 6 of the representative deployment experiment: barcodes and images answer
// CAPABILITY_UNSUPPORTED (D75); "inventory has movements" is a flag that step 7 sets from inventory's events.
package product

import (
	"embed"
	"io/fs"

	core "github.com/brickKit/mdm-product/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/component"
	"github.com/brickKit/be-sdk-go/v3/events"
	"github.com/brickKit/be-sdk-go/v3/runtime"
)

// Identity of the component.
const (
	Version  = "3.0.32"
	GRPCPort = 9092
	// Org is the component the legal entities belong to: a required dependency.
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func migrations() fs.FS {
	sub, _ := fs.Sub(migrationFiles, "migrations")
	return sub
}

// Definition is the component as the launcher and the shells take it.
func Definition() component.Definition {
	return component.Definition{ID: core.ID, Version: Version, Migrations: migrations(), GRPCPort: GRPCPort,
		Streams: []events.Stream{{Name: core.Stream, Partitions: 4}}, Authz: core.Definitions(), Resend: core.OpResend, Recovery: core.OpRecover,
		New: func(rt *runtime.Runtime) (component.Module, error) { return New(rt) }}
}
