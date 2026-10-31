package protocheck

import (
	"fmt"
	"strings"

	schemav1 "github.com/trendvidia/protos/protowire/schema/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"
)

// This file implements the init-time function verification of RFC-001 §9.2:
// New() walks the descriptors passed via WithDescriptors and their imports,
// extracts every function FQN the schema depends on — `function` declarations
// from the file-scope carrier (extension 1328) and call sites recorded in
// lowered annotation expressions (extension 1327, Expression.calls) — and checks
// each against the registered implementations. Missing-impl default is
// lenient: an Unimplemented placeholder that fails at first call.
// WithStrictValidation turns misses into New() errors, and then calls each
// declared function once to find the ones only an Unimplemented stub
// implements.
//
// Engine builtins are never verified here: the compiler only records call
// sites that resolve to a visible `function` declaration (RFC-001 §8.1), so
// Expression.calls is complete exactly for the declared functions this walk
// is responsible for.
//
// Carrier payloads decode because this package links the generated
// protowire.schema.v1 types into the global registry: descriptors
// unmarshaled in-process resolve extensions 1327/1328 instead of parking
// them in unknown fields.

// UnimplementedFunction returns the placeholder implementation installed for
// fqn when lenient verification finds no registered impl (RFC-001 §9.2). It
// fails every call with the reserved CodeFunctionUnimplemented (§7),
// mirroring the UnimplementedFunctions codegen contract of RFC-001 §9.3.
func UnimplementedFunction(fqn string) Function {
	return func(args []any) (bool, *Violation) {
		return false, &Violation{
			Code:            CodeFunctionUnimplemented,
			FallbackMessage: MsgFunctionUnimplemented(fqn),
		}
	}
}

// requiredFunc is one function FQN the descriptor walk found, with the
// source location that names it (declaration site or call site) for
// error reporting.
type requiredFunc struct {
	fqn string
	loc *schemav1.SourceLocation
}

func (r requiredFunc) String() string {
	if r.loc == nil || r.loc.GetFile() == "" {
		return r.fqn
	}
	return fmt.Sprintf("%s (%s:%d:%d)", r.fqn, r.loc.GetFile(), r.loc.GetLine(), r.loc.GetColumn())
}

// verifyRegisteredFunctions walks fd, collects every declared or called
// function FQN, and reconciles the set against registry. In strict mode a
// miss is an error; in lenient mode a miss installs an Unimplemented
// placeholder in registry.
func verifyRegisteredFunctions(fd protoreflect.FileDescriptor, registry map[string]*registeredFunc, strict bool) error {
	required := collectRequiredFunctions(fd)

	var missing []requiredFunc
	seen := make(map[string]int) // fqn → index into missing
	for _, req := range required {
		if _, ok := registry[req.fqn]; ok {
			continue
		}
		if i, ok := seen[req.fqn]; ok {
			// Prefer a located occurrence for reporting: declarations
			// carry no location yet, call sites do.
			if missing[i].loc == nil && req.loc != nil {
				missing[i] = req
			}
			continue
		}
		seen[req.fqn] = len(missing)
		missing = append(missing, req)
	}
	if len(missing) == 0 {
		return nil
	}

	if strict {
		names := make([]string, len(missing))
		for i, m := range missing {
			names[i] = m.String()
		}
		return fmt.Errorf("protocheck: strict validation: %d function(s) without a registered implementation: %s",
			len(missing), strings.Join(names, ", "))
	}

	for _, m := range missing {
		registry[m.fqn] = &registeredFunc{impl: UnimplementedFunction(m.fqn), placeholder: true}
	}
	return nil
}

