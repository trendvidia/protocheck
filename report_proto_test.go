package protocheck_test

// Wire round-trip property (#46, RFC-001 goal 5): Proto ∘ ReportFromProto
// must be the identity on wire Reports — a report parsed off the wire and
// re-emitted is byte-for-byte semantically equal.

import (
	"os"
	"testing"

	"github.com/trendvidia/protocheck/v2"
	schemav1 "github.com/trendvidia/protos/protowire/schema/v1"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
)

// wireReportFixture exercises every Value kind, every subscript type, all
// rule kinds, redaction, truncation, and fail-fast mode.
func wireReportFixture(t *testing.T) *schemav1.Report {
	t.Helper()
	packed, err := anypb.New(&emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	return &schemav1.Report{
		MessageType: "wire.Everything",
		Mode:        schemav1.ExecutionMode_EXECUTION_MODE_FAIL_FAST,
		Truncated:   true,
		Engine:      &schemav1.EngineInfo{Name: "protocheck-go/example", Version: "2.1.0"},
		Violations: []*schemav1.EnrichedViolation{
			{
				Cause: &schemav1.Violation{
					Code:            "wire.kitchen_sink",
					FallbackMessage: "everything at once",
					Params: map[string]*schemav1.Value{
						"s":  {Kind: &schemav1.Value_StringValue{StringValue: "x"}},
						"i":  {Kind: &schemav1.Value_IntValue{IntValue: -3}},
						"u":  {Kind: &schemav1.Value_UintValue{UintValue: 18446744073709551615}},
						"d":  {Kind: &schemav1.Value_DoubleValue{DoubleValue: 2.5}},
						"b":  {Kind: &schemav1.Value_BoolValue{BoolValue: true}},
						"by": {Kind: &schemav1.Value_BytesValue{BytesValue: []byte{1, 2}}},
						"e":  {Kind: &schemav1.Value_EnumName{EnumName: "wire.Status.BAD"}},
						"n":  {Kind: &schemav1.Value_NullValue{NullValue: schemav1.NullValue_NULL_VALUE}},
						"m":  {Kind: &schemav1.Value_MessageValue{MessageValue: packed}},
						"list": {Kind: &schemav1.Value_ListValue{ListValue: &schemav1.ListValue{Values: []*schemav1.Value{
							{Kind: &schemav1.Value_StringValue{StringValue: "a"}},
							{Kind: &schemav1.Value_IntValue{IntValue: 1}},
						}}}},
					},
				},
				Path: &schemav1.FieldPath{Segments: []*schemav1.FieldPathSegment{
					{FieldName: "items", FieldNumber: 1, Subscript: &schemav1.FieldPathSegment_Index{Index: 3}},
					{FieldName: "labels", FieldNumber: 2, Subscript: &schemav1.FieldPathSegment_StringKey{StringKey: "env"}},
					{FieldName: "counts", FieldNumber: 3, Subscript: &schemav1.FieldPathSegment_IntKey{IntKey: -7}},
					{FieldName: "quotas", FieldNumber: 4, Subscript: &schemav1.FieldPathSegment_UintKey{UintKey: 9}},
					{FieldName: "flags", FieldNumber: 5, Subscript: &schemav1.FieldPathSegment_BoolKey{BoolKey: true}},
					{FieldName: "leaf", FieldNumber: 6},
				}},
				TypeChain:   []string{"string", "wire.Alias"},
				ActualValue: &schemav1.Value{Kind: &schemav1.Value_StringValue{StringValue: "bad"}},
				Source:      &schemav1.SourceLocation{File: "wire.proto", Line: 12, Column: 3},
				RuleKind:    schemav1.RuleKind_RULE_KIND_TYPE_REFINEMENT,
				ForKey:      true,
				SourceRef: &schemav1.SourceRef{
					File:           "wire.proto",
					DescriptorPath: "wire.Everything.LabelsEntry.key[protowire.schema.v1.validate#0]",
				},
			},
			{
				Cause:    &schemav1.Violation{Code: "protowire.required", FallbackMessage: "field is required"},
				Path:     &schemav1.FieldPath{Segments: []*schemav1.FieldPathSegment{{FieldName: "phone", FieldNumber: 2}}},
				RuleKind: schemav1.RuleKind_RULE_KIND_REQUIRED,
			},
			{
				Cause:         &schemav1.Violation{Code: "secret.short", FallbackMessage: "too short"},
				Path:          &schemav1.FieldPath{Segments: []*schemav1.FieldPathSegment{{FieldName: "secret", FieldNumber: 3}}},
				RuleKind:      schemav1.RuleKind_RULE_KIND_DEFAULT,
				ValueRedacted: true,
			},
		},
		WallTimeNanos: 12345,
	}
}

func TestWireReportRoundTrip(t *testing.T) {
	want := wireReportFixture(t)
	got := protocheck.ReportFromProto(want).Proto()
	if !proto.Equal(got, want) {
		t.Errorf("round trip is not the identity.\n--- got ---\n%s\n--- want ---\n%s",
			prototext.Format(got), prototext.Format(want))
	}
}

func TestGoldenRoundTrip(t *testing.T) {
	// The vendored conformance golden must also survive the round trip.
	raw, err := readGolden()
	if err != nil {
		t.Fatal(err)
	}
	want := &schemav1.Report{}
	if err := prototext.Unmarshal(raw, want); err != nil {
		t.Fatal(err)
	}
	got := protocheck.ReportFromProto(want).Proto()
	if !proto.Equal(got, want) {
		t.Errorf("golden round trip differs.\n--- got ---\n%s", prototext.Format(got))
	}
}

func TestReportFromProtoNil(t *testing.T) {
	if protocheck.ReportFromProto(nil) != nil {
		t.Error("ReportFromProto(nil) must be nil")
	}
}

func readGolden() ([]byte, error) {
	return os.ReadFile("testdata/schema-extensions/07_report_golden.textproto")
}
