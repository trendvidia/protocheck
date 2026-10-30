// Package protocheck provides protobuf message validation using declarative
// constraints defined in .proto files. It is the engine-agnostic validation
// layer of RFC-001: the message walk, the violation and report model, and
// the §9.1 engine SPI live here; expression evaluation plugs in through
// [ExpressionEngine] (§9.4).
package protocheck

import (
	"time"

	"errors"
	"fmt"
	"sync"

	pb "github.com/trendvidia/protocheck/v2/protocheckpb"
	"github.com/trendvidia/protowire-go/encoding/pxf"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// errNilMessage refuses a nil message: there is nothing to validate, and
// passing it would report a message that does not exist as valid.
var errNilMessage = errors.New("protocheck: nil message")

// Validator validates protobuf messages against protocheck constraints.
type Validator struct {
	exprEngine ExpressionEngine
	failFast   bool
	engine     *spiEngine

	// spiMu guards spiFuncs. Registration must complete before the first
	// Validate; the lock only orders concurrent Register calls.
	spiMu sync.Mutex
	// spiFuncs holds RFC-001 §9.2 function implementations keyed by their
	// schema fully-qualified name, including Unimplemented placeholders
	// installed by lenient init-time verification.
	spiFuncs map[string]*registeredFunc
	// fnParams maps a declared function's FQN to its numeric parameter
	// types ([]string), as functionParams reads them from the descriptors.
	fnParams sync.Map

	// carrier indexes the RFC-001 carrier rules (@required/@default/
	// @validate, extension 1327) of the descriptors passed via
	// WithDescriptors and their imports; nil when none carry rules.
	carrier *carrierIndex
	// listed is the WithDescriptors files and their imports, which carrier
	// was built from; listedPaths holds their paths.
	listed      []protoreflect.FileDescriptor
	listedPaths map[string]bool
	// rootCarriers caches, per file of a validated message, the index that
	// run walks with: a *rootCarrier keyed by protoreflect.FileDescriptor.
	rootCarriers sync.Map

	// checkExprs caches Check expressions compiled for the built-in
	// language (no expression engine), keyed by source.
	checkExprs sync.Map
}

func (v *Validator) storeFunctionParams(files []protoreflect.FileDescriptor) {
	for fqn, types := range functionParams(files) {
		v.fnParams.Store(fqn, types)
	}
}

// registeredFunc is one registry entry. placeholder marks an Unimplemented
// stand-in from lenient verification, which a later Register may replace.
type registeredFunc struct {
	impl        Function
	placeholder bool
}

// New creates a new Validator. Options can register custom functions.
//
// The Validator enforces every rule of a validated message's schema: bracket
// rules and @required/@default/@validate alike, the latter read from the
// descriptor of the message's file and its imports at first use.
//
// When descriptors are supplied via WithDescriptors, New also runs the
// RFC-001 §9.2 init-time verification over them and their imports: every
// function FQN the schema declares or calls must have a registered
// implementation. Misses install Unimplemented placeholders that fail at
// first call — or, with WithStrictValidation, fail New itself. A function
// missing from a file no WithDescriptors reaches fails at first call.
func New(opts ...Option) (*Validator, error) {
	cfg := &validatorConfig{}
	for _, o := range opts {
		o(cfg)
	}
	if cfg.err != nil {
		return nil, cfg.err
	}
	v := &Validator{
		exprEngine: cfg.exprEngine,
		failFast:   cfg.failFast,
		spiFuncs:   make(map[string]*registeredFunc),
	}
	for fqn, impl := range cfg.spiFuncs {
		v.spiFuncs[fqn] = &registeredFunc{impl: impl}
	}
	v.listed = withImports(cfg.descriptors)
	v.listedPaths = make(map[string]bool, len(v.listed))
	for _, fd := range v.listed {
		v.listedPaths[fd.Path()] = true
		if err := verifyRegisteredFunctions(fd, v.spiFuncs, cfg.strict); err != nil {
			return nil, err
		}
	}
	v.storeFunctionParams(v.listed)
	carrier, err := buildCarrierIndex(v.listed)
	if err != nil {
		return nil, err
	}
	v.carrier = carrier
	v.engine = newSPIEngine(v, cfg.catalogs)
	return v, nil
}

// registerFunction records impl under its schema FQN, replacing an
// Unimplemented placeholder if lenient verification installed one.
func (v *Validator) registerFunction(fqn string, impl Function) error {
	if fqn == "" {
		return errors.New("protocheck: Register: empty function FQN")
	}
	if impl == nil {
		return fmt.Errorf("protocheck: Register(%q): nil Function", fqn)
	}
	v.spiMu.Lock()
	defer v.spiMu.Unlock()
	if existing, ok := v.spiFuncs[fqn]; ok && !existing.placeholder {
		return fmt.Errorf("protocheck: Register(%q): already registered", fqn)
	}
	v.spiFuncs[fqn] = &registeredFunc{impl: impl}
	return nil
}

// Close releases the configured expression engine's resources (pooled VMs,
// caches). Call it when the Validator is no longer needed; it must not race
// an in-flight Validate. A Validator used for the whole process lifetime
// need not be Closed. Idempotent.
func (v *Validator) Close() error {
	if v.exprEngine != nil {
		return v.exprEngine.Close()
	}
	return nil
}

// Validate checks the message against all protocheck constraints.
// Returns nil if valid, or a *ValidationError listing all violations. Any
// other error means msg was not validated: it is nil, or a typed nil pointer
// with no message behind it, or its schema carries a rule that could not be
// loaded.
func (v *Validator) Validate(msg proto.Message) error {
	// One instant per run: every now() inside this Validate call sees
	// it (RFC-001 §6.2 rule 5), so a rule cannot pass and fail within
	// one report for equal values.
	now := time.Now()
	if msg == nil {
		return errNilMessage
	}
	root := msg.ProtoReflect()
	if !root.IsValid() {
		return errNilMessage
	}
	carrier, err := v.carrierFor(root.Descriptor().ParentFile())
	if err != nil {
		return err
	}
	violations := v.validate(now, newCarrierRun(carrier, root), nil, root)
	if len(violations) > 0 {
		if v.failFast {
			// The walk short-circuits at loop boundaries but a single rule
			// group may emit several violations; the fail-fast contract is
			// exactly one.
			violations = violations[:1]
		}
		return &ValidationError{Violations: violations}
	}
	return nil
}

// validate recursively validates a message and returns all violations.
func (v *Validator) validate(now time.Time, run *carrierRun, path FieldPath, msg protoreflect.Message) []*EnrichedViolation {
	if !msg.IsValid() {
		return nil
	}

	md := msg.Descriptor()
	var violations []*EnrichedViolation

	// ── RFC-001 carrier rules (@required / @default / @validate) ─────────
	if run.index != nil {
		violations = append(violations, v.validateCarrierRules(now, run, path, msg)...)
		if v.failFast && len(violations) > 0 {
			return violations
		}
	}

	// ── Message-level constraints ────────────────────────────────────────
	if mc := getMessageConstraints(md); mc != nil {
		if mc.Disabled {
			return nil
		}
		for _, expr := range mc.Check {
			if viol := v.evalMessageExpression(now, path, msg, expr); viol != nil {
				violations = append(violations, viol)
				if v.failFast {
					return violations
				}
			}
		}
	}

	// ── Oneof constraints ────────────────────────────────────────────────
	oneofs := md.Oneofs()
	for i := 0; i < oneofs.Len(); i++ {
		od := oneofs.Get(i)
		if od.IsSynthetic() {
			continue // skip proto3 optional synthetic oneofs
		}
		if oc := getOneofConstraints(od); oc != nil {
			if oc.Required && msg.WhichOneof(od) == nil {
				violations = append(violations, newRequiredViolation(
					path.child(FieldPathSegment{FieldName: string(od.Name())}),
					"exactly one field in the oneof must be set",
				))
				if v.failFast {
					return violations
				}
			}
		}
	}

	// ── Field constraints ────────────────────────────────────────────────
	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)

		// Skip non-selected oneof fields: only validate the active member.
		if od := fd.ContainingOneof(); od != nil && !od.IsSynthetic() {
			if msg.WhichOneof(od) != fd {
				continue
			}
		}

		fc := getFieldConstraints(fd)
		if fc == nil {
			// Even without constraints, message-typed values — singular
			// fields, list elements, and map values — are validated
			// against their own message's constraints.
			if containsMessages(fd) {
				violations = append(violations, v.validateField(now, run, path.child(segmentOf(fd)), fd, msg.Get(fd), nil)...)
				if v.failFast && len(violations) > 0 {
					return violations
				}
			}
			continue
		}

		fieldPath := path.child(segmentOf(fd))

		// Ignore handling.
		if fc.Ignore == pb.Ignore_IGNORE_ALWAYS {
			continue
		}

		populated := msg.Has(fd)

		if fc.Required && !populated {
			violations = append(violations, newRequiredViolation(fieldPath, MsgRequired))
			if v.failFast {
				return violations
			}
			continue
		}

		if !populated && fc.Ignore == pb.Ignore_IGNORE_IF_UNPOPULATED {
			continue
		}

		// IGNORE_IF_DEFAULT_VALUE: skip whenever the field's value equals its
		// proto default — including the proto3-optional case where the field
		// is explicitly set to the zero value (populated == true). For message-
		// kind fields protoreflect has no usable Default(), so we fall back to
		// the unpopulated check (which matches protovalidate semantics).
		if fc.Ignore == pb.Ignore_IGNORE_IF_DEFAULT_VALUE {
			if !populated {
				continue
			}
			if fd.Kind() != protoreflect.MessageKind && msg.Get(fd).Equal(fd.Default()) {
				continue
			}
		}

		val := msg.Get(fd)

		violations = append(violations, v.validateField(now, run, fieldPath, fd, val, fc)...)
		if v.failFast && len(violations) > 0 {
			return violations
		}
	}

	return violations
}

