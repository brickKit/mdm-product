package product

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/brickKit/be-sdk-go/v3/canon"
	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/errs"
	"github.com/brickKit/be-sdk-go/v3/events"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	productv3 "github.com/brickKit/mdm-product/contract/v3/gen/go/be/mdm/product/v3"
	"github.com/jackc/pgx/v5"
)

// aggregate is how the source methods read one kind of record (table AS p).
type aggregate struct {
	table, cols string
	data        func(pgx.Row) (string, int64, any, error) // the ID, the version and the payload
}

var aggregates = map[productv3.Aggregate]aggregate{
	productv3.Aggregate_AGGREGATE_PRODUCT: {"product", productCols, func(r pgx.Row) (string, int64, any, error) {
		v, err := scanProduct(r)
		return v.ID, v.Version, v, err
	}},
	productv3.Aggregate_AGGREGATE_CATEGORY: {"category", categoryCols, func(r pgx.Row) (string, int64, any, error) {
		v, err := scanCategory(r)
		return v.ID, v.Version, v, err
	}},
}

func (m *Module) stamp(ctx context.Context, tx db.Tx) (*productv3.Stamp, error) {
	st, err := events.ReadStamp(ctx, tx, m.instance, events.Stream{Name: Stream, Partitions: 4})
	if err != nil {
		return nil, err
	}
	return &productv3.Stamp{Deployment: m.deployment, Instance: st.Instance, Epoch: st.Epoch.String(), Positions: st.Positions}, nil
}

// readByID reads the records of one aggregate by ID (only well-formed IDs reach the database).
func readByID(ctx context.Context, tx db.Tx, a aggregate, idList []string) (map[string]*productv3.Record, error) {
	var valid []string
	for _, id := range idList {
		if uuidForm.MatchString(id) {
			valid = append(valid, id)
		}
	}
	found := map[string]*productv3.Record{}
	if len(valid) == 0 {
		return found, nil
	}
	rows, err := tx.Query(ctx, fmt.Sprintf(`SELECT %s FROM %s AS p WHERE p.id = ANY($1::uuid[])`, a.cols, a.table), valid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		id, version, data, err := a.data(rows)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(data)
		if err != nil {
			return nil, err
		}
		found[id] = &productv3.Record{Id: id, Version: version, Data: raw}
	}
	return found, rows.Err()
}

// Snapshot pages through one aggregate by ID, with the stamp of the same snapshot (protocol 02, S3).
func (m *Module) Snapshot(ctx context.Context, in *productv3.SnapshotRequest) (*productv3.SnapshotResponse, error) {
	a, ok := aggregates[in.GetAggregate()]
	if !ok {
		return nil, invalid("aggregate", "an aggregate of this component")
	}
	limit := int(in.GetLimit())
	if limit == 0 {
		limit = MaxBatch
	}
	if limit < 0 || limit > MaxBatch {
		return nil, errs.New(errs.ReasonBatchTooLarge, fmt.Sprintf("at most %d records a page", MaxBatch))
	}
	after := in.GetCursor()
	if after == "" {
		after = "00000000-0000-0000-0000-000000000000"
	} else if !uuidForm.MatchString(after) {
		return nil, errs.New(errs.ReasonCursorInvalid, "the cursor cannot be read")
	}
	out := &productv3.SnapshotResponse{}
	err := m.rt.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		var err error
		if out.Stamp, err = m.stamp(ctx, tx); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, fmt.Sprintf(`SELECT %s FROM %s AS p WHERE p.id > $1::uuid ORDER BY p.id LIMIT %d`,
			a.cols, a.table, limit+1), after)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			if len(out.Records) == limit {
				out.NextCursor = out.Records[len(out.Records)-1].Id
				break
			}
			id, version, data, err := a.data(rows)
			if err != nil {
				return err
			}
			raw, err := json.Marshal(data)
			if err != nil {
				return err
			}
			out.Records = append(out.Records, &productv3.Record{Id: id, Version: version, Data: raw})
		}
		return rows.Err()
	}, db.Snapshot())
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Read answers by ID in the requested order: exists, or purged (nothing is deleted in this version).
func (m *Module) Read(ctx context.Context, in *productv3.ReadRequest) (*productv3.ReadResponse, error) {
	a, ok := aggregates[in.GetAggregate()]
	if !ok {
		return nil, invalid("aggregate", "an aggregate of this component")
	}
	if len(in.GetIds()) > MaxBatch {
		return nil, errs.New(errs.ReasonBatchTooLarge, fmt.Sprintf("at most %d IDs a call", MaxBatch))
	}
	out := &productv3.ReadResponse{}
	var found map[string]*productv3.Record
	err := m.rt.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		var err error
		if out.Stamp, err = m.stamp(ctx, tx); err != nil {
			return err
		}
		found, err = readByID(ctx, tx, a, in.GetIds())
		return err
	}, db.Snapshot())
	if err != nil {
		return nil, err
	}
	for _, id := range in.GetIds() {
		r := &productv3.ReadResult{Id: id, State: productv3.ReadState_READ_STATE_PURGED}
		if f, ok := found[id]; ok {
			r = &productv3.ReadResult{Id: id, State: productv3.ReadState_READ_STATE_EXISTS, Version: f.Version, Data: f.Data}
		}
		out.Results = append(out.Results, r)
	}
	return out, nil
}

