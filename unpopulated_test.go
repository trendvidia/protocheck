package protocheck_test

import (
	"slices"
	"testing"

	"github.com/trendvidia/protocheck/v2"
	"github.com/trendvidia/protocheck/v2/internal/testpb"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

// A @validate rule does not run on a field that is not populated and has no
// @default. For a proto3 scalar without `optional`, the zero value is not
// populated, so a range that excludes zero accepts 0. The README's remedy is
// `optional` plus @required (#46).
const unpopulatedSrc = `syntax = "proto3";
package unpopulated;

import "protowire/schema/v1/annotations.proto";

message Tempo {
  float implicit_bpm = 1 @validate(this >= 40.0 && this <= 220.0, code = "tempo.range");
  optional float bpm = 2 @required @validate(this >= 40.0 && this <= 220.0, code = "tempo.range");
}
`

func TestValidateSkipsUnpopulatedScalar(t *testing.T) {
	fd := compileProtos(t, map[string]string{"unpopulated.proto": unpopulatedSrc}, "unpopulated.proto")["unpopulated.proto"]
	v, err := protocheck.New(protocheck.WithDescriptors(fd))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	md := fd.Messages().ByName("Tempo")

	build := func(fields map[string]float32) *dynamicpb.Message {
		m := dynamicpb.NewMessage(md)
		for name, val := range fields {
			m.Set(md.Fields().ByName(protoreflect.Name(name)), protoreflect.ValueOfFloat32(val))
		}
		return m
	}
	report := func(fields map[string]float32) []string {
		var out []string
		for _, viol := range carrierViolations(t, v, build(fields)) {
			out = append(out, viol.Path.String()+" "+viol.Cause.Code)
		}
		return out
	}

	for _, tc := range []struct {
		name   string
		fields map[string]float32
		want   []string
	}{
		// Setting an implicit-presence float to 0 leaves it unpopulated:
		// its range rule is skipped, so 0 passes.
		{"implicit zero is skipped", map[string]float32{"implicit_bpm": 0, "bpm": 90}, nil},
		{"implicit nonzero is checked", map[string]float32{"implicit_bpm": 39, "bpm": 90}, []string{"implicit_bpm tempo.range"}},
		// With presence, unset fails @required and an explicit 0 fails
		// the range.
		{"optional unset fails required", nil, []string{"bpm " + protocheck.CodeRequired}},
		{"optional zero fails range", map[string]float32{"bpm": 0}, []string{"bpm tempo.range"}},
		{"optional in range passes", map[string]float32{"bpm": 90}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := report(tc.fields); !slices.Equal(got, tc.want) {
				t.Errorf("violations = %q, want %q", got, tc.want)
			}
		})
	}
}

// Bracket rules are the contrast: they check an implicit-presence scalar's
// zero value unless an ignore mode says otherwise.
func TestBracketRuleChecksImplicitZero(t *testing.T) {
	v := mustValidator(t)
	expectInvalid(t, v, &testpb.Product{Name: "widget"}, "double.gt")
	expectValid(t, v, &testpb.Product{Name: "widget", Price: 1})
}