// verifyFunctionsImplemented calls each function files declare once, through
// the path a rule's call takes, with zero values of its declared parameter
// types (RFC-001 §9.2). The helper registers a method inherited from an
// embedded Unimplemented stub like any other, so its name in the registry says
// nothing, and the stub's reserved code is the only sign of it. Functions are
// pure (§6.5), so the call is safe and its result is discarded.
func (v *Validator) verifyFunctionsImplemented(files []protoreflect.FileDescriptor) error {
	types := newDeclaredTypes(files)
	var stubs []string
	for _, fd := range files {
		fns := readExtension(fd.Options(), schemav1.E_Functions, func() *schemav1.FileFunctions { return &schemav1.FileFunctions{} })
		for _, fn := range fns.GetDeclarations() {
			args := make([]any, len(fn.GetParams()))
			for i, p := range fn.GetParams() {
				args[i] = types.zeroArg(p.GetType(), fn.GetName())
			}
			if v.isStub(fn.GetName(), args) {
				stubs = append(stubs, requiredFunc{fqn: fn.GetName(), loc: fn.GetLocation()}.String())
			}
		}
	}
	if len(stubs) > 0 {
		return fmt.Errorf("protocheck: strict validation: %d function(s) implemented only by an Unimplemented stub: %s",
			len(stubs), strings.Join(stubs, ", "))
	}
	return nil
}

// isStub reports whether calling fqn with args returned the reserved code. A
// real implementation may refuse a zero value, by a violation or a panic, and
// that says nothing about whether it exists.
func (v *Validator) isStub(fqn string, args []any) (stub bool) {
	defer func() {
		if recover() != nil {
			stub = false
		}
	}()
	_, viol := v.callFunction(fqn, args)
	return viol != nil && viol.Code == CodeFunctionUnimplemented
}

// declaredTypes resolves the parameter types of function declarations.
type declaredTypes struct {
	aliases map[string]string
	types   map[string]protoreflect.Descriptor
}

func newDeclaredTypes(files []protoreflect.FileDescriptor) declaredTypes {
	d := declaredTypes{aliases: typeAliases(files), types: make(map[string]protoreflect.Descriptor)}
	addEnums := func(enums protoreflect.EnumDescriptors) {
		for i := 0; i < enums.Len(); i++ {
			d.types[string(enums.Get(i).FullName())] = enums.Get(i)
		}
	}
	var addMessages func(protoreflect.MessageDescriptors)
	addMessages = func(messages protoreflect.MessageDescriptors) {
		for i := 0; i < messages.Len(); i++ {
			md := messages.Get(i)
			d.types[string(md.FullName())] = md
			addEnums(md.Enums())
			addMessages(md.Messages())
		}
	}
	for _, fd := range files {
		addEnums(fd.Enums())
		addMessages(fd.Messages())
	}
	return d
}

// zeroArg returns the zero value of the type a declaration in the scope of
// fqn names, as a rule's call binds it, so that callFunction converts it as
// it would a field. A type it cannot resolve gives nil, which a stub's guard
// lets through because such a parameter generates as any.
func (d declaredTypes) zeroArg(name, fqn string) any {
	for range len(d.aliases) + 1 {
		switch name {
		case "string":
			return ""
		case "bool":
			return false
		case "bytes":
			return []byte{}
		case "int32", "sint32", "sfixed32", "int64", "sint64", "sfixed64",
			"uint32", "fixed32", "uint64", "fixed64", "float", "double":
			return int64(0)
		}
		full := d.resolve(name, fqn)
		if base, ok := d.aliases[full]; ok {
			name, fqn = base, full
			continue
		}
		switch t := d.types[full].(type) {
		case protoreflect.EnumDescriptor:
			return 0
		case protoreflect.MessageDescriptor:
			return bindMessage(newMessage(t))
		}
		return nil
	}
	return nil
}

// resolve finds what name means in the enclosing scopes of fqn.
func (d declaredTypes) resolve(name, fqn string) string {
	for dot := strings.LastIndexByte(fqn, '.'); dot >= 0; dot = strings.LastIndexByte(fqn, '.') {
		fqn = fqn[:dot]
		candidate := fqn + "." + name
		if _, ok := d.aliases[candidate]; ok {
			return candidate
		}
		if _, ok := d.types[candidate]; ok {
			return candidate
		}
	}
	return name
}

