package protocheck

// Runtime evaluation of indexed carrier rules (RFC-001 §6.4): the walk
// consults the carrierIndex per visited message and evaluates @required,
// @default substitution, and @validate rules, producing wire-aligned
// enriched violations. Repeated and map fields dispatch per element —
// maps on both dimensions, entry keys (ForKey) as well as values
// (fixture 15, #53).

import (
	"sort"
	"time"

	"github.com/trendvidia/protowire-go/encoding/pxf"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// callFunction dispatches a declared function by FQN against the RFC-001
// §9.2 registry. An unregistered FQN behaves like an Unimplemented
// placeholder — reachable only when the schema skipped init-time
// verification.
func (v *Validator) callFunction(fqn string, args []any) (bool, *Violation) {
	v.spiMu.Lock()
	rf, ok := v.spiFuncs[fqn]
	v.spiMu.Unlock()
	if !ok {
		return UnimplementedFunction(fqn)(args)
	}
	args, viol := v.convertArgs(fqn, args)
	if viol != nil {
		return false, viol
	}
	return rf.impl(args)
}

// validateCarrierRules evaluates the carrier rules indexed for msg's type.
func (v *Validator) validateCarrierRules(now time.Time, carrier *carrierIndex, path FieldPath, msg protoreflect.Message) []*EnrichedViolation {
	mr := carrier.messageOf(msg.Descriptor().FullName())
	if mr == nil {
		return nil
	}
	var violations []*EnrichedViolation

	// ── Message-level @validate: `this` binds to the message ────────────
	for _, rule := range mr.checks {
		if viol := v.evalCarrierRule(now, rule, msg.Interface(), path, nil, nil); viol != nil {
			violations = append(violations, viol)
			if v.failFast {
				return violations
			}
		}
	}

	// ── Field rules ─────────────────────────────────────────────────────
	fields := msg.Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		fr := mr.fields[fd.Name()]
		if fr == nil {
			continue
		}
		// @sensitive (RFC-001 §6.7): field-level, alias-expanded, or
		// inherited from the message classification.
		sensitive := fr.sensitive || mr.sensitive
		fieldPath := path.child(segmentOf(fd))
		populated := msg.Has(fd)

		if fr.required != nil && !populated {
			violations = append(violations, &EnrichedViolation{
				Cause: &Violation{
					Code:            CodeRequired,
					FallbackMessage: MsgRequired,
				},
				Path:      fieldPath,
				RuleKind:  RuleKindRequired,
				Source:    fr.required.source,
				SourceRef: fr.required.ref,
			})
			if v.failFast {
				return violations
			}
			continue
		}

		if len(fr.checks) == 0 {
			continue
		}

		// ── Collections (RFC-001 §6.4): alias-expanded rules apply per
		// element with `this` bound to each element and subscripted
		// paths; directly-written rules apply to the collection as a
		// whole. Empty collections still run collection-level rules.
		// For maps, value-type alias rules lower onto the map field's
		// own AnnotationList (protocompile ≥ v0.19.0) and dispatch per
		// entry value; key-type alias rules stay on the synthetic
		// entry's key field (grafted into fr.keyChecks at indexing)
		// and dispatch per entry key with ForKey set.
		if fd.IsList() {
			list := msg.Get(fd).List()
			keyFd := pxf.KeyField(fd)
			for _, rule := range fr.checks {
				if rule.kind == RuleKindTypeRefinement {
					for j := 0; j < list.Len(); j++ {
						elemVal := list.Get(j)
						elemPath := fieldPath.subscript(listSubscript(keyFd, elemVal, j))
						this := elementBinding(fd, elemVal)
						if viol := redactIf(sensitive, v.evalCarrierRule(now, rule, this, elemPath, fr.typeChain, this)); viol != nil {
							violations = append(violations, viol)
							if v.failFast {
								return violations
							}
						}
					}
				} else {
					if viol := v.evalCarrierRule(now, rule, listBinding(fd, list), fieldPath, fr.typeChain, nil); viol != nil {
						violations = append(violations, viol)
						if v.failFast {
							return violations
						}
					}
				}
			}
			continue
		}
		if fd.IsMap() {
			m := msg.Get(fd).Map()
			keyKind := fd.MapKey().Kind()
			valFd := fd.MapValue()

			// Per-entry, both dimensions (§6.4): key rules (from the
			// synthetic entry's key field, ForKey set) before value
			// rules, following the entry's field order — a key and a
			// value violation of the same entry share the subscripted
			// path and are told apart by ForKey (§7). Entries iterate
			// in sorted key order so reports are deterministic.
			for _, k := range sortedMapKeys(m) {
				elemPath := fieldPath.subscript(mapKeySubscript(k, keyKind))
				keyThis := protoValueToGo(k.Value(), keyKind)
				for _, rule := range fr.keyChecks {
					viol := redactIf(sensitive, v.evalCarrierRule(now, rule, keyThis, elemPath, fr.keyTypeChain, keyThis))
					if viol != nil {
						viol.ForKey = true
						violations = append(violations, viol)
						if v.failFast {
							return violations
						}
					}
				}
				this := elementBinding(valFd, m.Get(k))
				for _, rule := range fr.checks {
					if rule.kind != RuleKindTypeRefinement {
						continue
					}
					if viol := redactIf(sensitive, v.evalCarrierRule(now, rule, this, elemPath, fr.typeChain, this)); viol != nil {
						violations = append(violations, viol)
						if v.failFast {
							return violations
						}
					}
				}
			}

			// Collection-level rules run after the per-entry dimension
			// (fr.checks orders refinements first per §6.4).
			for _, rule := range fr.checks {
				if rule.kind == RuleKindTypeRefinement {
					continue
				}
				if viol := v.evalCarrierRule(now, rule, mapBinding(fd, m), fieldPath, fr.typeChain, nil); viol != nil {
					violations = append(violations, viol)
					if v.failFast {
						return violations
					}
				}
			}
			continue
		}

		// ── Singular fields ─────────────────────────────────────────
		// Bind `this`: the field value; on absence, the @default
		// substitute (rules then run against it, per the annotation
		// library contract); otherwise rules are skipped.
		var this any
		substituted := false
		switch {
		case populated && fd.Kind() == protoreflect.MessageKind:
			this = bindMessage(msg.Get(fd).Message())
		case populated:
			this = bindScalar(fd, msg.Get(fd))
		case fr.hasDefault:
			this = fr.defaultValue
			substituted = true
		default:
			continue
		}

		for _, rule := range fr.checks {
			if viol := redactIf(sensitive, v.evalCarrierRule(now, rule, this, fieldPath, fr.typeChain, this)); viol != nil {
				if substituted {
					// RFC-001 §6.4: a rule failing against a
					// @default-substituted value marks a schema-authoring
					// error — the declared default itself violates the
					// field's rules. RULE_KIND_DEFAULT supersedes the
					// rule's own VALIDATE / TYPE_REFINEMENT kind; rule
					// origin stays recoverable from Cause.Code and
					// TypeChain.
					viol.RuleKind = RuleKindDefault
				}
				violations = append(violations, viol)
				if v.failFast {
					return violations
				}
			}
		}
	}
	return violations
}

