package protocheck_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/trendvidia/protocheck/v2"
	"github.com/trendvidia/protocheck/v2/internal/testpb"
	"google.golang.org/protobuf/proto"
)

// violationsOf validates msg and returns the violations, failing t if the
// message is unexpectedly valid.
func violationsOf(t *testing.T, v *protocheck.Validator, msg proto.Message) []*protocheck.EnrichedViolation {
	t.Helper()
	err := v.Validate(msg)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	var ve *protocheck.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	return ve.Violations
}

func TestFieldPathString(t *testing.T) {
	cases := []struct {
		path protocheck.FieldPath
		want string
	}{
		{protocheck.FieldPath{{FieldName: "name", FieldNumber: 1}}, "name"},
		{protocheck.FieldPath{
			{FieldName: "address", FieldNumber: 2},
			{FieldName: "zip_code", FieldNumber: 3},
		}, "address.zip_code"},
		{protocheck.FieldPath{
			{FieldName: "items", FieldNumber: 4, Subscript: protocheck.Index(3)},
			{FieldName: "product_id", FieldNumber: 1},
		}, "items[3].product_id"},
		{protocheck.FieldPath{{FieldName: "labels", FieldNumber: 1, Subscript: protocheck.StringKey("env")}}, "labels[env]"},
		{protocheck.FieldPath{{FieldName: "counts", FieldNumber: 1, Subscript: protocheck.IntKey(-2)}}, "counts[-2]"},
		{protocheck.FieldPath{{FieldName: "quotas", FieldNumber: 1, Subscript: protocheck.UintKey(7)}}, "quotas[7]"},
		{protocheck.FieldPath{{FieldName: "flags", FieldNumber: 1, Subscript: protocheck.BoolKey(true)}}, "flags[true]"},
	}
	for _, c := range cases {
		if got := c.path.String(); got != c.want {
			t.Errorf("FieldPath.String() = %q, want %q", got, c.want)
		}
	}
}

func TestStructuredPathNestedMessage(t *testing.T) {
	v := mustValidator(t)

	violations := violationsOf(t, v, &testpb.Order{
		Id: "123e4567-e89b-12d3-a456-426614174000",
		ShippingAddress: &testpb.Address{
			Street: "Main St 1",
			// city missing → required violation nested one level down.
		},
		Items: []*testpb.LineItem{{ProductId: "p", Quantity: 1, UnitPrice: 1}},
	})

	var hit *protocheck.EnrichedViolation
	for _, viol := range violations {
		if viol.Path.String() == "shipping_address.city" {
			hit = viol
		}
	}
	if hit == nil {
		t.Fatalf("no violation at shipping_address.city, got %v", violations)
	}
	if hit.RuleKind != protocheck.RuleKindRequired {
		t.Errorf("RuleKind = %v, want REQUIRED", hit.RuleKind)
	}
	want := protocheck.FieldPath{
		{FieldName: "shipping_address", FieldNumber: 2},
		{FieldName: "city", FieldNumber: 2},
	}
	if len(hit.Path) != len(want) || hit.Path[0] != want[0] || hit.Path[1] != want[1] {
		t.Errorf("Path = %#v, want %#v", hit.Path, want)
	}
}

func TestStructuredPathRepeatedSubscript(t *testing.T) {
	v := mustValidator(t)

	violations := violationsOf(t, v, &testpb.Inventory{
		Skus: []string{"ABC-1", "x", "DEF-2"},
	})
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %v", violations)
	}
	viol := violations[0]
	if got := viol.Path.String(); got != "skus[1]" {
		t.Errorf("path = %q, want %q", got, "skus[1]")
	}
	if viol.Path[0].Subscript != protocheck.Index(1) {
		t.Errorf("subscript = %#v, want Index(1)", viol.Path[0].Subscript)
	}
	if viol.Cause.Code != "string.min_len" {
		t.Errorf("code = %q, want string.min_len", viol.Cause.Code)
	}
	if got := viol.Cause.Params["min_len"]; got != uint64(3) {
		t.Errorf("params[min_len] = %#v (%T), want uint64(3)", got, got)
	}
}

func TestStructuredPathMapKeyAndValue(t *testing.T) {
	v := mustValidator(t)

	// Empty key violates keys.min_len; oversized value violates
	// values.max_len. Both paths address the same entry shape — ForKey
	// distinguishes the key violation.
	keyViolations := violationsOf(t, v, &testpb.Config{
		Labels: map[string]string{"": "ok"},
	})
	if len(keyViolations) != 1 {
		t.Fatalf("expected 1 key violation, got %v", keyViolations)
	}
	kv := keyViolations[0]
	if !kv.ForKey {
		t.Error("key violation must set ForKey")
	}
	if kv.Path[0].Subscript != protocheck.StringKey("") {
		t.Errorf("subscript = %#v, want StringKey(\"\")", kv.Path[0].Subscript)
	}
	if !strings.Contains(kv.String(), "(key)") {
		t.Errorf("String() should mark the key violation, got %q", kv.String())
	}

	valViolations := violationsOf(t, v, &testpb.Config{
		Labels: map[string]string{"env": strings.Repeat("x", 300)},
	})
	if len(valViolations) != 1 {
		t.Fatalf("expected 1 value violation, got %v", valViolations)
	}
	vv := valViolations[0]
	if vv.ForKey {
		t.Error("value violation must not set ForKey")
	}
	if got := vv.Path.String(); got != "labels[env]" {
		t.Errorf("path = %q, want %q", got, "labels[env]")
	}
	if vv.Cause.Code != "string.max_len" {
		t.Errorf("code = %q, want string.max_len", vv.Cause.Code)
	}
}

func TestRuleKinds(t *testing.T) {
	v := mustValidator(t)

	violations := violationsOf(t, v, &testpb.User{
		Name:  strings.Repeat("n", 101), // string.max_len → VALIDATE
		Email: "",                       // required → REQUIRED
	})
	kinds := make(map[string]protocheck.RuleKind)
	for _, viol := range violations {
		kinds[viol.Cause.Code] = viol.RuleKind
	}
	if kinds["required"] != protocheck.RuleKindRequired {
		t.Errorf("required → %v, want REQUIRED", kinds["required"])
	}
	if kinds["string.max_len"] != protocheck.RuleKindValidate {
		t.Errorf("string.max_len → %v, want VALIDATE", kinds["string.max_len"])
	}
}

func TestReportFormatLocalized(t *testing.T) {
	v := mustValidator(t)
	eng := v.Engine()
	if err := eng.RegisterCatalog("pt-BR", staticCatalog{
		"required": "campo obrigatório",
	}); err != nil {
		t.Fatal(err)
	}

	report, err := eng.Validate(&testpb.User{Name: "Alice"}) // email missing
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Violations) != 1 {
		t.Fatalf("expected 1 violation, got %v", report.Violations)
	}

	localized := report.Format("pt-BR")
	if len(localized) != 1 || !strings.Contains(localized[0], "campo obrigatório") {
		t.Errorf("Format(pt-BR) = %v, want catalog rendering", localized)
	}
	if !strings.Contains(localized[0], "email") || !strings.Contains(localized[0], "[required]") {
		t.Errorf("Format output should carry path and code, got %v", localized)
	}

	// Unregistered locale → fallback message.
	fallback := report.Format("de-DE")
	if len(fallback) != 1 || !strings.Contains(fallback[0], "field is required") {
		t.Errorf("Format(de-DE) = %v, want fallback message", fallback)
	}
}
