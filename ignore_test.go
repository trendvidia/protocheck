package protocheck_test

import (
	"testing"

	"github.com/trendvidia/protocheck/v2/internal/testpb"
)

// IGNORE_IF_DEFAULT_VALUE vs IGNORE_IF_UNPOPULATED — the two diverge only for
// proto3 optional fields explicitly set to the zero value.
func TestIgnoreModes_DefaultValueVsUnpopulated(t *testing.T) {
	v := mustValidator(t)
	empty := ""
	short := "ab"
	long := "abcdef"

	// Both unset: both ignore modes skip → valid.
	expectValid(t, v, &testpb.IgnoreModes{})

	// Both explicitly set to the default ("") — proto3 optional makes Has=true:
	//   IGNORE_IF_DEFAULT_VALUE skips (value == default).
	//   IGNORE_IF_UNPOPULATED does NOT skip (populated == true), so "" fails min_len.
	expectValid(t, v, &testpb.IgnoreModes{DefaultSkip: &empty})
	expectInvalid(t, v, &testpb.IgnoreModes{UnpopSkip: &empty}, "string.min_len")

	// Both set to a non-default but rule-violating value: both validate, both fail.
	expectInvalid(t, v, &testpb.IgnoreModes{DefaultSkip: &short}, "string.min_len")
	expectInvalid(t, v, &testpb.IgnoreModes{UnpopSkip: &short}, "string.min_len")

	// Both set to a passing value: valid.
	expectValid(t, v, &testpb.IgnoreModes{DefaultSkip: &long, UnpopSkip: &long})
}

// IGNORE_IF_UNPOPULATED on a message-typed field: skip recursion when nil,
// recurse normally when populated.
func TestIgnore_MessageField_Unpopulated(t *testing.T) {
	v := mustValidator(t)

	// Unset shipping: skip → valid.
	expectValid(t, v, &testpb.OptionalNested{})

	// Set but invalid (Address.street and city are required + min_len=1):
	// recursion still happens, violations bubble up.
	expectInvalid(t, v, &testpb.OptionalNested{
		Shipping: &testpb.Address{}, // missing required street + city
	}, "required")

	// Set and valid: passes.
	expectValid(t, v, &testpb.OptionalNested{
		Shipping: &testpb.Address{Street: "123 Main", City: "Springfield"},
	})
}

// A disabled outer message must short-circuit ALL validation, including
// nested message fields' own rules.
func TestDisabledMessage_SkipsNested(t *testing.T) {
	v := mustValidator(t)

	// name is required, user is a User with its own required rules — both
	// would fail if validated. disabled=true on the outer message must skip
	// everything.
	expectValid(t, v, &testpb.DisabledWithNested{})

	// Even with an explicitly invalid nested User, disabled still skips.
	expectValid(t, v, &testpb.DisabledWithNested{
		User: &testpb.User{Email: "not-an-email"}, // would fail string.email
	})
}
