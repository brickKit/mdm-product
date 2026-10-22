package product

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/launcher"
	"github.com/brickKit/be-sdk-go/v3/mergesafety"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheManifestDeclaresWhatTheSDKReads: component.yaml's configSchema is exactly the keys the SDK reads.
func TestTheManifestDeclaresWhatTheSDKReads(t *testing.T) {
	raw, err := os.ReadFile("../component.yaml")
	require.NoError(t, err)
	if p := launcher.ManifestProblems(raw, Definition(), nil); len(p) > 0 {
		t.Errorf("component.yaml's configSchema differs from what the SDK reads:\n%v\nthe block:\n%s", p,
			launcher.ConfigSchemaYAML(Definition(), nil))
	}
}

// TestTheManifestListsThePublishedEvents: component.yaml's events.publishes is exactly the event contracts.
func TestTheManifestListsThePublishedEvents(t *testing.T) {
	raw, err := os.ReadFile("../component.yaml")
	require.NoError(t, err)
	assert.Empty(t, launcher.EventProblems(raw, os.DirFS("../contract/events")))
}

// TestTheRoutesAreTheOpenAPIDocument: every route is in contract/openapi.yaml with its operation, and nothing else is.
func TestTheRoutesAreTheOpenAPIDocument(t *testing.T) {
	raw, err := os.ReadFile("../contract/openapi.yaml")
	require.NoError(t, err)
	var r httpx.Routes
	require.NoError(t, (&Module{}).Routes(&r))
	assert.Empty(t, httpx.OpenAPIProblems(raw, r.List()))
}

// TestThePayloadsFitTheirSchemas: what events and source reads carry is what contract/events promises.
func TestThePayloadsFitTheirSchemas(t *testing.T) {
	cat := "01a15ab0-0000-7000-8000-0000000000d1"
	for typ, v := range map[string]any{
		ProductChanged: Product{ID: "01a15ab0-0000-7000-8000-0000000000d2", Code: "TEA-GREEN-250G", Kind: "stock", Stocked: true,
			Name: "Green tea 250 g", Names: map[string]string{"zh-CN": "绿茶 250 克"}, CategoryID: &cat, BaseUnit: "C62",
			Tracking: "batch", Conversions: []Conversion{{Unit: "BX", Factor: "12"}},
			Availability: []Availability{{LegalEntityID: "01a15ab0-0000-7000-8000-0000000000a1", Sellable: true}},
			State:        "active", Version: 3, UpdatedAt: "2026-10-20T08:00:00.000000Z"},
		CategoryChanged: Category{ID: cat, Code: "TEA", Name: "Tea", Names: map[string]string{}, Active: true, Version: 1,
			UpdatedAt: "2026-10-20T08:00:00.000000Z"},
	} {
		c := jsonschema.NewCompiler()
		s, err := c.Compile("../contract/events/" + typ + ".schema.json")
		require.NoError(t, err, typ)
		b, err := json.Marshal(v)
		require.NoError(t, err)
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
		require.NoError(t, err)
		assert.NoError(t, s.Validate(doc), typ)
	}
}

// TestTheModuleIsMergeSafe: the module is compiled into the Go infra shell.
func TestTheModuleIsMergeSafe(t *testing.T) {
	findings, err := mergesafety.CheckDir(".")
	require.NoError(t, err)
	for _, f := range findings {
		t.Errorf("%v", f)
	}
}