// Eligibility is protocol 07's evidence for a new reference to a product.
type Eligibility struct {
	Eligible       bool   `json:"eligible"`
	Reason         string `json:"reason"`
	ProductVersion int64  `json:"product_version,omitempty"`
	Stocked        bool   `json:"stocked"`
	EvaluatedAt    string `json:"evaluated_at"`
}

func (m *Module) eligibility(ctx context.Context, productID, legalEntity, purpose string) (Eligibility, error) {
	if purpose != "sell" && purpose != "purchase" {
		return Eligibility{}, invalid("purpose", "purpose is sell or purchase")
	}
	e := Eligibility{EvaluatedAt: canon.Time(time.Now())}
	if !uuidForm.MatchString(productID) || !uuidForm.MatchString(legalEntity) {
		e.Reason = "not_found"
		return e, nil
	}
	err := m.rt.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		p, err := getProduct(ctx, tx, productID, false)
		if errors.Is(err, errNone) {
			e.Reason = "not_found"
			return nil
		}
		if err != nil {
			return err
		}
		e.ProductVersion, e.Stocked = p.Version, p.Stocked
		if p.State != "active" {
			e.Reason = "product_" + p.State
			return nil
		}
		for _, a := range p.Availability {
			if a.LegalEntityID != legalEntity {
				continue
			}
			switch {
			case purpose == "sell" && !a.Sellable:
				e.Reason = "not_sellable"
			case purpose == "purchase" && !a.Purchasable:
				e.Reason = "not_purchasable"
			default:
				e.Eligible, e.Reason = true, "eligible"
			}
			return nil
		}
		e.Reason = "not_opened"
		return nil
	})
	return e, err
}

func (m *Module) eligibilityHTTP(c *httpx.Context) error {
	ctx, _, err := check(c, OpRead)
	if err != nil {
		return err
	}
	e, err := m.eligibility(ctx, c.Query("product_id"), c.Query("legal_entity_id"), c.Query("purpose"))
	if err != nil {
		return err
	}
	return c.JSON(200, e)
}

var purposes = map[productv3.Purpose]string{productv3.Purpose_PURPOSE_SELL: "sell", productv3.Purpose_PURPOSE_PURCHASE: "purchase"}

// CheckEligibility is ProductService.CheckEligibility (the same answer as GET /api/product/eligibility).
func (m *Module) CheckEligibility(ctx context.Context, in *productv3.CheckEligibilityRequest) (*productv3.CheckEligibilityResponse, error) {
	e, err := m.eligibility(ctx, in.GetProductId(), in.GetLegalEntityId(), purposes[in.GetPurpose()])
	if err != nil {
		return nil, err
	}
	return &productv3.CheckEligibilityResponse{Eligible: e.Eligible, Reason: e.Reason, ProductVersion: e.ProductVersion,
		Stocked: e.Stocked, EvaluatedAt: e.EvaluatedAt}, nil
}

// Resolve answers protocol 07's resolution by ID: current, or not found (no merges or erasure in this version).
func (m *Module) Resolve(ctx context.Context, in *productv3.ResolveRequest) (*productv3.ResolveResponse, error) {
	if len(in.GetIds()) > MaxBatch {
		return nil, errs.New(errs.ReasonBatchTooLarge, fmt.Sprintf("at most %d IDs a call", MaxBatch))
	}
	var found map[string]*productv3.Record
	err := m.rt.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		var err error
		found, err = readByID(ctx, tx, aggregates[productv3.Aggregate_AGGREGATE_PRODUCT], in.GetIds())
		return err
	})
	if err != nil {
		return nil, err
	}
	out := &productv3.ResolveResponse{}
	for _, id := range in.GetIds() {
		r := &productv3.Resolved{Id: id, Resolution: productv3.Resolution_RESOLUTION_NOT_FOUND}
		if f, ok := found[id]; ok {
			r = &productv3.Resolved{Id: id, Resolution: productv3.Resolution_RESOLUTION_CURRENT, Version: f.Version, Data: f.Data}
		}
		out.Results = append(out.Results, r)
	}
	return out, nil
}

// Convert is ProductService.Convert (the same answer as GET /api/product/products/{id}/convert).
func (m *Module) Convert(ctx context.Context, in *productv3.ConvertRequest) (*productv3.ConvertResponse, error) {
	out, err := m.convert(ctx, in.GetProductId(), in.GetQuantity(), in.GetFromUnit(), in.GetToUnit())
	if err != nil {
		return nil, err
	}
	return &productv3.ConvertResponse{Quantity: out.Quantity, Factor: out.Factor, Source: out.Source,
		ProductVersion: out.ProductVersion}, nil
}
