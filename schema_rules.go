package protocheck

// Carrier-rule index: the descriptor-load half of RFC-001 §6.4 validation
// for schemas lowered by the protowire compiler. New() walks the files
// supplied via WithDescriptors and indexes, per message, the rules carried
// in the lowered annotation lists (extension 50400) — @required, @default,
// and @validate uses of the protowire.schema.v1 annotation library —
// pre-parsing every rule expression. Each rule is joined with the embedded
// source map (extension 50404) by its canonical descriptor path (RFC-001
// §8.3.1, formatted via fdp.DescriptorPath, never hand-assembled), giving
// violations their Source and SourceRef enrichment.
//
// Type-alias provenance: the compiler macro-expands a type alias's rules
// into the use-site field's annotation list. Each expanded entry keeps the
// alias declaration's source location, which is matched against the alias's
// own annotation list (carrier 50403) to attribute the rule to its alias —
// those violations report RuleKindTypeRefinement and the field's type
// chain. Rules evaluate base → derived → field-level (RFC-001 §6.4),
// regardless of the emitted list order.

import (
	"fmt"
	"strings"

	"github.com/trendvidia/protocompile/fdp"
	schemav1 "github.com/trendvidia/protos/protowire/schema/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Standard annotation FQNs of the protowire.schema.v1 annotation library.
const (
	annValidate  = "protowire.schema.v1.validate"
	annRequired  = "protowire.schema.v1.required"
	annDefault   = "protowire.schema.v1.default"
	annSensitive = "protowire.schema.v1.sensitive"
)

// carrierIndex holds every carrier rule of the descriptors passed to New,
// keyed by message full name.
type carrierIndex struct {
	messages map[protoreflect.FullName]*messageCarrierRules
}

func (ci *carrierIndex) messageOf(name protoreflect.FullName) *messageCarrierRules {
	if ci == nil {
		return nil
	}
	return ci.messages[name]
}

type messageCarrierRules struct {
	checks []*carrierRule
	fields map[protoreflect.Name]*fieldCarrierRules

	// sensitive marks a message whose fields are all @sensitive (RFC-001
	// §6.7) — set directly or inherited transitively through
	// message-typed fields of a sensitive message.
	sensitive bool
}

type fieldCarrierRules struct {
	required     *carrierRule
	defaultValue any
	hasDefault   bool
	sensitive    bool // @sensitive on the field (or alias-expanded onto it)
	checks       []*carrierRule
	typeChain    []string // base-first, including the root primitive

	// Map fields only (RFC-001 §6.4): key-type alias rules, carried by
	// the §8 lowering on the synthetic entry's key field and evaluated
	// per entry key with ForKey set. keyTypeChain is the KEY type's
	// chain; the map field's own typeChain belongs to the value
	// dimension.
	keyChecks    []*carrierRule
	keyTypeChain []string
}

// carrierRule is one pre-compiled @validate (or the meta of a @required).
type carrierRule struct {
	expr      *compiledExpr
	code      string // use-site override
	message   string // use-site override
	synthCode string // engine-synthesized fallback per RFC-001 §7
	kind      RuleKind
	source    *SourceLocation
	ref       *SourceRef
}

// ─── Index construction ─────────────────────────────────────────────────────

type indexBuilder struct {
	typeDecls map[string]*schemav1.TypeDecl // alias FQN → decl
	fnCodes   map[string]string             // function FQN → [error_code] option
	out       *carrierIndex
}

// buildCarrierIndex indexes every message of the given files. Files are
// processed as one unit so cross-file aliases and function declarations
// resolve (RFC-001 fixtures 06).
func buildCarrierIndex(files []protoreflect.FileDescriptor) (*carrierIndex, error) {
	if len(files) == 0 {
		return nil, nil
	}
	b := &indexBuilder{
		typeDecls: make(map[string]*schemav1.TypeDecl),
		fnCodes:   make(map[string]string),
		out:       &carrierIndex{messages: make(map[protoreflect.FullName]*messageCarrierRules)},
	}

	// Pass 1: file-scope declarations, cross-file.
	for _, fd := range files {
		if decls := readExtension(fd.Options(), schemav1.E_TypeDecls, func() *schemav1.FileTypeDecls { return &schemav1.FileTypeDecls{} }); decls != nil {
			for _, td := range decls.GetDeclarations() {
				b.typeDecls[td.GetName()] = td
			}
		}
		if fns := readExtension(fd.Options(), schemav1.E_Functions, func() *schemav1.FileFunctions { return &schemav1.FileFunctions{} }); fns != nil {
			for _, fn := range fns.GetDeclarations() {
				if arg := fn.GetOptions()["error_code"]; arg != nil {
					b.fnCodes[fn.GetName()] = arg.GetStringValue()
				}
			}
		}
	}

	// Pass 2: message rules.
	for _, fd := range files {
		sm := readExtension(fd.Options(), schemav1.E_SourceMap, func() *schemav1.SourceMap { return &schemav1.SourceMap{} })
		entries := make(map[string]*schemav1.SourceEntry)
		smFile := ""
		if sm != nil {
			smFile = sm.GetFile()
			for _, e := range sm.GetEntries() {
				entries[e.GetDescriptorPath()] = e
			}
		}
		msgs := fd.Messages()
		for i := 0; i < msgs.Len(); i++ {
			if err := b.indexMessage(msgs.Get(i), smFile, entries); err != nil {
				return nil, err
			}
		}
	}
	if len(b.out.messages) == 0 {
		return nil, nil
	}
	b.propagateSensitivity(files)
	return b.out, nil
}

// propagateSensitivity closes message-level @sensitive over message-typed
// fields (RFC-001 §6.7: "messages (all fields, transitively through
// message-typed fields)"): every message type reachable through the fields
// of a sensitive message is itself sensitive.
func (b *indexBuilder) propagateSensitivity(files []protoreflect.FileDescriptor) {
	byName := make(map[protoreflect.FullName]protoreflect.MessageDescriptor)
	var collect func(md protoreflect.MessageDescriptor)
	collect = func(md protoreflect.MessageDescriptor) {
		byName[md.FullName()] = md
		nested := md.Messages()
		for i := 0; i < nested.Len(); i++ {
			if !nested.Get(i).IsMapEntry() {
				collect(nested.Get(i))
			}
		}
	}
	for _, fd := range files {
		msgs := fd.Messages()
		for i := 0; i < msgs.Len(); i++ {
			collect(msgs.Get(i))
		}
	}

	for changed := true; changed; {
		changed = false
		for name, mr := range b.out.messages {
			if !mr.sensitive {
				continue
			}
			md, ok := byName[name]
			if !ok {
				continue
			}
			fields := md.Fields()
			for i := 0; i < fields.Len(); i++ {
				fd := fields.Get(i)
				var reached protoreflect.MessageDescriptor
				switch {
				case fd.IsMap() && fd.MapValue().Kind() == protoreflect.MessageKind:
					reached = fd.MapValue().Message()
				case !fd.IsMap() && fd.Kind() == protoreflect.MessageKind:
					reached = fd.Message()
				default:
					continue
				}
				sub := b.out.messages[reached.FullName()]
				if sub == nil {
					sub = &messageCarrierRules{fields: make(map[protoreflect.Name]*fieldCarrierRules)}
					b.out.messages[reached.FullName()] = sub
				}
				if !sub.sensitive {
					sub.sensitive = true
					changed = true
				}
			}
		}
	}
}

func (b *indexBuilder) indexMessage(md protoreflect.MessageDescriptor, smFile string, sm map[string]*schemav1.SourceEntry) error {
	mr := &messageCarrierRules{fields: make(map[protoreflect.Name]*fieldCarrierRules)}

	// Message-level @validate and @sensitive.
	if list := readExtension(md.Options(), schemav1.E_MessageAnnotations, func() *schemav1.AnnotationList { return &schemav1.AnnotationList{} }); list != nil {
		ordinals := make(map[string]int)
		for _, ann := range list.GetEntries() {
			ordinal := ordinals[ann.GetName()]
			ordinals[ann.GetName()]++
			switch ann.GetName() {
			case annSensitive:
				mr.sensitive = true
			case annValidate:
				rule, err := b.buildValidateRule(ann, string(md.FullName()), ordinal, smFile, sm)
				if err != nil {
					return fmt.Errorf("message %s: %w", md.FullName(), err)
				}
				if declLoc := declLocation(md); declLoc != nil {
					rule.source = declLoc
				}
				mr.checks = append(mr.checks, rule)
			}
		}
	}

	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		fr, err := b.indexField(fd, smFile, sm)
		if err != nil {
			return err
		}
		// Map key dimension (RFC-001 §6.4): the §8 lowering keeps
		// key-type alias rules on the synthetic entry's key field —
		// unlike value-type rules, which expand onto the map field
		// itself (protocompile ≥ v0.19.0). Graft them onto the map
		// field's rules for per-key dispatch.
		if fd.IsMap() {
			kfr, err := b.indexField(fd.MapKey(), smFile, sm)
			if err != nil {
				return err
			}
			if kfr != nil && len(kfr.checks) > 0 {
				if fr == nil {
					fr = &fieldCarrierRules{}
				}
				fr.keyChecks = kfr.checks
				fr.keyTypeChain = kfr.typeChain
			}
		}
		if fr != nil {
			mr.fields[fd.Name()] = fr
		}
	}

	if len(mr.checks) > 0 || len(mr.fields) > 0 || mr.sensitive {
		b.out.messages[md.FullName()] = mr
	}

	nested := md.Messages()
	for i := 0; i < nested.Len(); i++ {
		if nested.Get(i).IsMapEntry() {
			continue
		}
		if err := b.indexMessage(nested.Get(i), smFile, sm); err != nil {
			return err
		}
	}
	return nil
}

