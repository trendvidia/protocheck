package protocheck

import (
	"fmt"
	"strings"
)

// This file defines the violation model of RFC-001 §7, mirroring the wire
// shapes pinned in protowire's proto/schema/v1/report.proto: Violation is
// the engine-independent cause a validation function returns (§6.5), and
// EnrichedViolation wraps it with the context only the engine knows — the
// structured field path, rule kind, and (as later milestones land) type
// chain and source location.

// Violation is the engine-independent failure value (wire
// protowire.schema.v1.Violation). Function authors populate a stable code,
// structured params, and a fallback message; everything the function cannot
// know lives in EnrichedViolation.
type Violation struct {
	// Code is stable and machine-readable, e.g. "string.min_len" or
	// "user.invalid_country". Codes beginning with "protowire." are
	// reserved for spec-defined violations.
	Code string

	// Params carries structured parameters for locale-catalog
	// interpolation, keyed by the rule's parameter name
	// ({"pattern": ..., "min_len": ...}).
	Params map[string]any

	// FallbackMessage is the engine-author default, used on catalog miss.
	FallbackMessage string
}

// RuleKind classifies the construct that produced a violation. Values match
// protowire.schema.v1.RuleKind.
type RuleKind int32

const (
	RuleKindUnspecified    RuleKind = 0
	RuleKindValidate       RuleKind = 1
	RuleKindRequired       RuleKind = 2
	RuleKindDefault        RuleKind = 3
	RuleKindTypeRefinement RuleKind = 4
)

func (k RuleKind) String() string {
	switch k {
	case RuleKindValidate:
		return "VALIDATE"
	case RuleKindRequired:
		return "REQUIRED"
	case RuleKindDefault:
		return "DEFAULT"
	case RuleKindTypeRefinement:
		return "TYPE_REFINEMENT"
	default:
		return "UNSPECIFIED"
	}
}

// EnrichedViolation wraps a Violation with engine context (wire
// protowire.schema.v1.EnrichedViolation).
type EnrichedViolation struct {
	Cause *Violation

	// Path addresses the offending value inside the message instance.
	Path FieldPath

	// TypeChain is the refinement provenance, base first
	// (["string", "myco.commons.Email"]). Populated once type-alias
	// evaluation lands.
	TypeChain []string

	RuleKind RuleKind

	// ForKey marks a violation against a map entry's key rather than the
	// value Path addresses (wire EnrichedViolation.for_key, protowire
	// v1.3.0): a map subscript always addresses the entry's value, so
	// without this flag a key violation and a value violation on the same
	// entry would carry identical paths.
	ForKey bool

	// ActualValue is the offending value (wire actual_value). nil means
	// the field was absent — for REQUIRED violations, absence is the
	// violation, so there is no value to capture — or redacted; see
	// ValueRedacted.
	ActualValue any

	// ValueRedacted is true when the violated field is classified
	// @sensitive (RFC-001 §6.7): the engine withheld the offending value,
	// leaving ActualValue nil. Distinguishes redaction from genuine
	// absence (wire value_redacted).
	ValueRedacted bool

	// Source is the rule's position in the originating .proto source,
	// from the embedded source map (carrier 1331). nil when the
	// descriptor carries no source map (pre-v1.2 toolchains) or the rule
	// came from protocheck's own option extensions.
	Source *SourceLocation

	// SourceRef keys the rule that produced this violation by
	// (source-map file, descriptor path) in the canonical RFC-001 §8.3.1
	// grammar — the join key protolsp and other tooling use to map
	// violations back to editor ranges. nil under the same conditions as
	// Source.
	SourceRef *SourceRef
}

// SourceLocation is a position in a .proto source file (wire
// protowire.schema.v1.SourceLocation). Line and Column are 1-indexed;
// Column counts Unicode code points.
type SourceLocation struct {
	File   string
	Line   int32
	Column int32
}

func (l *SourceLocation) String() string {
	return fmt.Sprintf("%s:%d:%d", l.File, l.Line, l.Column)
}

// SourceRef identifies the lowered construct that produced a violation by
// its source-map key (RFC-001 §8.3.1): the originating file plus the
// canonical descriptor path, e.g.
// "myco.User.email[protowire.schema.v1.validate#1]". DescriptorPath is
// produced by formatting descpath.Path, never hand-assembled, so it
// round-trips through the shared parser.
type SourceRef struct {
	File           string
	DescriptorPath string
}

func (v *EnrichedViolation) String() string {
	var b strings.Builder
	if len(v.Path) > 0 {
		b.WriteString(v.Path.String())
		if v.ForKey {
			b.WriteString(" (key)")
		}
		b.WriteString(": ")
	}
	b.WriteString(v.Cause.FallbackMessage)
	fmt.Fprintf(&b, " [%s]", v.Cause.Code)
	return b.String()
}

