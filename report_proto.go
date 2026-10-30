package protocheck

// Wire-report conversion (RFC-001 goal 5 / #34): Proto renders a Go-native
// Report as the protowire.schema.v1.Report message every port emits, using
// the generated bindings from protocompile's gen package. This is the
// shape gRPC adapters embed in google.rpc.Status.details (§6.6 rule 6)
// and the shape the cross-port conformance golden compares.

import (
	"fmt"
	"reflect"
	"time"

	schemav1 "github.com/trendvidia/protos/protowire/schema/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

// EnumName is a captured enum value by its qualified name
// ("myco.OrderStatus.CANCELLED") — the Go form of the wire Value's
// enum_name kind, kept distinct from plain strings so conversion
// round-trips.
type EnumName string

// Null is an explicitly-null captured value (wire null_value) — distinct
// from nil, which means absent.
type Null struct{}

// Proto converts the report to its protowire.schema.v1.Report wire form.
func (r *Report) Proto() *schemav1.Report {
	out := &schemav1.Report{
		MessageType:   r.MessageType,
		Mode:          schemav1.ExecutionMode(r.Mode),
		Truncated:     r.Truncated,
		WallTimeNanos: r.WallTimeNanos,
	}
	if r.Engine != (EngineInfo{}) {
		out.Engine = &schemav1.EngineInfo{Name: r.Engine.Name, Version: r.Engine.Version}
	}
	for _, viol := range r.Violations {
		out.Violations = append(out.Violations, violationProto(viol))
	}
	return out
}

func violationProto(v *EnrichedViolation) *schemav1.EnrichedViolation {
	out := &schemav1.EnrichedViolation{
		Cause: &schemav1.Violation{
			Code:            v.Cause.Code,
			FallbackMessage: v.Cause.FallbackMessage,
		},
		TypeChain:     v.TypeChain,
		RuleKind:      schemav1.RuleKind(v.RuleKind),
		ValueRedacted: v.ValueRedacted,
		ForKey:        v.ForKey,
	}
	if v.SourceRef != nil {
		out.SourceRef = &schemav1.SourceRef{
			File:           v.SourceRef.File,
			DescriptorPath: v.SourceRef.DescriptorPath,
		}
	}
	if len(v.Cause.Params) > 0 {
		out.Cause.Params = make(map[string]*schemav1.Value, len(v.Cause.Params))
		for name, val := range v.Cause.Params {
			out.Cause.Params[name] = valueProto(val)
		}
	}
	if len(v.Path) > 0 {
		out.Path = &schemav1.FieldPath{}
		for _, seg := range v.Path {
			out.Path.Segments = append(out.Path.Segments, segmentProto(seg))
		}
	}
	if v.ActualValue != nil {
		out.ActualValue = valueProto(v.ActualValue)
	}
	if v.Source != nil {
		out.Source = &schemav1.SourceLocation{
			File:   v.Source.File,
			Line:   v.Source.Line,
			Column: v.Source.Column,
		}
	}
	return out
}

func segmentProto(seg FieldPathSegment) *schemav1.FieldPathSegment {
	out := &schemav1.FieldPathSegment{
		FieldName:   seg.FieldName,
		FieldNumber: seg.FieldNumber,
	}
	switch sub := seg.Subscript.(type) {
	case Index:
		out.Subscript = &schemav1.FieldPathSegment_Index{Index: uint64(sub)}
	case StringKey:
		out.Subscript = &schemav1.FieldPathSegment_StringKey{StringKey: string(sub)}
	case IntKey:
		out.Subscript = &schemav1.FieldPathSegment_IntKey{IntKey: int64(sub)}
	case UintKey:
		out.Subscript = &schemav1.FieldPathSegment_UintKey{UintKey: uint64(sub)}
	case BoolKey:
		out.Subscript = &schemav1.FieldPathSegment_BoolKey{BoolKey: bool(sub)}
	}
	return out
}

// valueProto converts a Go-native captured value (ActualValue, params) to
// the typed protowire.schema.v1.Value. Unrepresentable values degrade to
// their fmt rendering as a string — never dropped silently.
func valueProto(val any) *schemav1.Value {
	switch v := val.(type) {
	case string:
		return &schemav1.Value{Kind: &schemav1.Value_StringValue{StringValue: v}}
	case bool:
		return &schemav1.Value{Kind: &schemav1.Value_BoolValue{BoolValue: v}}
	case int:
		return &schemav1.Value{Kind: &schemav1.Value_IntValue{IntValue: int64(v)}}
	case int32:
		return &schemav1.Value{Kind: &schemav1.Value_IntValue{IntValue: int64(v)}}
	case int64:
		return &schemav1.Value{Kind: &schemav1.Value_IntValue{IntValue: v}}
	case uint32:
		return &schemav1.Value{Kind: &schemav1.Value_UintValue{UintValue: uint64(v)}}
	case uint64:
		return &schemav1.Value{Kind: &schemav1.Value_UintValue{UintValue: v}}
	case float32:
		return &schemav1.Value{Kind: &schemav1.Value_DoubleValue{DoubleValue: float64(v)}}
	case float64:
		return &schemav1.Value{Kind: &schemav1.Value_DoubleValue{DoubleValue: v}}
	case []byte:
		return &schemav1.Value{Kind: &schemav1.Value_BytesValue{BytesValue: v}}
	case time.Time:
		// A Timestamp bound natively (RFC-001 §6.2 rule 2) renders as
		// RFC 3339 rather than time.Time's fmt form.
		return &schemav1.Value{Kind: &schemav1.Value_StringValue{StringValue: v.UTC().Format(time.RFC3339Nano)}}
	case time.Duration:
		return &schemav1.Value{Kind: &schemav1.Value_StringValue{StringValue: v.String()}}
	case EnumName:
		return &schemav1.Value{Kind: &schemav1.Value_EnumName{EnumName: string(v)}}
	case Null:
		return &schemav1.Value{Kind: &schemav1.Value_NullValue{NullValue: schemav1.NullValue_NULL_VALUE}}
	case *anypb.Any:
		// Already Any-wrapped (e.g. round-tripped from the wire): pass
		// through rather than double-wrapping.
		return &schemav1.Value{Kind: &schemav1.Value_MessageValue{MessageValue: v}}
	case proto.Message:
		if packed, err := anypb.New(v); err == nil {
			return &schemav1.Value{Kind: &schemav1.Value_MessageValue{MessageValue: packed}}
		}
	}

	// Typed slices (rule params like []string from `in` rules).
	if rv := reflect.ValueOf(val); rv.Kind() == reflect.Slice {
		list := &schemav1.ListValue{Values: make([]*schemav1.Value, rv.Len())}
		for i := 0; i < rv.Len(); i++ {
			list.Values[i] = valueProto(rv.Index(i).Interface())
		}
		return &schemav1.Value{Kind: &schemav1.Value_ListValue{ListValue: list}}
	}

	return &schemav1.Value{Kind: &schemav1.Value_StringValue{StringValue: fmt.Sprintf("%v", val)}}
}

// ─── Wire → Go ──────────────────────────────────────────────────────────────

// ReportFromProto converts a wire Report back to the Go-native form.
// Converting a Go report to the wire and back is lossless on the wire
// (Proto ∘ ReportFromProto is the identity on wire Reports).
func ReportFromProto(in *schemav1.Report) *Report {
	if in == nil {
		return nil
	}
	out := &Report{
		MessageType:   in.GetMessageType(),
		Mode:          ExecutionMode(in.GetMode()),
		Truncated:     in.GetTruncated(),
		WallTimeNanos: in.GetWallTimeNanos(),
	}
	if e := in.GetEngine(); e != nil {
		out.Engine = EngineInfo{Name: e.GetName(), Version: e.GetVersion()}
	}
	for _, viol := range in.GetViolations() {
		out.Violations = append(out.Violations, violationFromProto(viol))
	}
	return out
}

func violationFromProto(in *schemav1.EnrichedViolation) *EnrichedViolation {
	out := &EnrichedViolation{
		Cause:         &Violation{},
		TypeChain:     in.GetTypeChain(),
		RuleKind:      RuleKind(in.GetRuleKind()),
		ValueRedacted: in.GetValueRedacted(),
		ForKey:        in.GetForKey(),
	}
	if ref := in.GetSourceRef(); ref != nil {
		out.SourceRef = &SourceRef{File: ref.GetFile(), DescriptorPath: ref.GetDescriptorPath()}
	}
	if c := in.GetCause(); c != nil {
		out.Cause.Code = c.GetCode()
		out.Cause.FallbackMessage = c.GetFallbackMessage()
		if params := c.GetParams(); len(params) > 0 {
			out.Cause.Params = make(map[string]any, len(params))
			for name, val := range params {
				out.Cause.Params[name] = valueFromProto(val)
			}
		}
	}
	for _, seg := range in.GetPath().GetSegments() {
		out.Path = append(out.Path, segmentFromProto(seg))
	}
	if av := in.GetActualValue(); av != nil {
		out.ActualValue = valueFromProto(av)
	}
	if src := in.GetSource(); src != nil {
		out.Source = &SourceLocation{File: src.GetFile(), Line: src.GetLine(), Column: src.GetColumn()}
	}
	return out
}

func segmentFromProto(in *schemav1.FieldPathSegment) FieldPathSegment {
	out := FieldPathSegment{
		FieldName:   in.GetFieldName(),
		FieldNumber: in.GetFieldNumber(),
	}
	switch sub := in.GetSubscript().(type) {
	case *schemav1.FieldPathSegment_Index:
		out.Subscript = Index(sub.Index)
	case *schemav1.FieldPathSegment_StringKey:
		out.Subscript = StringKey(sub.StringKey)
	case *schemav1.FieldPathSegment_IntKey:
		out.Subscript = IntKey(sub.IntKey)
	case *schemav1.FieldPathSegment_UintKey:
		out.Subscript = UintKey(sub.UintKey)
	case *schemav1.FieldPathSegment_BoolKey:
		out.Subscript = BoolKey(sub.BoolKey)
	}
	return out
}

// valueFromProto converts a wire Value to the Go-native representation
// valueProto emits for it, so wire → Go → wire round-trips.
func valueFromProto(in *schemav1.Value) any {
	switch v := in.GetKind().(type) {
	case *schemav1.Value_StringValue:
		return v.StringValue
	case *schemav1.Value_IntValue:
		return v.IntValue
	case *schemav1.Value_UintValue:
		return v.UintValue
	case *schemav1.Value_DoubleValue:
		return v.DoubleValue
	case *schemav1.Value_BoolValue:
		return v.BoolValue
	case *schemav1.Value_BytesValue:
		return v.BytesValue
	case *schemav1.Value_EnumName:
		return EnumName(v.EnumName)
	case *schemav1.Value_NullValue:
		return Null{}
	case *schemav1.Value_MessageValue:
		return v.MessageValue
	case *schemav1.Value_ListValue:
		out := make([]any, len(v.ListValue.GetValues()))
		for i, el := range v.ListValue.GetValues() {
			out[i] = valueFromProto(el)
		}
		return out
	default:
		return nil
	}
}
