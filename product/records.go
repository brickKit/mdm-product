package product

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/brickKit/be-sdk-go/v3/canon"
	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/events"
	"github.com/jackc/pgx/v5"
)

// Conversion is a product's own conversion: one Unit is Factor of its base unit.
type Conversion struct {
	Unit   string `json:"unit"`
	Factor string `json:"factor"`
}

// Availability says whether new documents in a legal entity may sell or buy the product.
type Availability struct {
	LegalEntityID string `json:"legal_entity_id"`
	Sellable      bool   `json:"sellable"`
	Purchasable   bool   `json:"purchasable"`
}

// Product is a product's complete state: the payload of mdm.product.changed.v1 and of its reads.
type Product struct {
	ID           string            `json:"id"`
	Code         string            `json:"code"`
	Kind         string            `json:"kind"`
	Stocked      bool              `json:"stocked"`
	Name         string            `json:"name"`
	Names        map[string]string `json:"names"`
	CategoryID   *string           `json:"category_id"`
	BaseUnit     string            `json:"base_unit"`
	Tracking     string            `json:"tracking"`
	HasMovements bool              `json:"has_movements"`
	Conversions  []Conversion      `json:"conversions"`
	Availability []Availability    `json:"availability"`
	State        string            `json:"state"`
	Version      int64             `json:"version"`
	UpdatedAt    string            `json:"updated_at"`
}

// Category is a product category's complete state (mdm.product_category.changed.v1).
type Category struct {
	ID        string            `json:"id"`
	Code      string            `json:"code"`
	Name      string            `json:"name"`
	Names     map[string]string `json:"names"`
	ParentID  *string           `json:"parent_id"`
	Active    bool              `json:"active"`
	Version   int64             `json:"version"`
	UpdatedAt string            `json:"updated_at"`
}

// productCols reads a product with its conversions and availability in one row (ordered, so the same state always
// gives the same JSON).
const (
	productCols = `p.id::text, p.code, p.kind, p.name, p.names, p.category_id::text, p.base_unit, p.tracking,
		p.has_movements, p.state, p.version, p.updated_at,
		COALESCE((SELECT jsonb_agg(jsonb_build_object('unit', c.unit, 'factor', trim_scale(c.factor)::text) ORDER BY c.unit)
			FROM product_conversion c WHERE c.product_id = p.id), '[]'::jsonb),
		COALESCE((SELECT jsonb_agg(jsonb_build_object('legal_entity_id', a.legal_entity_id::text, 'sellable', a.sellable,
			'purchasable', a.purchasable) ORDER BY a.legal_entity_id) FROM product_availability a WHERE a.product_id = p.id),
			'[]'::jsonb)`
	categoryCols = `id::text, code, name, names, parent_id::text, active, version, updated_at`
)

func scanProduct(row pgx.Row) (Product, error) {
	var p Product
	var names, conv, avail []byte
	var at time.Time
	err := row.Scan(&p.ID, &p.Code, &p.Kind, &p.Name, &names, &p.CategoryID, &p.BaseUnit, &p.Tracking, &p.HasMovements,
		&p.State, &p.Version, &at, &conv, &avail)
	if err != nil {
		return p, err
	}
	p.Stocked, p.UpdatedAt = p.Kind == "stock", canon.Time(at)
	return p, errors.Join(json.Unmarshal(names, &p.Names), json.Unmarshal(conv, &p.Conversions),
		json.Unmarshal(avail, &p.Availability))
}

func scanCategory(row pgx.Row) (Category, error) {
	var c Category
	var names []byte
	var at time.Time
	err := row.Scan(&c.ID, &c.Code, &c.Name, &names, &c.ParentID, &c.Active, &c.Version, &at)
	if err == nil {
		c.UpdatedAt, err = canon.Time(at), json.Unmarshal(names, &c.Names)
	}
	return c, err
}

// errNone is a lookup that found no row.
var errNone = errors.New("no such record")

func one[T any](row pgx.Row, scan func(pgx.Row) (T, error)) (T, error) {
	v, err := scan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, errNone
	}
	return v, err
}

func getProduct(ctx context.Context, tx db.Tx, id string, lock bool) (Product, error) {
	if lock { // the row lock first: the aggregate columns cannot take FOR UPDATE
		if _, err := tx.Exec(ctx, `SELECT 1 FROM product WHERE id = $1::uuid FOR UPDATE`, id); err != nil {
			return Product{}, err
		}
	}
	return one(tx.QueryRow(ctx, `SELECT `+productCols+` FROM product p WHERE p.id = $1::uuid`, id), scanProduct)
}

func getCategory(ctx context.Context, tx db.Tx, id string, lock bool) (Category, error) {
	q := `SELECT ` + categoryCols + ` FROM category WHERE id = $1::uuid`
	if lock {
		q += ` FOR UPDATE`
	}
	return one(tx.QueryRow(ctx, q, id), scanCategory)
}

// emit writes the record's full-state event into the outbox, in the transaction that changed it (protocol 02, S2).
func (m *Module) emit(ctx context.Context, tx db.Tx, typ, id string, version int64, data any) error {
	_, err := m.rt.Events.Emit(ctx, tx, Stream, events.Event{Type: typ, RecordID: id, Version: version,
		Projection: events.Full, Data: data})
	return err
}