func (b *indexBuilder) indexField(fd protoreflect.FieldDescriptor, smFile string, sm map[string]*schemav1.SourceEntry) (*fieldCarrierRules, error) {
	list := readExtension(fd.Options(), schemav1.E_FieldAnnotations, func() *schemav1.AnnotationList { return &schemav1.AnnotationList{} })
	if list == nil {
		return nil, nil
	}

	fr := &fieldCarrierRules{}
	element := string(fd.FullName())
	// The fixture-07 declaration-site convention (see declLocation)
	// covers singular fields only. Collection rules keep their
	// source-map locations — the fixture-15 golden pins the rule's
	// annotation site (the alias's @validate for per-element rules, the
	// field's own @validate for collection-level rules) — and synthetic
	// map-entry key fields have no user-written declaration to cite.
	var declLoc *SourceLocation
	if !fd.IsList() && !fd.IsMap() && !isMapEntryField(fd) {
		declLoc = declLocation(fd)
	}
	ordinals := make(map[string]int)
	aliasSet := make(map[string]bool)
	type pendingCheck struct {
		rule  *carrierRule
		alias string // "" for a direct field-level rule
	}
	var pending []pendingCheck

	for _, ann := range list.GetEntries() {
		name := ann.GetName()
		ordinal := ordinals[name]
		ordinals[name]++

		switch name {
		case annSensitive:
			fr.sensitive = true

		case annRequired:
			source, ref := b.lookupSource(element, name, ordinal, smFile, sm)
			if declLoc != nil {
				source = declLoc
			}
			fr.required = &carrierRule{kind: RuleKindRequired, source: source, ref: ref}

		case annDefault:
			val, err := defaultArgValue(ann)
			if err != nil {
				return nil, fmt.Errorf("field %s: @default: %w", fd.FullName(), err)
			}
			if n, ok := val.(int); ok && fd.Kind() == protoreflect.EnumKind {
				val = enumValue{num: int64(n), enum: fd.Enum()}
			}
			fr.defaultValue = val
			fr.hasDefault = true

		case annValidate:
			rule, err := b.buildValidateRule(ann, element, ordinal, smFile, sm)
			if err != nil {
				return nil, fmt.Errorf("field %s: %w", fd.FullName(), err)
			}
			if declLoc != nil {
				rule.source = declLoc
			}
			alias := b.attributeAlias(ann)
			if alias != "" {
				rule.kind = RuleKindTypeRefinement
				aliasSet[alias] = true
			}
			pending = append(pending, pendingCheck{rule: rule, alias: alias})
		}
	}

	if fr.required == nil && !fr.hasDefault && !fr.sensitive && len(pending) == 0 {
		return nil, nil
	}

	// Type chain, base-first: root primitive, then aliases derived from it.
	chainOrder := b.aliasChain(aliasSet)
	fr.typeChain = append([]string{baseTypeName(fd)}, chainOrder...)

	// Evaluation order per RFC-001 §6.4: base → derived → field-level,
	// regardless of the emitted list order. Stable within each group.
	rank := func(alias string) int {
		if alias == "" {
			return len(chainOrder) // field-level last
		}
		for i, a := range chainOrder {
			if a == alias {
				return i
			}
		}
		return len(chainOrder)
	}
	for r := 0; r <= len(chainOrder); r++ {
		for _, pc := range pending {
			if rank(pc.alias) == r {
				fr.checks = append(fr.checks, pc.rule)
			}
		}
	}
	return fr, nil
}

