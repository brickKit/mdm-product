//go:build integration

package itest

import (
	"github.com/brickKit/be-sdk-go/v3/ids"
	assert "github.com/stretchr/testify/assert"
	require "github.com/stretchr/testify/require"
)

// availability: by legal entity (mdm/org says whether it is usable) and the eligibility a sales order needs.
func (fx *fixture) availability() {
	t := fx.t
	// availability by legal entity and the eligibility a sales order needs
	code, av := fx.alice.do("PUT", "/api/product/products/"+fx.id+"/legal-entities/"+fx.leA, map[string]any{"sellable": true, "purchasable": false})
	require.Equal(t, 200, code, av)
	code, bad := fx.alice.do("PUT", "/api/product/products/"+fx.id+"/legal-entities/"+ids.New().String(), map[string]any{"sellable": true, "purchasable": true})
	assert.Equal(t, 409, code)
	assert.Equal(t, "LEGAL_ENTITY_NOT_USABLE", bad["reason"])
	elig := func(le, purpose string) map[string]any {
		_, e := fx.alice.do("GET", "/api/product/eligibility?product_id="+fx.id+"&legal_entity_id="+le+"&purpose="+purpose, nil)
		return e
	}
	assert.Equal(t, true, elig(fx.leA, "sell")["eligible"])
	assert.Equal(t, "not_purchasable", elig(fx.leA, "purchase")["reason"])
	assert.Equal(t, "not_opened", elig(fx.leB, "sell")["reason"])
	_, _ = fx.alice.do("POST", "/api/product/products/"+fx.id+"/state", map[string]any{"state": "suspended"})
	assert.Equal(t, "product_suspended", elig(fx.leA, "sell")["reason"])
}
