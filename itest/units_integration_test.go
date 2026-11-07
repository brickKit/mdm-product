//go:build integration

package itest

import (
	assert "github.com/stretchr/testify/assert"
)

// conversions: the product's own conversion (a box of 12, whole boxes), none between unrelated units, a catalog
// conversion within the base unit's category.
func (fx *fixture) conversions() {
	t := fx.t
	_, conv := fx.alice.do("GET", "/api/product/products/"+fx.id+"/convert?quantity=3&from_unit=BX&to_unit=C62", nil)
	assert.Equal(t, "36", conv["quantity"])
	assert.Equal(t, "product", conv["source"])
	_, conv = fx.alice.do("GET", "/api/product/products/"+fx.id+"/convert?quantity=40&from_unit=C62&to_unit=BX", nil)
	assert.Equal(t, "3", conv["quantity"], "a box is counted in whole boxes: 40/12 rounds to 3")
	code, noconv := fx.alice.do("GET", "/api/product/products/"+fx.id+"/convert?quantity=1&from_unit=KGM&to_unit=C62", nil)
	assert.Equal(t, 409, code)
	assert.Equal(t, "NO_CONVERSION", noconv["reason"])
	// a catalog conversion within the base unit's category
	_, rice := fx.alice.do("POST", "/api/product/products", map[string]any{"code": "RICE-BULK", "kind": "stock",
		"name": "散装大米", "base_unit": "KGM"})
	_, conv = fx.alice.do("GET", "/api/product/products/"+rice["id"].(string)+"/convert?quantity=2.5&from_unit=TNE&to_unit=KGM", nil)
	assert.Equal(t, "2500.000", conv["quantity"])
	assert.Equal(t, "catalog", conv["source"])
}
