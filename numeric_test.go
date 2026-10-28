package protocheck_test

import (
	"math"
	"testing"

	"github.com/trendvidia/protocheck/v2/internal/testpb"
)

func TestNumericRules(t *testing.T) {
	v := mustValidator(t)

	tests := []struct {
		name  string
		msg   *testpb.NumericShapes
		valid bool
		id    string
	}{
		// ── float ────────────────────────────────────────────────────────
		{name: "float const match", msg: &testpb.NumericShapes{Weight: 1.5}, valid: true},
		{name: "float const mismatch", msg: &testpb.NumericShapes{Weight: 2.5}, valid: false, id: "float.const"},

		{name: "float finite ok", msg: &testpb.NumericShapes{Temperature: 36.6}, valid: true},
		{name: "float finite +inf", msg: &testpb.NumericShapes{Temperature: float32(math.Inf(1))}, valid: false, id: "float.finite"},
		{name: "float finite -inf", msg: &testpb.NumericShapes{Temperature: float32(math.Inf(-1))}, valid: false, id: "float.finite"},
		{name: "float finite nan", msg: &testpb.NumericShapes{Temperature: float32(math.NaN())}, valid: false, id: "float.finite"},

		{name: "float in member", msg: &testpb.NumericShapes{Voltage: 5.0}, valid: true},
		{name: "float in non-member", msg: &testpb.NumericShapes{Voltage: 9.0}, valid: false, id: "float.in"},

		{name: "float not_in clear", msg: &testpb.NumericShapes{Current: 10.0}, valid: true},
		{name: "float not_in hit", msg: &testpb.NumericShapes{Current: 1.0}, valid: false, id: "float.not_in"},

		// ── double ───────────────────────────────────────────────────────
		{name: "double const match", msg: &testpb.NumericShapes{Pi: 3.14}, valid: true},
		{name: "double const mismatch", msg: &testpb.NumericShapes{Pi: 3.15}, valid: false, id: "double.const"},

		{name: "double finite ok", msg: &testpb.NumericShapes{Mass: 1.0}, valid: true},
		{name: "double finite +inf", msg: &testpb.NumericShapes{Mass: math.Inf(1)}, valid: false, id: "double.finite"},
		{name: "double finite nan", msg: &testpb.NumericShapes{Mass: math.NaN()}, valid: false, id: "double.finite"},

		// ── int32 ────────────────────────────────────────────────────────
		{name: "int32 const match", msg: &testpb.NumericShapes{Code32: 42}, valid: true},
		{name: "int32 const mismatch", msg: &testpb.NumericShapes{Code32: 43}, valid: false, id: "int32.const"},

		// ── int64 ────────────────────────────────────────────────────────
		{name: "int64 gte/lte inside", msg: &testpb.NumericShapes{Timestamp: 500}, valid: true},
		{name: "int64 gte/lte below", msg: &testpb.NumericShapes{Timestamp: -1}, valid: false, id: "int64.gte"},
		{name: "int64 gte/lte above", msg: &testpb.NumericShapes{Timestamp: 1000001}, valid: false, id: "int64.lte"},

		{name: "int64 const match (negative)", msg: &testpb.NumericShapes{Seq: -7}, valid: true},
		{name: "int64 const mismatch", msg: &testpb.NumericShapes{Seq: 7}, valid: false, id: "int64.const"},

		{name: "int64 in member", msg: &testpb.NumericShapes{Score: 20}, valid: true},
		{name: "int64 in non-member", msg: &testpb.NumericShapes{Score: 25}, valid: false, id: "int64.in"},

		{name: "int64 not_in clear", msg: &testpb.NumericShapes{AntiScore: -5}, valid: true},
		{name: "int64 not_in hit", msg: &testpb.NumericShapes{AntiScore: -1}, valid: false, id: "int64.not_in"},

		{name: "int64 lt below", msg: &testpb.NumericShapes{StrictLt: 99}, valid: true},
		{name: "int64 lt equal", msg: &testpb.NumericShapes{StrictLt: 100}, valid: false, id: "int64.lt"},
		{name: "int64 gt above", msg: &testpb.NumericShapes{StrictGt: 101}, valid: true},
		{name: "int64 gt equal", msg: &testpb.NumericShapes{StrictGt: 100}, valid: false, id: "int64.gt"},

		// ── uint32 ───────────────────────────────────────────────────────
		{name: "uint32 const match", msg: &testpb.NumericShapes{Code32U: 100}, valid: true},
		{name: "uint32 const mismatch", msg: &testpb.NumericShapes{Code32U: 99}, valid: false, id: "uint32.const"},

		// ── uint64 ───────────────────────────────────────────────────────
		{name: "uint64 gte/lte inside", msg: &testpb.NumericShapes{Bignum: 500}, valid: true},
		{name: "uint64 gte/lte above", msg: &testpb.NumericShapes{Bignum: 1000001}, valid: false, id: "uint64.lte"},

		{name: "uint64 const match (high)", msg: &testpb.NumericShapes{Bigconst: 18446744073709551613}, valid: true},
		{name: "uint64 const mismatch", msg: &testpb.NumericShapes{Bigconst: 18446744073709551612}, valid: false, id: "uint64.const"},

		{name: "uint64 in member", msg: &testpb.NumericShapes{Port: 443}, valid: true},
		{name: "uint64 in non-member", msg: &testpb.NumericShapes{Port: 9000}, valid: false, id: "uint64.in"},

		{name: "uint64 not_in clear", msg: &testpb.NumericShapes{BlockedPort: 80}, valid: true},
		{name: "uint64 not_in hit", msg: &testpb.NumericShapes{BlockedPort: 22}, valid: false, id: "uint64.not_in"},

		{name: "uint64 lt below", msg: &testpb.NumericShapes{StrictUlt: 99}, valid: true},
		{name: "uint64 lt equal", msg: &testpb.NumericShapes{StrictUlt: 100}, valid: false, id: "uint64.lt"},
		{name: "uint64 gt above", msg: &testpb.NumericShapes{StrictUgt: 101}, valid: true},
		{name: "uint64 gt equal", msg: &testpb.NumericShapes{StrictUgt: 100}, valid: false, id: "uint64.gt"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.valid {
				expectValid(t, v, tc.msg)
			} else {
				expectInvalid(t, v, tc.msg, tc.id)
			}
		})
	}
}

