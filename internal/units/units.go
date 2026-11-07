package units

import (
	"context"
	"encoding/json"
	"errors"

	core "github.com/brickKit/mdm-product/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/money"
	"github.com/cockroachdb/apd/v3"
)

// Unit is one unit of the catalog.
type Unit struct {
	Code     string            `json:"code"`
	Category string            `json:"category"`
	Factor   *string           `json:"factor"` // in the category's base unit; nil for package units
	Decimals int               `json:"decimals"`
	Name     string            `json:"name"`
	Names    map[string]string `json:"names"`
}

func UnitOf(ctx context.Context, tx db.Tx, code string) (Unit, error) {
	var u Unit
	var names []byte
	err := tx.QueryRow(ctx, `SELECT code, category, trim_scale(factor)::text, decimals, name, names FROM unit WHERE code = $1`,
		code).Scan(&u.Code, &u.Category, &u.Factor, &u.Decimals, &u.Name, &names)
	if err != nil {
		return u, core.Invalid("unit", "no unit "+code+" in the catalog")
	}
	return u, json.Unmarshal(names, &u.Names)
}

func ListUnits(m *core.Base, c *httpx.Context) error {
	ctx, _, err := core.Check(c, core.OpRead)
	if err != nil {
		return err
	}
	out := []Unit{}
	err = m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		rows, err := tx.Query(ctx, `SELECT code, category, trim_scale(factor)::text, decimals, name, names FROM unit
			ORDER BY category, factor NULLS LAST, code`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var u Unit
			var names []byte
			if err := rows.Scan(&u.Code, &u.Category, &u.Factor, &u.Decimals, &u.Name, &names); err != nil {
				return err
			}
			if err := json.Unmarshal(names, &u.Names); err != nil {
				return err
			}
			out = append(out, u)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	return c.JSON(200, map[string]any{"units": out})
}

// arith is the context of conversions: far more digits than the factors' 12 decimal places need.
var arith = apd.BaseContext.WithPrecision(40)

// Converted is a quantity in another unit.
type Converted struct {
	Quantity       string `json:"quantity"`
	Factor         string `json:"factor"`
	Source         string `json:"source"` // product, catalog
	ProductVersion int64  `json:"product_version"`
}

// Convert turns a quantity of the product from one unit into another: through the product's base unit, with the
// product's own conversion first and the catalog's within the base unit's category second (protocol 14: product
// conversions come before generic ones). The result rounds to the target unit's decimals.
func Convert(m *core.Base, ctx context.Context, productID, quantity, from, to string) (Converted, error) {
	if !core.UUIDForm.MatchString(productID) {
		return Converted{}, core.NotFound("product")
	}
	q, err := core.Decimal(quantity)
	if err != nil {
		return Converted{}, core.Invalid("quantity", "quantity is a decimal string")
	}
	var out Converted
	err = m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		p, err := core.GetProduct(ctx, tx, productID, false)
		if err != nil {
			return err
		}
		base, err := UnitOf(ctx, tx, p.BaseUnit)
		if err != nil {
			return err
		}
		target, err := UnitOf(ctx, tx, to)
		if err != nil {
			return err
		}
		own := map[string]string{}
		for _, cv := range p.Conversions {
			own[cv.Unit] = cv.Factor
		}
		source := "catalog"
		// toBase is how many base units one u is
		toBase := func(code string) (*apd.Decimal, error) {
			if code == p.BaseUnit {
				return apd.New(1, 0), nil
			}
			if f, ok := own[code]; ok {
				source = "product"
				return core.Decimal(f)
			}
			u, err := UnitOf(ctx, tx, code)
			if err != nil {
				return nil, err
			}
			if u.Category != base.Category || u.Factor == nil || base.Factor == nil {
				return nil, core.ErrUnit.New("no conversion from " + code + " to " + p.BaseUnit + " for " + p.Code)
			}
			uf, _ := core.Decimal(*u.Factor)
			bf, _ := core.Decimal(*base.Factor)
			var r apd.Decimal
			_, err = arith.Quo(&r, uf, bf)
			return &r, err
		}
		ff, err := toBase(from)
		if err != nil {
			return err
		}
		ft, err := toBase(to)
		if err != nil {
			return err
		}
		var factor, result apd.Decimal
		if _, err := arith.Quo(&factor, ff, ft); err != nil {
			return err
		}
		if _, err := arith.Mul(&result, q, &factor); err != nil {
			return err
		}
		rounded := money.Round(&result, int32(target.Decimals), money.HalfAwayFromZero)
		shown := money.Round(&factor, 12, money.HalfAwayFromZero)
		out = Converted{Quantity: rounded.Text('f'), Factor: core.Canonical(shown), Source: source, ProductVersion: p.Version}
		return nil
	})
	if errors.Is(err, core.ErrNone) {
		return out, core.NotFound("product")
	}
	return out, err
}

func ConvertHTTP(m *core.Base, c *httpx.Context) error {
	ctx, _, err := core.Check(c, core.OpRead)
	if err != nil {
		return err
	}
	out, err := Convert(m, ctx, c.Param("id"), c.Query("quantity"), c.Query("from_unit"), c.Query("to_unit"))
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}