// redactIf withholds a violation's captured value when the field is
// classified @sensitive (RFC-001 §6.7): ActualValue is cleared and
// ValueRedacted set, distinguishing redaction from genuine absence.
// Violations that never captured a value (required, collection-level
// rules) pass through unchanged.
func redactIf(sensitive bool, viol *EnrichedViolation) *EnrichedViolation {
	if viol == nil || !sensitive || viol.ActualValue == nil {
		return viol
	}
	viol.ActualValue = nil
	viol.ValueRedacted = true
	return viol
}

// sortedMapKeys returns m's keys in ascending order — protoreflect map
// iteration is randomized, and per-entry violations must land in a
// deterministic order for report equality. Map keys are integral, bool,
// or string (never float/bytes), so ordering is total.
func sortedMapKeys(m protoreflect.Map) []protoreflect.MapKey {
	keys := make([]protoreflect.MapKey, 0, m.Len())
	m.Range(func(k protoreflect.MapKey, _ protoreflect.Value) bool {
		keys = append(keys, k)
		return true
	})
	sort.Slice(keys, func(i, j int) bool {
		switch a := keys[i].Interface().(type) {
		case bool:
			return !a && keys[j].Bool()
		case int32:
			return a < int32(keys[j].Int())
		case int64:
			return a < keys[j].Int()
		case uint32:
			return a < uint32(keys[j].Uint())
		case uint64:
			return a < keys[j].Uint()
		case string:
			return a < keys[j].String()
		}
		return false
	})
	return keys
}

