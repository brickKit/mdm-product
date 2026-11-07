//go:build integration

package itest

import (
	assert "github.com/stretchr/testify/assert"
	require "github.com/stretchr/testify/require"
)

// catalog: a category; the boundary card's story: a box holds the product's own number of pieces.
func (fx *fixture) catalog() {
	t := fx.t
	code, cat := fx.alice.do("POST", "/api/product/categories", map[string]any{"code": "TEA", "name": "茶叶"})
	require.Equal(t, 200, code, cat)
	// the boundary card's story: a box holds the product's own number of pieces
	code, tea := fx.alice.do("POST", "/api/product/products", map[string]any{"code": "TEA-GREEN-250G", "kind": "stock",
		"name": "绿茶 250 克", "base_unit": "C62", "category_id": cat["id"], "tracking": "batch",
		"conversions": []map[string]any{{"unit": "BX", "factor": "12.000"}}})
	require.Equal(t, 200, code, tea)
	assert.Equal(t, true, tea["stocked"])
	assert.Equal(t, "12", tea["conversions"].([]any)[0].(map[string]any)["factor"])
	fx.tea, fx.id = tea, tea["id"].(string)
}

// services: a service needs no stock and is never tracked; the same content again makes no new version, for a
// product with no conversions and no availability as well.
func (fx *fixture) services() {
	t := fx.t
	// the story: a service needs no stock
	_, svc := fx.alice.do("POST", "/api/product/products", map[string]any{"code": "SVC-INSTALL", "kind": "service",
		"name": "上门安装", "base_unit": "HUR"})
	assert.Equal(t, false, svc["stocked"])
	code, _ := fx.alice.do("PATCH", "/api/product/products/"+svc["id"].(string), map[string]any{"tracking": "serial"})
	assert.Equal(t, 400, code, "a service is never tracked by batch or serial")
	// the same content again makes no new version, for a product with no conversions and no availability as well
	svcID := svc["id"].(string)
	code, same := fx.alice.do("PATCH", "/api/product/products/"+svcID, map[string]any{"name": "上门安装"})
	require.Equal(t, 200, code, same)
	assert.EqualValues(t, 1, same["version"], "nothing changed: no new version")
	_, open1 := fx.alice.do("PUT", "/api/product/products/"+svcID+"/legal-entities/"+fx.leB, map[string]any{"sellable": true, "purchasable": false})
	_, open2 := fx.alice.do("PUT", "/api/product/products/"+svcID+"/legal-entities/"+fx.leB, map[string]any{"sellable": true, "purchasable": false})
	assert.EqualValues(t, 2, open1["version"])
	assert.EqualValues(t, 2, open2["version"], "the same availability again: no new version")
	fx.svc = svc
}

// movements: once inventory has movements, the tracking policy no longer changes; the name still does.
func (fx *fixture) movements() {
	t, ctx := fx.t, fx.ctx
	// the story: once inventory has movements, the tracking policy no longer changes (the flag comes from inventory's
	// events in step 7; here it is set the way that consumer will)
	_, err := fx.admin.Exec(ctx, "UPDATE "+itSchema+".product SET has_movements = true WHERE id = $1", fx.id)
	require.NoError(t, err)
	code, moved := fx.alice.do("PATCH", "/api/product/products/"+fx.id, map[string]any{"tracking": "none"})
	assert.Equal(t, 409, code)
	assert.Equal(t, "HAS_MOVEMENTS", moved["reason"])
	code, renamed := fx.alice.do("PATCH", "/api/product/products/"+fx.id, map[string]any{"name": "绿茶 250 克（新包装）"})
	assert.Equal(t, 200, code, "the name still changes")
	assert.EqualValues(t, 2, renamed["version"])
}
