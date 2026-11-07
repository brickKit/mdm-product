package product

import (
	"context"

	productv3 "github.com/brickKit/mdm-product/contract/v3/gen/go/be/mdm/product/v3"
	source "github.com/brickKit/mdm-product/v3/internal/source"
)

// The gRPC methods of the system surface answer through internal/source.

// Snapshot is SourceService.Snapshot.
func (m *Module) Snapshot(ctx context.Context, in *productv3.SnapshotRequest) (*productv3.SnapshotResponse, error) {
	return source.Snapshot(m.b, ctx, in)
}

// Read is SourceService.Read.
func (m *Module) Read(ctx context.Context, in *productv3.ReadRequest) (*productv3.ReadResponse, error) {
	return source.Read(m.b, ctx, in)
}

// CheckEligibility is ProductService.CheckEligibility.
func (m *Module) CheckEligibility(ctx context.Context, in *productv3.CheckEligibilityRequest) (*productv3.CheckEligibilityResponse, error) {
	return source.CheckEligibility(m.b, ctx, in)
}

// Resolve is ProductService.Resolve.
func (m *Module) Resolve(ctx context.Context, in *productv3.ResolveRequest) (*productv3.ResolveResponse, error) {
	return source.Resolve(m.b, ctx, in)
}

// Convert is ProductService.Convert.
func (m *Module) Convert(ctx context.Context, in *productv3.ConvertRequest) (*productv3.ConvertResponse, error) {
	return source.Convert(m.b, ctx, in)
}