// validateField applies type-specific rules and custom expressions to a single field.
func (v *Validator) validateField(now time.Time, run *carrierRun, path FieldPath, fd protoreflect.FieldDescriptor, val protoreflect.Value, fc *pb.FieldConstraints) []*EnrichedViolation {
	var violations []*EnrichedViolation

	// ── Repeated fields ──────────────────────────────────────────────────
	if fd.IsList() {
		list := val.List()
		rr := fc.GetRepeated()
		keyFd := pxf.KeyField(fd)

		if rr != nil {
			violations = append(violations, validateRepeatedRules(list.Len(), rr, path)...)

			// Uniqueness. On a keyed repeated field (protowire (pxf.key),
			// draft -01 §3.13) the assertion is over the designated key
			// field's values: duplicate keys are a codec-level decode error
			// in keyed-block form, but the anonymous list form admits them
			// at the data level. Unkeyed lists keep whole-element
			// uniqueness.
			if rr.Unique && list.Len() > 0 {
				if keyFd != nil {
					seen := make(map[string]bool, list.Len())
					for j := 0; j < list.Len(); j++ {
						key := list.Get(j).Message().Get(keyFd).String()
						if seen[key] {
							violations = append(violations, newViolationP(
								path.subscript(StringKey(key)),
								"repeated.unique", params("key", key),
								"duplicate key %q", key))
							if v.failFast {
								return violations
							}
							continue
						}
						seen[key] = true
					}
				} else {
					seen := make(map[string]bool, list.Len())
					for j := 0; j < list.Len(); j++ {
						key := uniqueKey(list.Get(j).Interface())
						if seen[key] {
							violations = append(violations, newViolation(path, "repeated.unique", "list items must be unique"))
							break
						}
						seen[key] = true
					}
				}
			}
		}

		// Per-element validation. Message elements always validate against
		// their own constraints; keyed elements are addressed by key value
		// — the entry name in PXF sources — falling back to the index when
		// the key is unset. Scalar elements validate against the items
		// rules; message elements additionally evaluate items.check
		// expressions with the element's fields (the key included) in
		// scope.
		for j := 0; j < list.Len(); j++ {
			elemVal := list.Get(j)
			elemPath := path.subscript(listSubscript(keyFd, elemVal, j))
			if fd.Kind() == protoreflect.MessageKind {
				violations = append(violations, v.validate(now, run, elemPath, elemVal.Message())...)
				if rr.GetItems() != nil {
					for _, expr := range rr.GetItems().Check {
						if viol := v.evalElementExpression(now, elemPath, elemVal.Message(), expr); viol != nil {
							violations = append(violations, viol)
						}
					}
				}
			} else if rr.GetItems() != nil {
				violations = append(violations, v.validateScalar(now, elemPath, elemVal, fd.Kind(), rr.GetItems(), fd)...)
			}
			if v.failFast && len(violations) > 0 {
				return violations
			}
		}

		// Field-level custom expressions are not applied to repeated fields.
		return violations
	}

	// ── Map fields ───────────────────────────────────────────────────────
	if fd.IsMap() {
		m := val.Map()
		mr := fc.GetMap()
		if mr != nil {
			violations = append(violations, validateMapRules(m.Len(), mr, path)...)
		}

		keyKind := fd.MapKey().Kind()
		valKind := fd.MapValue().Kind()

		m.Range(func(k protoreflect.MapKey, mval protoreflect.Value) bool {
			entryPath := path.subscript(mapKeySubscript(k, keyKind))

			if mr.GetKeys() != nil {
				keyViolations := v.validateScalar(now, entryPath, k.Value(), keyKind, mr.GetKeys(), fd.MapKey())
				for _, viol := range keyViolations {
					viol.ForKey = true
				}
				violations = append(violations, keyViolations...)
			}
			// Message values always validate against their own
			// constraints, then against the values check rules (#73);
			// scalar values need declared values rules.
			if valKind == protoreflect.MessageKind {
				violations = append(violations, v.validate(now, run, entryPath, mval.Message())...)
				for _, expr := range mr.GetValues().GetCheck() {
					if viol := v.evalElementExpression(now, entryPath, mval.Message(), expr); viol != nil {
						violations = append(violations, viol)
					}
				}
			} else if mr.GetValues() != nil {
				violations = append(violations, v.validateScalar(now, entryPath, mval, valKind, mr.GetValues(), fd.MapValue())...)
			}
			// Stop ranging once fail-fast has its violation.
			return !(v.failFast && len(violations) > 0)
		})
		return violations
	}

	// ── Scalar / message fields ──────────────────────────────────────────

	// For message-typed fields, recurse, then apply the field's own check
	// rules to the message value when it is set (#68; an absent field skips
	// validation, RFC-001 §6.1).
	if fd.Kind() == protoreflect.MessageKind {
		violations = append(violations, v.validate(now, run, path, val.Message())...)
		if !val.Message().IsValid() {
			return violations
		}
		for _, expr := range fc.GetCheck() {
			if viol := v.evalMessageFieldExpression(now, path, val.Message(), expr); viol != nil {
				violations = append(violations, viol)
			}
		}
		return violations
	}

	violations = append(violations, v.validateScalar(now, path, val, fd.Kind(), fc, fd)...)

	return violations
}

