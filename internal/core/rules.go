package core

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/brickKit/be-sdk-go/v3/audit"
	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/errs"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/identity"
	"github.com/brickKit/be-sdk-go/v3/money"
	apd "github.com/cockroachdb/apd/v3"
)

// The component's own reasons (the SDK's catalog has the shared ones).
var (
	ErrVersion = errs.Define(ID, "VERSION_CONFLICT", errs.Aborted, 409, errs.RetryNo, "The record changed since that version")
	ErrCode    = errs.Define(ID, "CODE_TAKEN", errs.AlreadyExists, 409, errs.RetryNo, "The code belongs to another record")
	ErrState   = errs.Define(ID, "STATE_CONFLICT", errs.FailedPrecondition, 409, errs.RetryNo,
		"The record's state does not allow this")
	ErrMoved = errs.Define(ID, "HAS_MOVEMENTS", errs.FailedPrecondition, 409, errs.RetryNo,
		"Inventory has movements of the product: its tracking and base unit no longer change")
	ErrLegalEntity = errs.Define(ID, "LEGAL_ENTITY_NOT_USABLE", errs.FailedPrecondition, 409, errs.RetryNo,
		"The legal entity does not exist or is not active")
	ErrUnit = errs.Define(ID, "NO_CONVERSION", errs.FailedPrecondition, 409, errs.RetryNo,
		"No conversion between these units for this product")
)

var (
	CodeForm = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_.-]{0,47}$`)
	UUIDForm = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

func Unsupported(c *httpx.Context) error {
	if _, _, err := Check(c, c.Operation()); err != nil {
		return err
	}
	return errs.New(errs.ReasonCapabilityUnsupported, "not in this version of mdm/product (barcodes and images come later)")
}

// Check admits the request's subject to op and returns the context to work in and the acting principal.
func Check(c *httpx.Context, op string) (context.Context, string, error) {
	s, _ := sdkauthz.From(c.Ctx())
	ctx, _, err := s.Check(c.Ctx(), op, sdkauthz.Facts{})
	if err != nil {
		return ctx, "", err
	}
	u, _ := identity.UserFrom(ctx)
	return ctx, u.Subject, nil
}

func Invalid(field, why string) error {
	return errs.New(errs.ReasonRequestInvalid, why).WithViolations(errs.Violation{Field: field, Reason: "INVALID", Description: why})
}

func NotFound(what string) error { return errs.New(errs.ReasonNotFound, what+" not found") }

// Change records the business change with the record's new state (protocol 03), in the transaction that made it.
func Change(m *Base, ctx context.Context, tx db.Tx, actor, op, typ, id string, after any) error {
	_, err := m.RT.Audit.Write(ctx, tx, audit.Record{Category: audit.BusinessChange, Operation: op, Outcome: audit.Succeeded,
		Actor: actor, Target: audit.TargetOf(typ, id, after), Changes: map[string]any{"after": after}})
	return err
}

func Same(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

func CheckNames(names map[string]string) error {
	for tag, n := range names {
		if tag == "" || len(tag) > 35 || strings.TrimSpace(n) == "" {
			return Invalid("names", "names maps a language tag to a non-empty name")
		}
	}
	return nil
}

func Decimal(s string) (*apd.Decimal, error) { return money.Parse(s) }

// Canonical is a decimal's text without trailing zeros (the contract's form).
func Canonical(d *apd.Decimal) string {
	var r apd.Decimal
	r.Reduce(d)
	return r.Text('f')
}

func SortAvailability(a []Availability) {
	sort.Slice(a, func(i, j int) bool { return a[i].LegalEntityID < a[j].LegalEntityID })
}
