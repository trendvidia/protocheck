package protocheck

import (
	"math"
	"slices"
	"strings"

	schemav1 "github.com/trendvidia/protos/protowire/schema/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// functionParams maps each function declared in files to the scalar type of
// each parameter, resolved through type aliases. A parameter that is not a
// numeric scalar has an empty entry and is passed as it binds.
func functionParams(files []protoreflect.FileDescriptor) map[string][]string {
	aliases := typeAliases(files)
	params := make(map[string][]string)
	for _, fd := range files {
		fns := readExtension(fd.Options(), schemav1.E_Functions, func() *schemav1.FileFunctions { return &schemav1.FileFunctions{} })
		if fns == nil {
			continue
		}
		for _, fn := range fns.GetDeclarations() {
			types := make([]string, len(fn.GetParams()))
			for i, p := range fn.GetParams() {
				types[i] = resolveParamType(qualifyParamType(p.GetType(), fn.GetName(), aliases), aliases)
			}
			params[fn.GetName()] = types
		}
	}
	return params
}

// typeAliases maps each `type` declared in files to its base type.
func typeAliases(files []protoreflect.FileDescriptor) map[string]string {
	aliases := make(map[string]string)
	for _, fd := range files {
		if decls := readExtension(fd.Options(), schemav1.E_TypeDecls, func() *schemav1.FileTypeDecls { return &schemav1.FileTypeDecls{} }); decls != nil {
			for _, td := range decls.GetDeclarations() {
				aliases[td.GetName()] = td.GetBaseTypeFqn()
			}
		}
	}
	return aliases
}

// qualifyParamType resolves a parameter type against the enclosing scopes of
// the function fqn. protocompile v0.37.0 writes the full name, which passes
// through; a descriptor from an earlier compiler keeps the declared spelling.
func qualifyParamType(name, fqn string, aliases map[string]string) string {
	for dot := strings.LastIndexByte(fqn, '.'); dot >= 0; dot = strings.LastIndexByte(fqn, '.') {
		fqn = fqn[:dot]
		if _, ok := aliases[fqn+"."+name]; ok {
			return fqn + "." + name
		}
	}
	return name
}

// wrapperParamTypes maps the numeric wrapper types to the scalar they unwrap
// to (RFC-001 §6.2).
var wrapperParamTypes = map[string]string{
	"google.protobuf.Int32Value":  "int32",
	"google.protobuf.Int64Value":  "int64",
	"google.protobuf.UInt32Value": "uint32",
	"google.protobuf.UInt64Value": "uint64",
	"google.protobuf.FloatValue":  "float",
	"google.protobuf.DoubleValue": "double",
}

func resolveParamType(fqn string, aliases map[string]string) string {
	for range len(aliases) + 1 {
		switch fqn {
		case "int32", "sint32", "sfixed32", "int64", "sint64", "sfixed64",
			"uint32", "fixed32", "uint64", "fixed64", "float", "double":
			return fqn
		}
		if t, ok := wrapperParamTypes[fqn]; ok {
			return t
		}
		base, ok := aliases[fqn]
		if !ok {
			return ""
		}
		// A base alias may be spelled package-locally.
		if _, ok := aliases[base]; !ok {
			if dot := strings.LastIndexByte(fqn, '.'); dot >= 0 {
				if qualified := fqn[:dot+1] + base; aliases[qualified] != "" {
					base = qualified
				}
			}
		}
		fqn = base
	}
	return ""
}

// convertArg returns v, as an expression bound it, in the Go type of the
// declared numeric type. It reports false when v is not a number or does not
// fit: expression integers are 64-bit, so a literal can overflow a 32-bit
// parameter even though a field of that type cannot.
func convertArg(declared string, v any) (any, bool) {
	var i int64
	var u uint64
	var f float64
	var kind byte
	switch n := v.(type) {
	case int:
		i, kind = int64(n), 'i'
	case int64:
		i, kind = n, 'i'
	case uint64:
		u, kind = n, 'u'
	case float64:
		f, kind = n, 'f'
	default:
		return nil, false
	}
	switch declared {
	case "int32", "sint32", "sfixed32":
		s, ok := asInt64(kind, i, u)
		if !ok || s < math.MinInt32 || s > math.MaxInt32 {
			return nil, false
		}
		return int32(s), true
	case "int64", "sint64", "sfixed64":
		s, ok := asInt64(kind, i, u)
		return s, ok
	case "uint32", "fixed32":
		w, ok := asUint64(kind, i, u)
		if !ok || w > math.MaxUint32 {
			return nil, false
		}
		return uint32(w), true
	case "uint64", "fixed64":
		w, ok := asUint64(kind, i, u)
		return w, ok
	case "float", "double":
		switch kind {
		case 'i':
			f = float64(i)
		case 'u':
			f = float64(u)
		}
		if declared == "double" {
			return f, true
		}
		if !math.IsInf(f, 0) && !math.IsNaN(f) && math.Abs(f) > math.MaxFloat32 {
			return nil, false
		}
		return float32(f), true
	}
	return nil, false
}

func asInt64(kind byte, i int64, u uint64) (int64, bool) {
	switch kind {
	case 'i':
		return i, true
	case 'u':
		return int64(u), u <= math.MaxInt64
	}
	return 0, false
}

func asUint64(kind byte, i int64, u uint64) (uint64, bool) {
	switch kind {
	case 'i':
		return uint64(i), i >= 0
	case 'u':
		return u, true
	}
	return 0, false
}

// convertArgs converts args to the declared parameter types of fqn. The
// declaration wins over how a value happened to bind: a field and a literal of
// one type arrive alike. A count that disagrees with the declaration is left
// for the function's own guards.
func (v *Validator) convertArgs(fqn string, args []any) ([]any, *Violation) {
	p, ok := v.fnParams.Load(fqn)
	if !ok {
		return args, nil
	}
	types := p.([]string)
	if len(types) != len(args) {
		return args, nil
	}
	out := slices.Clone(args)
	for i, t := range types {
		if t == "" {
			continue
		}
		c, ok := convertArg(t, args[i])
		if !ok {
			return nil, &Violation{Code: CodeFunctionInvalidArgument, FallbackMessage: MsgFunctionArgType(fqn, i, t)}
		}
		out[i] = c
	}
	return out, nil
}

// MessageArg returns v, a message argument of a declared function, as the
// generated message type T, such as *pb.Inner. RFC-001 §9.1 passes a generated
// message as itself and a validated dynamic message as a dynamic one, so a
// plain assertion would refuse the dynamic form; generated stubs call this
// instead. A dynamic message is copied through its wire form. It reports false
// when v is not a message of T's type.
func MessageArg[T proto.Message](v any) (T, bool) {
	var zero T
	if t, ok := v.(T); ok {
		return t, true
	}
	m, ok := v.(proto.Message)
	if !ok {
		return zero, false
	}
	dst := zero.ProtoReflect().New()
	if m.ProtoReflect().Descriptor().FullName() != dst.Descriptor().FullName() {
		return zero, false
	}
	b, err := proto.MarshalOptions{AllowPartial: true}.Marshal(m)
	if err != nil {
		return zero, false
	}
	if err := (proto.UnmarshalOptions{AllowPartial: true}).Unmarshal(b, dst.Interface()); err != nil {
		return zero, false
	}
	return dst.Interface().(T), true
}