// buildValidateRule compiles one @validate use.
func (b *indexBuilder) buildValidateRule(ann *schemav1.Annotation, element string, ordinal int, smFile string, sm map[string]*schemav1.SourceEntry) (*carrierRule, error) {
	var expr *schemav1.Expression
	rule := &carrierRule{kind: RuleKindValidate}
	for _, arg := range ann.GetArgs() {
		switch arg.GetName() {
		case "":
			if e := arg.GetExpression(); e != nil {
				expr = e
			}
		case "code":
			rule.code = arg.GetStringValue()
		case "message":
			rule.message = arg.GetStringValue()
		}
	}
	if expr == nil {
		return nil, fmt.Errorf("@validate #%d carries no expression argument", ordinal)
	}

	calls := make(map[string]string, len(expr.GetCalls()))
	firstCall := ""
	for _, c := range expr.GetCalls() {
		fqn := c.GetFqn()
		if firstCall == "" {
			firstCall = fqn
		}
		calls[fqn] = fqn
		if dot := strings.LastIndexByte(fqn, '.'); dot >= 0 {
			calls[fqn[dot+1:]] = fqn
		}
	}
	enums := make(map[string]int32, len(expr.GetEnumValues()))
	for _, ev := range expr.GetEnumValues() {
		enums[ev.GetSpelling()] = ev.GetValue().GetNumber()
	}
	compiled, err := compileExpr(expr.GetSource(), calls, enums)
	if err != nil {
		return nil, err
	}
	rule.expr = compiled

	// Engine-synthesized code ladder (RFC-001 §7, fixture 05): the failing
	// function's [error_code] option, else a code derived from its FQN,
	// else the generic "validate.failed".
	switch {
	case firstCall != "" && b.fnCodes[firstCall] != "":
		rule.synthCode = b.fnCodes[firstCall]
	case firstCall != "":
		rule.synthCode = firstCall
	default:
		rule.synthCode = "validate.failed"
	}

	rule.source, rule.ref = b.lookupSource(element, annValidate, ordinal, smFile, sm)
	return rule, nil
}