// validateScalar applies standard rules and custom expressions to a scalar value.
func (v *Validator) validateScalar(now time.Time, path FieldPath, val protoreflect.Value, kind protoreflect.Kind, fc *pb.FieldConstraints, fd protoreflect.FieldDescriptor) []*EnrichedViolation {
	var violations []*EnrichedViolation

	switch kind {
	case protoreflect.FloatKind:
		if r := fc.GetFloat(); r != nil {
			violations = append(violations, validateFloatRules(float32(val.Float()), r, path)...)
		}
	case protoreflect.DoubleKind:
		if r := fc.GetDouble(); r != nil {
			violations = append(violations, validateDoubleRules(val.Float(), r, path)...)
		}
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		if r := fc.GetInt32(); r != nil {
			violations = append(violations, validateInt32Rules(int32(val.Int()), r, path)...)
		}
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		if r := fc.GetInt64(); r != nil {
			violations = append(violations, validateInt64Rules(val.Int(), r, path)...)
		}
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		if r := fc.GetUint32(); r != nil {
			violations = append(violations, validateUint32Rules(uint32(val.Uint()), r, path)...)
		}
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		if r := fc.GetUint64(); r != nil {
			violations = append(violations, validateUint64Rules(val.Uint(), r, path)...)
		}
	case protoreflect.BoolKind:
		if r := fc.GetBool(); r != nil {
			violations = append(violations, validateBoolRules(val.Bool(), r, path)...)
		}
	case protoreflect.StringKind:
		if r := fc.GetString_(); r != nil {
			violations = append(violations, validateStringRules(val.String(), r, path)...)
		}
	case protoreflect.BytesKind:
		if r := fc.GetBytes(); r != nil {
			violations = append(violations, validateBytesRules(val.Bytes(), r, path)...)
		}
	case protoreflect.EnumKind:
		if r := fc.GetEnum(); r != nil {
			defined := make(map[int32]bool)
			if fd != nil {
				enumDesc := fd.Enum()
				if enumDesc != nil {
					vals := enumDesc.Values()
					for i := 0; i < vals.Len(); i++ {
						defined[int32(vals.Get(i).Number())] = true
					}
				}
			}
			violations = append(violations, validateEnumRules(int32(val.Enum()), r, path, defined)...)
		}
	}

	// Field-level custom expressions.
	for _, expr := range fc.Check {
		if viol := v.evalFieldExpression(now, path, val, kind, expr); viol != nil {
			violations = append(violations, viol)
		}
	}

	return violations
}

