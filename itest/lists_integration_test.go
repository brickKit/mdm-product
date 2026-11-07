//go:build integration

package itest

import (
	assert "github.com/stretchr/testify/assert"
)

// lists: the keyword (pinyin initials), pages, the unit catalog.
func (fx *fixture) lists() {
	t := fx.t
	// lists and the keyword
	_, found := fx.alice.do("GET", "/api/product/products?q=lc", nil) // 绿茶: l c
	assert.Len(t, found["products"], 1)
	_, page := fx.alice.do("GET", "/api/product/products?limit=2", nil)
	assert.Len(t, page["products"], 2)
	assert.NotEmpty(t, page["next_cursor"])
	_, units := fx.alice.do("GET", "/api/product/units", nil)
	assert.GreaterOrEqual(t, len(units["units"].([]any)), 20)
}
