package product

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

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

func unitOf(ctx context.Context, tx db.Tx, code string) (Unit, error) {
	var u Unit
	var names []byte
	err := tx.QueryRow(ctx, `SELECT code, category, trim_scale(factor)::text, decimals, name, names FROM unit WHERE code = $1`,
		code).Scan(&u.Code, &u.Category, &u.Factor, &u.Decimals, &u.Name, &names)
	if err != nil {
		return u, invalid("unit", "no unit "+code+" in the catalog")
	}
	return u, json.Unmarshal(names, &u.Names)
}

func (m *Module) listUnits(c *httpx.Context) error {
	ctx, _, err := check(c, OpRead)
	if err != nil {
		return err
	}
	out := []Unit{}
	err = m.rt.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
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

func decimal(s string) (*apd.Decimal, error) { return money.Parse(s) }

// canonical is a decimal's text without trailing zeros (the contract's form).
func canonical(d *apd.Decimal) string {
	var r apd.Decimal
	r.Reduce(d)
	return r.Text('f')
}

func sortAvailability(a []Availability) {
	sort.Slice(a, func(i, j int) bool { return a[i].LegalEntityID < a[j].LegalEntityID })
}

// Converted is a quantity in another unit.
type Converted struct {
	Quantity       string `json:"quantity"`
	Factor         string `json:"factor"`
	Source         string `json:"source"` // product, catalog
	ProductVersion int64  `json:"product_version"`
}

// convert turns a quantity of the product from one unit into another: through the product's base unit, with the
// product's own conversion first and the catalog's within the base unit's category second (protocol 14: product
// conversions come before generic ones). The result rounds to the target unit's decimals.
func (m *Module) convert(ctx context.Context, productID, quantity, from, to string) (Converted, error) {
	if !uuidForm.MatchString(productID) {
		return Converted{}, notFound("product")
	}
	q, err := decimal(quantity)
	if err != nil {
		return Converted{}, invalid("quantity", "quantity is a decimal string")
	}
	var out Converted
	err = m.rt.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		p, err := getProduct(ctx, tx, productID, false)
		if err != nil {
			return err
		}
		base, err := unitOf(ctx, tx, p.BaseUnit)
		if err != nil {
			return err
		}
		target, err := unitOf(ctx, tx, to)
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
				return decimal(f)
			}
			u, err := unitOf(ctx, tx, code)
			if err != nil {
				return nil, err
			}
			if u.Category != base.Category || u.Factor == nil || base.Factor == nil {
				return nil, errUnit.New("no conversion from " + code + " to " + p.BaseUnit + " for " + p.Code)
			}
			uf, _ := decimal(*u.Factor)
			bf, _ := decimal(*base.Factor)
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
		out = Converted{Quantity: rounded.Text('f'), Factor: canonical(shown), Source: source, ProductVersion: p.Version}
		return nil
	})
	if errors.Is(err, errNone) {
		return out, notFound("product")
	}
	return out, err
}

func (m *Module) convertHTTP(c *httpx.Context) error {
	ctx, _, err := check(c, OpRead)
	if err != nil {
		return err
	}
	out, err := m.convert(ctx, c.Param("id"), c.Query("quantity"), c.Query("from_unit"), c.Query("to_unit"))
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}