func TestBoolConst(t *testing.T) {
	v := mustValidator(t)
	tru, fls := true, false

	// Unset → IGNORE_IF_UNPOPULATED skips, valid.
	expectValid(t, v, &testpb.BoolConsts{})

	// agreed expects const=true.
	expectValid(t, v, &testpb.BoolConsts{Agreed: &tru})
	expectInvalid(t, v, &testpb.BoolConsts{Agreed: &fls}, "bool.const")

	// optout expects const=false.
	expectValid(t, v, &testpb.BoolConsts{Optout: &fls})
	expectInvalid(t, v, &testpb.BoolConsts{Optout: &tru}, "bool.const")
}

func TestEnumRules_ConstAndNotIn(t *testing.T) {
	v := mustValidator(t)

	// Unset (STATUS_UNSPECIFIED = 0) → IGNORE_IF_UNPOPULATED skips, valid.
	expectValid(t, v, &testpb.EnumConsts{})

	// preferred expects STATUS_ACTIVE (1); STATUS_INACTIVE (2) fails const.
	expectValid(t, v, &testpb.EnumConsts{Preferred: testpb.Status_STATUS_ACTIVE})
	expectInvalid(t, v, &testpb.EnumConsts{Preferred: testpb.Status_STATUS_INACTIVE}, "enum.const")

	// blocked must not be STATUS_INACTIVE (2); ACTIVE (1) is fine.
	expectValid(t, v, &testpb.EnumConsts{Blocked: testpb.Status_STATUS_ACTIVE})
	expectInvalid(t, v, &testpb.EnumConsts{Blocked: testpb.Status_STATUS_INACTIVE}, "enum.not_in")
}