// ─── Field paths ────────────────────────────────────────────────────────────

// FieldPath addresses a value inside a message instance (wire
// protowire.schema.v1.FieldPath). It is structured — not a dotted string —
// so map keys and repeated indices are unambiguous; the dotted rendering
// from String is derived, never parsed back.
type FieldPath []FieldPathSegment

// FieldPathSegment is one step of a FieldPath. Subscript is set when the
// segment addresses one element of a repeated field or map.
type FieldPathSegment struct {
	FieldName   string
	FieldNumber int32
	Subscript   Subscript // nil when the segment addresses the field itself
}

// Subscript addresses one element of a repeated field or map. Map keys are
// typed — never coerced through strings. The implementations are Index,
// StringKey, IntKey, UintKey, and BoolKey.
type Subscript interface {
	isSubscript()
	String() string
}

// Index addresses a repeated-field element.
type Index uint64

// StringKey addresses a map<string, V> entry.
type StringKey string

// IntKey addresses a map<int32|int64|sint*|sfixed*, V> entry.
type IntKey int64

// UintKey addresses a map<uint32|uint64|fixed*, V> entry.
type UintKey uint64

// BoolKey addresses a map<bool, V> entry.
type BoolKey bool

func (Index) isSubscript()     {}
func (StringKey) isSubscript() {}
func (IntKey) isSubscript()    {}
func (UintKey) isSubscript()   {}
func (BoolKey) isSubscript()   {}

func (i Index) String() string     { return fmt.Sprintf("%d", uint64(i)) }
func (k StringKey) String() string { return string(k) }
func (k IntKey) String() string    { return fmt.Sprintf("%d", int64(k)) }
func (k UintKey) String() string   { return fmt.Sprintf("%d", uint64(k)) }
func (k BoolKey) String() string   { return fmt.Sprintf("%t", bool(k)) }

// String derives the human-readable dotted rendering, e.g.
// "address.zip_code", "items[3]", or "attrs[env]".
func (p FieldPath) String() string {
	var b strings.Builder
	for i, seg := range p {
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(seg.FieldName)
		if seg.Subscript != nil {
			b.WriteByte('[')
			b.WriteString(seg.Subscript.String())
			b.WriteByte(']')
		}
	}
	return b.String()
}

// child returns a new FieldPath extending p with seg. The copy keeps sibling
// walks from aliasing one backing array.
func (p FieldPath) child(seg FieldPathSegment) FieldPath {
	out := make(FieldPath, len(p)+1)
	copy(out, p)
	out[len(p)] = seg
	return out
}

// subscript returns a copy of p whose final segment addresses one element of
// the (repeated or map) field p currently ends on.
func (p FieldPath) subscript(s Subscript) FieldPath {
	out := make(FieldPath, len(p))
	copy(out, p)
	out[len(out)-1].Subscript = s
	return out
}

// ─── Validation error ───────────────────────────────────────────────────────

// ValidationError is returned by Validate when one or more constraints are
// violated.
type ValidationError struct {
	Violations []*EnrichedViolation
}

func (e *ValidationError) Error() string {
	switch len(e.Violations) {
	case 0:
		return "validation failed"
	case 1:
		return "validation failed: " + e.Violations[0].String()
	default:
		var b strings.Builder
		fmt.Fprintf(&b, "validation failed with %d violations:", len(e.Violations))
		for _, v := range e.Violations {
			b.WriteString("\n  - ")
			b.WriteString(v.String())
		}
		return b.String()
	}
}

// newViolation builds a VALIDATE-kind enriched violation without params.
func newViolation(path FieldPath, code, msgFmt string, args ...any) *EnrichedViolation {
	return newViolationP(path, code, nil, msgFmt, args...)
}

// newViolationP builds a VALIDATE-kind enriched violation carrying params.
func newViolationP(path FieldPath, code string, params map[string]any, msgFmt string, args ...any) *EnrichedViolation {
	return &EnrichedViolation{
		Cause: &Violation{
			Code:            code,
			Params:          params,
			FallbackMessage: fmt.Sprintf(msgFmt, args...),
		},
		Path:     path,
		RuleKind: RuleKindValidate,
	}
}

// newRequiredViolation builds a REQUIRED-kind enriched violation.
func newRequiredViolation(path FieldPath, msg string) *EnrichedViolation {
	return &EnrichedViolation{
		Cause:    &Violation{Code: "required", FallbackMessage: msg},
		Path:     path,
		RuleKind: RuleKindRequired,
	}
}
