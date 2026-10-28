package protocheck

import (
	"fmt"
	"strings"
	"time"

	pb "github.com/trendvidia/protocheck/v2/protocheckpb"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// This file defines the pluggable expression-engine seam (RFC-001 §9.4: a
// project selects one engine at validator-binary build time). protocheckpb
// Check rules carry engine-language source (§5.1); protocheck core walks
// messages, owns the violation model, and delegates expression evaluation
// to the configured ExpressionEngine. Engines live in separate modules, so
// this core module carries no engine dependency. (RFC-001 carrier
// @validate rules are unaffected: their dialect is evaluated in-process by
// exprlang.go.)

// ExpressionEngine evaluates engine-language expressions from protocheckpb
// Check rules. Implementations must be safe for concurrent use; Validate
// may evaluate expressions from multiple goroutines.
type ExpressionEngine interface {
	// Info identifies the engine in Report.Engine.
	Info() EngineInfo

	// EvalField evaluates src with `this` bound to a scalar field value.
	// The boolean is the rule outcome (true = pass); the error is reserved
	// for engine failure (compile error, unsupported kind, eval fault) and
	// is surfaced to the report verbatim as a violation message.
	EvalField(val protoreflect.Value, kind protoreflect.Kind, src string) (bool, error)

	// EvalMessage evaluates src with the message's fields bound by name.
	EvalMessage(msg protoreflect.Message, src string) (bool, error)

	// Close releases engine resources (pooled VMs, caches). Idempotent.
	Close() error
}

// WithExpressionEngine selects the engine that evaluates protocheckpb
// Check expressions (RFC-001 §9.4). Without one, Check expressions are
// written in, and evaluated as, the RFC-001 §5.4 expression language that
// @validate rules use: `this` is the field value, or the whole message for
// a message-level rule, and functions registered with WithFunction are
// callable by fully-qualified name or by a suffix only one of them has.
func WithExpressionEngine(engine ExpressionEngine) Option {
	return func(cfg *validatorConfig) {
		if cfg.err != nil {
			return
		}
		if engine == nil {
			cfg.err = fmt.Errorf("WithExpressionEngine: nil engine")
			return
		}
		if cfg.exprEngine != nil {
			cfg.err = fmt.Errorf("WithExpressionEngine: engine already configured")
			return
		}
		cfg.exprEngine = engine
	}
}

// engineInfo identifies the producing engine for reports: the configured
// expression engine's identity, or protocheck core's own when validation
// runs engine-free (structural + carrier rules only).
func (v *Validator) engineInfo() EngineInfo {
	if v.exprEngine != nil {
		return v.exprEngine.Info()
	}
	return EngineInfo{Name: "protocheck-go", Version: coreVersion()}
}

// protoValueToGo converts a protoreflect.Value to a plain Go value for
// expression bindings (the RFC-001 carrier dialect and engines alike bind
// scalars as int/uint64/float64/bool/string).
func protoValueToGo(val protoreflect.Value, kind protoreflect.Kind) any {
	switch kind {
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return int(val.Int())
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return val.Uint()
	case protoreflect.FloatKind:
		return float64(val.Float())
	case protoreflect.DoubleKind:
		return val.Float()
	case protoreflect.BoolKind:
		return val.Bool()
	case protoreflect.StringKind:
		return val.String()
	case protoreflect.EnumKind:
		return int(val.Enum())
	case protoreflect.BytesKind:
		return val.Bytes()
	default:
		return nil
	}
}

// evalFieldExpression applies one Check expression to a scalar field value
// through the configured engine, or the built-in language without one,
// mapping the outcome to the violation model: evaluation failure and rule
// failure both surface as violations under the rule's constraint id.
func (v *Validator) evalFieldExpression(now time.Time, path FieldPath, val protoreflect.Value, kind protoreflect.Kind, expr *pb.Expression) *EnrichedViolation {
	if v.exprEngine == nil {
		return v.evalBuiltinCheck(now, path, protoValueToGo(val, kind), expr)
	}
	pass, err := v.exprEngine.EvalField(val, kind, expr.GetExpression())
	if err != nil {
		return newViolation(path, expr.GetId(), "%v", err)
	}
	if pass {
		return nil
	}
	return checkFailed(path, expr, nil)
}

// evalMessageFieldExpression applies a field-level Check expression to a
// message-typed field value. The built-in language binds `this` per RFC-001
// §6.2 (bindMessage). ExpressionEngine.EvalField takes scalar values only, so
// with an engine the rule fails with an error naming the engine rather than
// passing unevaluated (#68).
func (v *Validator) evalMessageFieldExpression(now time.Time, path FieldPath, msg protoreflect.Message, expr *pb.Expression) *EnrichedViolation {
	if v.exprEngine == nil {
		return v.evalBuiltinCheck(now, path, bindMessage(msg), expr)
	}
	return newViolation(path, expr.GetId(),
		"expression engine %s cannot evaluate a field-level check on a message-typed field", v.exprEngine.Info().Name)
}

// evalElementExpression applies a Check expression to a message-typed
// collection element: a repeated item or a map value. An engine binds the
// element's fields by name, as for a message rule; the built-in language
// binds `this` per RFC-001 §6.2 (bindMessage), as for a singular field (#73).
func (v *Validator) evalElementExpression(now time.Time, path FieldPath, msg protoreflect.Message, expr *pb.Expression) *EnrichedViolation {
	if v.exprEngine == nil {
		return v.evalBuiltinCheck(now, path, bindMessage(msg), expr)
	}
	return v.evalMessageExpression(now, path, msg, expr)
}

// evalMessageExpression is evalFieldExpression's message-scope counterpart:
// an engine binds the message's fields by name; the built-in language binds
// the message as `this`.
func (v *Validator) evalMessageExpression(now time.Time, path FieldPath, msg protoreflect.Message, expr *pb.Expression) *EnrichedViolation {
	if v.exprEngine == nil {
		return v.evalBuiltinCheck(now, path, msg.Interface(), expr)
	}
	pass, err := v.exprEngine.EvalMessage(msg, expr.GetExpression())
	if err != nil {
		return newViolation(path, expr.GetId(), "%v", err)
	}
	if pass {
		return nil
	}
	return checkFailed(path, expr, nil)
}

// evalBuiltinCheck evaluates a Check expression in the RFC-001 §5.4
// language, compiling it on first use.
func (v *Validator) evalBuiltinCheck(now time.Time, path FieldPath, this any, expr *pb.Expression) *EnrichedViolation {
	src := expr.GetExpression()
	var compiled *compiledExpr
	if c, ok := v.checkExprs.Load(src); ok {
		compiled = c.(*compiledExpr)
	} else {
		c, err := compileCheckExpr(src, v.checkCallees())
		if err != nil {
			return newViolation(path, expr.GetId(), "%v", err)
		}
		v.checkExprs.Store(src, c)
		compiled = c
	}
	pass, cause, err := compiled.eval(this, now, v.callFunction)
	if err != nil {
		return newViolation(path, expr.GetId(), "%v", err)
	}
	if pass {
		return nil
	}
	return checkFailed(path, expr, cause)
}

// checkFailed builds the violation for a Check rule that evaluated false:
// the rule's own message, else a function-returned cause's, else a
// synthesized one.
func checkFailed(path FieldPath, expr *pb.Expression, cause *Violation) *EnrichedViolation {
	msg := expr.GetMessage()
	if msg == "" && cause != nil {
		msg = cause.FallbackMessage
	}
	if msg == "" {
		msg = fmt.Sprintf("expression %q failed", expr.GetExpression())
	}
	return newViolationP(path, expr.GetId(), params("expression", expr.GetExpression()), "%s", msg)
}

// checkCallees maps the spellings a Check expression may use for a
// registered function to its FQN: the FQN itself, and every dot-boundary
// suffix that no other registered function shares.
func (v *Validator) checkCallees() map[string]string {
	v.spiMu.Lock()
	defer v.spiMu.Unlock()
	owners := make(map[string][]string)
	for fqn := range v.spiFuncs {
		owners[fqn] = append(owners[fqn], fqn)
		for i := strings.IndexByte(fqn, '.'); i >= 0; {
			suffix := fqn[i+1:]
			if suffix != fqn {
				owners[suffix] = append(owners[suffix], fqn)
			}
			j := strings.IndexByte(suffix, '.')
			if j < 0 {
				break
			}
			i += j + 1
		}
	}
	callees := make(map[string]string, len(owners))
	for spelling, fqns := range owners {
		if len(fqns) == 1 {
			callees[spelling] = fqns[0]
		}
	}
	return callees
}