// isMapEntryField reports whether fd belongs to a synthetic map-entry
// message (the key/value fields of a lowered map<K,V>).
func isMapEntryField(fd protoreflect.FieldDescriptor) bool {
	md, ok := fd.Parent().(protoreflect.MessageDescriptor)
	return ok && md.IsMapEntry()
}

// declLocation is the declaration site of a descriptor per the fixture-07
// golden's normative choice: for singular fields,
// EnrichedViolation.source cites the violated element's declaration in
// the consuming file (user.proto's field lines) — not the rule's
// annotation site, which for alias-expanded rules may live in another
// file. Requires the descriptor to retain SourceCodeInfo; nil
// otherwise (callers keep the source-map annotation location as fallback).
func declLocation(d protoreflect.Descriptor) *SourceLocation {
	file := d.ParentFile()
	if file == nil {
		return nil
	}
	loc := file.SourceLocations().ByDescriptor(d)
	if loc.Path == nil {
		return nil
	}
	return &SourceLocation{
		File:   file.Path(),
		Line:   int32(loc.StartLine) + 1,
		Column: int32(loc.StartColumn) + 1,
	}
}

// lookupSource joins a lowered annotation with its source-map entry by the
// canonical descriptor path.
func (b *indexBuilder) lookupSource(element, annotation string, ordinal int, smFile string, sm map[string]*schemav1.SourceEntry) (*SourceLocation, *SourceRef) {
	path := fdp.DescriptorPath{Element: element, Annotation: annotation, Ordinal: ordinal}.String()
	entry, ok := sm[path]
	if !ok {
		return nil, nil
	}
	var loc *SourceLocation
	if sl := entry.GetSourceLocation(); sl != nil {
		loc = &SourceLocation{File: sl.GetFile(), Line: sl.GetLine(), Column: sl.GetColumn()}
	}
	return loc, &SourceRef{File: smFile, DescriptorPath: path}
}