// ─── Extension extraction helpers ───────────────────────────────────────────

func getFieldConstraints(fd protoreflect.FieldDescriptor) *pb.FieldConstraints {
	return readExtension(fd.Options(), pb.E_Field, func() *pb.FieldConstraints { return &pb.FieldConstraints{} })
}

func getMessageConstraints(md protoreflect.MessageDescriptor) *pb.MessageConstraints {
	return readExtension(md.Options(), pb.E_Message, func() *pb.MessageConstraints { return &pb.MessageConstraints{} })
}

func getOneofConstraints(od protoreflect.OneofDescriptor) *pb.OneofConstraints {
	return readExtension(od.Options(), pb.E_Oneof, func() *pb.OneofConstraints { return &pb.OneofConstraints{} })
}

// readExtension returns the value of ext on opts, or nil if unset.
//
// When opts came from protoc-gen-go, the stored value is already the concrete
// T and is returned directly. When opts came from descriptors loaded via
// protocompile (or any path that didn't resolve the extension against the
// global type registry), the value is a *dynamicpb.Message; we re-encode it
// and unmarshal into a fresh T. This avoids the type-assertion panic inside
// proto.GetExtension → ExtensionInfo.InterfaceOf for dynamic values.
//
// Descriptors whose options were built without this process's extension
// registry at all — a FileDescriptorSet compiled by an external toolchain,
// or FileDescriptorProtos assembled programmatically (protocompile's fdp)
// — park foreign extensions in unknown fields; those are decoded by field
// number as a fallback.
func readExtension[T proto.Message](opts proto.Message, ext protoreflect.ExtensionType, fresh func() T) T {
	var zero T
	if opts == nil {
		return zero
	}
	m := opts.ProtoReflect()
	xtd := ext.TypeDescriptor()
	if !m.Has(xtd) {
		if payload, ok := unknownFieldBytes(m.GetUnknown(), xtd.Number()); ok {
			dst := fresh()
			if err := proto.Unmarshal(payload, dst); err == nil {
				return dst
			}
		}
		return zero
	}
	raw := m.Get(xtd).Message().Interface()
	if v, ok := raw.(T); ok {
		return v
	}
	b, err := proto.Marshal(raw)
	if err != nil {
		return zero
	}
	dst := fresh()
	if err := proto.Unmarshal(b, dst); err != nil {
		return zero
	}
	return dst
}