// newMessage returns an empty message of md: its generated type when one is
// linked, as a generated function's assertion expects, and a dynamic message
// otherwise.
func newMessage(md protoreflect.MessageDescriptor) protoreflect.Message {
	if mt, err := protoregistry.GlobalTypes.FindMessageByName(md.FullName()); err == nil {
		return mt.New()
	}
	return dynamicpb.NewMessage(md)
}

// collectRequiredFunctions gathers function FQNs from the file's declaration
// carrier (1328) and from every annotation-carried expression's call list
// (1327) across the descriptor tree.
func collectRequiredFunctions(fd protoreflect.FileDescriptor) []requiredFunc {
	var required []requiredFunc

	// ── File-scope `function` declarations (extension 1328) ─────────────
	if decls := readExtension(fd.Options(), schemav1.E_Functions, func() *schemav1.FileFunctions { return &schemav1.FileFunctions{} }); decls != nil {
		for _, d := range decls.GetDeclarations() {
			required = append(required, requiredFunc{fqn: d.GetName(), loc: d.GetLocation()})
		}
	}

	// ── Annotation call sites (extension 1327) ──────────────────────────
	collect := func(list *schemav1.AnnotationList) {
		for _, ann := range list.GetEntries() {
			for _, arg := range ann.GetArgs() {
				expr := arg.GetExpression()
				if expr == nil {
					continue
				}
				loc := expr.GetLocation()
				if loc == nil {
					loc = ann.GetLocation()
				}
				for _, call := range expr.GetCalls() {
					required = append(required, requiredFunc{fqn: call.GetFqn(), loc: loc})
				}
			}
		}
	}
	annotations := func(opts protoreflect.ProtoMessage, ext protoreflect.ExtensionType) {
		if list := readExtension(opts, ext, func() *schemav1.AnnotationList { return &schemav1.AnnotationList{} }); list != nil {
			collect(list)
		}
	}

	annotations(fd.Options(), schemav1.E_FileAnnotations)

	var walkMessage func(md protoreflect.MessageDescriptor)
	walkEnum := func(ed protoreflect.EnumDescriptor) {
		annotations(ed.Options(), schemav1.E_EnumAnnotations)
		vals := ed.Values()
		for i := 0; i < vals.Len(); i++ {
			annotations(vals.Get(i).Options(), schemav1.E_EnumValueAnnotations)
		}
	}
	walkMessage = func(md protoreflect.MessageDescriptor) {
		annotations(md.Options(), schemav1.E_MessageAnnotations)
		fields := md.Fields()
		for i := 0; i < fields.Len(); i++ {
			annotations(fields.Get(i).Options(), schemav1.E_FieldAnnotations)
		}
		oneofs := md.Oneofs()
		for i := 0; i < oneofs.Len(); i++ {
			annotations(oneofs.Get(i).Options(), schemav1.E_OneofAnnotations)
		}
		enums := md.Enums()
		for i := 0; i < enums.Len(); i++ {
			walkEnum(enums.Get(i))
		}
		nested := md.Messages()
		for i := 0; i < nested.Len(); i++ {
			walkMessage(nested.Get(i))
		}
	}

	messages := fd.Messages()
	for i := 0; i < messages.Len(); i++ {
		walkMessage(messages.Get(i))
	}
	enums := fd.Enums()
	for i := 0; i < enums.Len(); i++ {
		walkEnum(enums.Get(i))
	}
	services := fd.Services()
	for i := 0; i < services.Len(); i++ {
		sd := services.Get(i)
		annotations(sd.Options(), schemav1.E_ServiceAnnotations)
		methods := sd.Methods()
		for j := 0; j < methods.Len(); j++ {
			annotations(methods.Get(j).Options(), schemav1.E_MethodAnnotations)
		}
	}

	return required
}