// attributeAlias reports which type alias a field's annotation entry was
// macro-expanded from, by exact source-location match against the alias
// declarations' own annotation lists — "" when the entry was written
// directly on the field.
func (b *indexBuilder) attributeAlias(ann *schemav1.Annotation) string {
	loc := ann.GetLocation()
	if loc == nil {
		return ""
	}
	for fqn, td := range b.typeDecls {
		for _, declAnn := range td.GetAnnotations().GetEntries() {
			if dl := declAnn.GetLocation(); dl != nil &&
				dl.GetFile() == loc.GetFile() && dl.GetLine() == loc.GetLine() && dl.GetColumn() == loc.GetColumn() {
				return fqn
			}
		}
	}
	return ""
}

// aliasChain orders the attributed aliases base-first by following each
// declaration's base_type_fqn link.
func (b *indexBuilder) aliasChain(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	// The derived-most alias is the one no other member derives from.
	bases := make(map[string]bool, len(set))
	for fqn := range set {
		if base := b.resolveBase(fqn); base != "" {
			bases[base] = true
		}
	}
	derived := ""
	for fqn := range set {
		if !bases[fqn] {
			derived = fqn
			break
		}
	}
	if derived == "" { // cycle — malformed; emit in arbitrary order
		out := make([]string, 0, len(set))
		for fqn := range set {
			out = append(out, fqn)
		}
		return out
	}
	// Walk derived → base, then reverse.
	var reversed []string
	for cur := derived; cur != "" && set[cur] && len(reversed) < len(set); cur = b.resolveBase(cur) {
		reversed = append(reversed, cur)
	}
	out := make([]string, len(reversed))
	for i, fqn := range reversed {
		out[len(reversed)-1-i] = fqn
	}
	return out
}

// resolveBase resolves a TypeDecl's base_type_fqn — possibly spelled
// package-locally — to a declared alias FQN, or "" for a concrete base.
func (b *indexBuilder) resolveBase(fqn string) string {
	td, ok := b.typeDecls[fqn]
	if !ok {
		return ""
	}
	base := td.GetBaseTypeFqn()
	if _, ok := b.typeDecls[base]; ok {
		return base
	}
	if dot := strings.LastIndexByte(fqn, '.'); dot >= 0 {
		qualified := fqn[:dot+1] + base
		if _, ok := b.typeDecls[qualified]; ok {
			return qualified
		}
	}
	return ""
}

// baseTypeName is the root of a field's type chain: the declared scalar
// kind, or the message/enum full name.
func baseTypeName(fd protoreflect.FieldDescriptor) string {
	// A map field's Kind is the synthetic entry message; refinement
	// chains on map fields root at the *value* type (value-type alias
	// rules lower onto the map field, protocompile ≥ v0.19.0), so the
	// base is the value field's type. Key aliases stay on the synthetic
	// entry's key field and never reach here.
	if fd.IsMap() {
		fd = fd.MapValue()
	}
	switch fd.Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return string(fd.Message().FullName())
	case protoreflect.EnumKind:
		return string(fd.Enum().FullName())
	default:
		return fd.Kind().String()
	}
}

// defaultArgValue extracts the Go value of a @default(value) argument.
func defaultArgValue(ann *schemav1.Annotation) (any, error) {
	for _, arg := range ann.GetArgs() {
		if arg.GetName() != "" && arg.GetName() != "value" {
			continue
		}
		switch v := arg.GetValue().(type) {
		case *schemav1.AnnotationArg_StringValue:
			return v.StringValue, nil
		case *schemav1.AnnotationArg_IntValue:
			return v.IntValue, nil
		case *schemav1.AnnotationArg_DoubleValue:
			return v.DoubleValue, nil
		case *schemav1.AnnotationArg_BoolValue:
			return v.BoolValue, nil
		case *schemav1.AnnotationArg_BytesValue:
			return v.BytesValue, nil
		case *schemav1.AnnotationArg_Literal:
			if ev := v.Literal.GetEnumValue(); ev != nil {
				return int(ev.GetNumber()), nil
			}
		}
	}
	return nil, fmt.Errorf("unsupported or missing value argument")
}
