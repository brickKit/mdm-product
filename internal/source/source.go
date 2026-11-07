package source

import (
	"context"
	"encoding/json"
	"fmt"

	core "github.com/brickKit/mdm-product/v3/internal/core"
	products "github.com/brickKit/mdm-product/v3/internal/products"
	units "github.com/brickKit/mdm-product/v3/internal/units"

	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/errs"
	"github.com/brickKit/be-sdk-go/v3/events"
	productv3 "github.com/brickKit/mdm-product/contract/v3/gen/go/be/mdm/product/v3"
	"github.com/jackc/pgx/v5"
)

// aggregate is how the source methods read one kind of record (table AS p).
type aggregate struct {
	table, cols string
	data        func(pgx.Row) (string, int64, any, error) // the ID, the version and the payload
}

var aggregates = map[productv3.Aggregate]aggregate{
	productv3.Aggregate_AGGREGATE_PRODUCT: {"product", core.ProductCols, func(r pgx.Row) (string, int64, any, error) {
		v, err := core.ScanProduct(r)
		return v.ID, v.Version, v, err
	}},
	productv3.Aggregate_AGGREGATE_CATEGORY: {"category", core.CategoryCols, func(r pgx.Row) (string, int64, any, error) {
		v, err := core.ScanCategory(r)
		return v.ID, v.Version, v, err
	}},
}

func stamp(m *core.Base, ctx context.Context, tx db.Tx) (*productv3.Stamp, error) {
	st, err := events.ReadStamp(ctx, tx, m.Instance, events.Stream{Name: core.Stream, Partitions: 4})
	if err != nil {
		return nil, err
	}
	return &productv3.Stamp{Deployment: m.Deployment, Instance: st.Instance, Epoch: st.Epoch.String(), Positions: st.Positions}, nil
}

// readByID reads the records of one aggregate by ID (only well-formed IDs reach the database).
func readByID(ctx context.Context, tx db.Tx, a aggregate, idList []string) (map[string]*productv3.Record, error) {
	var valid []string
	for _, id := range idList {
		if core.UUIDForm.MatchString(id) {
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
func Snapshot(m *core.Base, ctx context.Context, in *productv3.SnapshotRequest) (*productv3.SnapshotResponse, error) {
	a, ok := aggregates[in.GetAggregate()]
	if !ok {
		return nil, core.Invalid("aggregate", "an aggregate of this component")
	}
	limit := int(in.GetLimit())
	if limit == 0 {
		limit = core.MaxBatch
	}
	if limit < 0 || limit > core.MaxBatch {
		return nil, errs.New(errs.ReasonBatchTooLarge, fmt.Sprintf("at most %d records a page", core.MaxBatch))
	}
	after := in.GetCursor()
	if after == "" {
		after = "00000000-0000-0000-0000-000000000000"
	} else if !core.UUIDForm.MatchString(after) {
		return nil, errs.New(errs.ReasonCursorInvalid, "the cursor cannot be read")
	}
	out := &productv3.SnapshotResponse{}
	err := m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		var err error
		if out.Stamp, err = stamp(m, ctx, tx); err != nil {
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
func Read(m *core.Base, ctx context.Context, in *productv3.ReadRequest) (*productv3.ReadResponse, error) {
	a, ok := aggregates[in.GetAggregate()]
	if !ok {
		return nil, core.Invalid("aggregate", "an aggregate of this component")
	}
	if len(in.GetIds()) > core.MaxBatch {
		return nil, errs.New(errs.ReasonBatchTooLarge, fmt.Sprintf("at most %d IDs a call", core.MaxBatch))
	}
	out := &productv3.ReadResponse{}
	var found map[string]*productv3.Record
	err := m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		var err error
		if out.Stamp, err = stamp(m, ctx, tx); err != nil {
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

var purposes = map[productv3.Purpose]string{productv3.Purpose_PURPOSE_SELL: "sell", productv3.Purpose_PURPOSE_PURCHASE: "purchase"}

// CheckEligibility is ProductService.CheckEligibility (the same answer as GET /api/product/eligibility).
func CheckEligibility(m *core.Base, ctx context.Context, in *productv3.CheckEligibilityRequest) (*productv3.CheckEligibilityResponse, error) {
	e, err := products.EligibilityOf(m, ctx, in.GetProductId(), in.GetLegalEntityId(), purposes[in.GetPurpose()])
	if err != nil {
		return nil, err
	}
	return &productv3.CheckEligibilityResponse{Eligible: e.Eligible, Reason: e.Reason, ProductVersion: e.ProductVersion,
		Stocked: e.Stocked, EvaluatedAt: e.EvaluatedAt}, nil
}

// Resolve answers protocol 07's resolution by ID: current, or not found (no merges or erasure in this version).
func Resolve(m *core.Base, ctx context.Context, in *productv3.ResolveRequest) (*productv3.ResolveResponse, error) {
	if len(in.GetIds()) > core.MaxBatch {
		return nil, errs.New(errs.ReasonBatchTooLarge, fmt.Sprintf("at most %d IDs a call", core.MaxBatch))
	}
	var found map[string]*productv3.Record
	err := m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
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
func Convert(m *core.Base, ctx context.Context, in *productv3.ConvertRequest) (*productv3.ConvertResponse, error) {
	out, err := units.Convert(m, ctx, in.GetProductId(), in.GetQuantity(), in.GetFromUnit(), in.GetToUnit())
	if err != nil {
		return nil, err
	}
	return &productv3.ConvertResponse{Quantity: out.Quantity, Factor: out.Factor, Source: out.Source,
		ProductVersion: out.ProductVersion}, nil
}