// elementBinding binds one collection element for expression evaluation.
// (For maps, pass the value field descriptor — fd.MapValue().)
func elementBinding(fd protoreflect.FieldDescriptor, elem protoreflect.Value) any {
	if fd.Kind() == protoreflect.MessageKind {
		return bindMessage(elem.Message())
	}
	return bindScalar(fd, elem)
}

// bindScalar binds a non-message value of fd's kind. An enum binds as its
// number together with its enum type (RFC-001 §6.2 rule 6), so defined()
// can ask whether the number is declared; every other kind binds as
// protoValueToGo does.
func bindScalar(fd protoreflect.FieldDescriptor, val protoreflect.Value) any {
	if fd.Kind() == protoreflect.EnumKind {
		return enumValue{num: int64(val.Enum()), enum: fd.Enum()}
	}
	return protoValueToGo(val, fd.Kind())
}

// bindMessage binds a set message-typed value as `this` per RFC-001 §6.2:
// a wrapper binds as its unwrapped scalar (rule 1), a Timestamp as
// time.Time and a Duration as time.Duration (rule 2), and every other
// message — Any included (rule 3) — as the structured message itself
// (rule 4). Unset fields never reach here (§6.1).
func bindMessage(m protoreflect.Message) any {
	md := m.Descriptor()
	fields := md.Fields()
	switch md.FullName() {
	case "google.protobuf.Timestamp":
		secs := m.Get(fields.ByName("seconds")).Int()
		nanos := m.Get(fields.ByName("nanos")).Int()
		return time.Unix(secs, nanos).UTC()
	case "google.protobuf.Duration":
		secs := m.Get(fields.ByName("seconds")).Int()
		nanos := m.Get(fields.ByName("nanos")).Int()
		return time.Duration(secs)*time.Second + time.Duration(nanos)*time.Nanosecond
	case "google.protobuf.StringValue", "google.protobuf.BytesValue",
		"google.protobuf.BoolValue",
		"google.protobuf.Int32Value", "google.protobuf.Int64Value",
		"google.protobuf.UInt32Value", "google.protobuf.UInt64Value",
		"google.protobuf.FloatValue", "google.protobuf.DoubleValue":
		value := fields.ByName("value")
		return protoValueToGo(m.Get(value), value.Kind())
	}
	return m.Interface()
}

// listBinding binds a whole repeated field as []any for collection-level
// rules (`this.size() > 0`, ...).
func listBinding(fd protoreflect.FieldDescriptor, list protoreflect.List) any {
	out := make([]any, list.Len())
	for j := 0; j < list.Len(); j++ {
		out[j] = elementBinding(fd, list.Get(j))
	}
	return out
}

// mapBinding binds a whole map field as map[string]any keyed by the dotted
// subscript rendering, for collection-level rules.
func mapBinding(fd protoreflect.FieldDescriptor, m protoreflect.Map) any {
	keyKind := fd.MapKey().Kind()
	valKind := fd.MapValue().Kind()
	out := make(map[string]any, m.Len())
	m.Range(func(k protoreflect.MapKey, val protoreflect.Value) bool {
		var bound any
		if valKind == protoreflect.MessageKind {
			bound = val.Message().Interface()
		} else {
			bound = bindScalar(fd.MapValue(), val)
		}
		out[mapKeySubscript(k, keyKind).String()] = bound
		return true
	})
	return out
}

// evalCarrierRule evaluates one @validate rule and, on failure, builds the
// enriched violation with the RFC-001 §7 code ladder: use-site override,
// else the function-returned code, else the engine-synthesized fallback.
func (v *Validator) evalCarrierRule(now time.Time, rule *carrierRule, this any, path FieldPath, typeChain []string, actual any) *EnrichedViolation {
	ok, cause, err := rule.expr.eval(this, now, v.callFunction)
	if err != nil {
		cause = &Violation{Code: "expression.error", FallbackMessage: err.Error()}
	} else if ok {
		return nil
	}

	out := &Violation{}
	if cause != nil {
		*out = *cause
	}
	if rule.code != "" {
		out.Code = rule.code
	} else if out.Code == "" {
		out.Code = rule.synthCode
	}
	if rule.message != "" {
		out.FallbackMessage = rule.message
	} else if out.FallbackMessage == "" {
		out.FallbackMessage = "expression \"" + rule.expr.src + "\" failed"
	}

	return &EnrichedViolation{
		Cause:       out,
		Path:        path,
		TypeChain:   typeChain,
		RuleKind:    rule.kind,
		ActualValue: reportedValue(actual),
		Source:      rule.source,
		SourceRef:   rule.ref,
	}
}
