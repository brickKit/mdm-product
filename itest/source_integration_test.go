//go:build integration

package itest

import (
	productv3 "github.com/brickKit/mdm-product/contract/v3/gen/go/be/mdm/product/v3"
	core "github.com/brickKit/mdm-product/v3/internal/core"
	assert "github.com/stretchr/testify/assert"
	require "github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// source: the system surface over mTLS: eligibility, conversion, source reads.
func (fx *fixture) source() {
	t, ctx := fx.t, fx.ctx
	// the system surface over mTLS
	conn, err := grpc.NewClient(itSystem, grpc.WithTransportCredentials(credentials.NewTLS(fx.w.TLS(t, "erp/sales").ClientConfig(core.ID))))
	require.NoError(t, err)
	defer conn.Close()
	ps := productv3.NewProductServiceClient(conn)
	e, err := ps.CheckEligibility(ctx, &productv3.CheckEligibilityRequest{ProductId: fx.svc["id"].(string), LegalEntityId: fx.leA,
		Purpose: productv3.Purpose_PURPOSE_SELL})
	require.NoError(t, err)
	assert.False(t, e.Stocked)
	assert.Equal(t, "not_opened", e.Reason)
	c2, err := ps.Convert(ctx, &productv3.ConvertRequest{ProductId: fx.id, Quantity: "2", FromUnit: "BX", ToUnit: "C62"})
	require.NoError(t, err)
	assert.Equal(t, "24", c2.Quantity)
	read, err := productv3.NewSourceServiceClient(conn).Read(ctx, &productv3.ReadRequest{Aggregate: productv3.Aggregate_AGGREGATE_PRODUCT,
		Ids: []string{fx.id}})
	require.NoError(t, err)
	assert.Contains(t, string(read.Results[0].Data), `"has_movements":true`)
	assert.Len(t, read.Stamp.Positions, 4)
}