// unknownFieldBytes extracts the concatenated payload of a message-typed
// field from an unknown-fields blob. Multiple occurrences concatenate, per
// proto message-merge semantics.
func unknownFieldBytes(unknown protoreflect.RawFields, num protoreflect.FieldNumber) ([]byte, bool) {
	var payload []byte
	found := false
	b := []byte(unknown)
	for len(b) > 0 {
		n, typ, tagLen := protowire.ConsumeTag(b)
		if tagLen < 0 {
			return nil, false
		}
		b = b[tagLen:]
		valLen := protowire.ConsumeFieldValue(n, typ, b)
		if valLen < 0 {
			return nil, false
		}
		if n == num && typ == protowire.BytesType {
			v, _ := protowire.ConsumeBytes(b)
			payload = append(payload, v...)
			found = true
		}
		b = b[valLen:]
	}
	return payload, found
}

// segmentOf builds the FieldPath segment addressing fd.
func segmentOf(fd protoreflect.FieldDescriptor) FieldPathSegment {
	return FieldPathSegment{
		FieldName:   string(fd.Name()),
		FieldNumber: int32(fd.Number()),
	}
}

// containsMessages reports whether fd's values are message instances that
// carry their own constraints: a singular message field, a repeated message
// field, or a map with message values.
func containsMessages(fd protoreflect.FieldDescriptor) bool {
	if fd.IsMap() {
		return fd.MapValue().Kind() == protoreflect.MessageKind
	}
	return fd.Kind() == protoreflect.MessageKind
}

// listSubscript addresses one element of a repeated field: by its key-field
// value for keyed repeated fields — the entry name in PXF sources — and by
// index otherwise (or when the key is unset).
func listSubscript(keyFd protoreflect.FieldDescriptor, elem protoreflect.Value, index int) Subscript {
	if keyFd != nil {
		if m := elem.Message(); m.Has(keyFd) {
			return StringKey(m.Get(keyFd).String())
		}
	}
	return Index(uint64(index))
}

// mapKeySubscript converts a protoreflect map key into its typed Subscript.
func mapKeySubscript(k protoreflect.MapKey, kind protoreflect.Kind) Subscript {
	switch kind {
	case protoreflect.StringKind:
		return StringKey(k.String())
	case protoreflect.BoolKind:
		return BoolKey(k.Bool())
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return IntKey(k.Int())
	default: // uint32/uint64/fixed32/fixed64
		return UintKey(k.Uint())
	}
}
