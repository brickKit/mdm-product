package product

import (
	"github.com/brickKit/be-sdk-go/v3/recovery"
	core "github.com/brickKit/mdm-product/v3/internal/core"
)

// RecoveryChecks registers what a full recovery asks this member (be-protocol chapter 14): whether the objects this
// member audits are still here, at which version, and the checks it reports.
func (m *Module) RecoveryChecks(c *recovery.Checks) error {
	c.Presence(core.Categories, recovery.TablePresence("category", "id", "version"))
	c.Presence(core.Products, recovery.TablePresence("product", "id", "version"))
	return nil
}
