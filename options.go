package protocheck

import (
	"fmt"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// Option configures a Validator.
type Option func(*validatorConfig)

type validatorConfig struct {
	spiFuncs    map[string]Function
	catalogs    map[string]Catalog
	descriptors []protoreflect.FileDescriptor
	exprEngine  ExpressionEngine
	failFast    bool
	strict      bool
	err         error
}

// WithCatalog installs the message catalog for one locale at construction
// time — the Option counterpart of Engine.RegisterCatalog (RFC-001 §7).
// Report.Format(locale) then renders violation messages through it,
// falling back to each violation's FallbackMessage on a miss.
func WithCatalog(locale string, cat Catalog) Option {
	return func(cfg *validatorConfig) {
		if cfg.err != nil {
			return
		}
		if locale == "" {
			cfg.err = fmt.Errorf("WithCatalog: empty locale")
			return
		}
		if cat == nil {
			cfg.err = fmt.Errorf("WithCatalog(%q): nil Catalog", locale)
			return
		}
		if cfg.catalogs == nil {
			cfg.catalogs = make(map[string]Catalog)
		}
		if _, ok := cfg.catalogs[locale]; ok {
			cfg.err = fmt.Errorf("WithCatalog(%q): already registered", locale)
			return
		}
		cfg.catalogs[locale] = cat
	}
}

// WithFailFast makes the Validator stop at the first violation instead of
// collecting all of them (RFC-001 §6.4 fail-fast mode). Validate then reports
// a ValidationError carrying exactly one Violation, and the message walk
// short-circuits, skipping remaining rules. The default is collect-all.
func WithFailFast() Option {
	return func(cfg *validatorConfig) {
		cfg.failFast = true
	}
}

// WithDescriptors supplies the schema files New verifies at construction
// time (RFC-001 §9.2): every function FQN a file declares (carrier 50401)
// or calls from a lowered annotation expression (carrier 50400) must have a
// registered implementation. Without WithStrictValidation, a miss installs
// an Unimplemented placeholder that fails at first call.
func WithDescriptors(files ...protoreflect.FileDescriptor) Option {
	return func(cfg *validatorConfig) {
		cfg.descriptors = append(cfg.descriptors, files...)
	}
}

// WithStrictValidation makes init-time verification strict (RFC-001 §9.2):
// a function FQN required by a WithDescriptors file but missing from the
// registry fails New instead of being substituted with an Unimplemented
// placeholder.
func WithStrictValidation() Option {
	return func(cfg *validatorConfig) {
		cfg.strict = true
	}
}

// WithFunction registers an RFC-001 function implementation under its
// schema fully-qualified name — the construction-time equivalent of
// Engine.Register, visible to init-time verification (RFC-001 §9.2):
//
//	protocheck.New(
//	    protocheck.WithFunction("myco.commons.is_e164", protocheck.Function(isE164)),
//	    protocheck.WithDescriptors(fd),
//	    protocheck.WithStrictValidation(),
//	)
//
// fn must be a Function (or any func(args []any) (bool, *Violation)).
// Engine-native Go functions — the reflectively-wrapped helpers callable
// as check.<name>(...) inside protocheckpb Check expressions — are an
// expression-engine feature and register on the engine instead, which
// plugs in via WithExpressionEngine.
func WithFunction(name string, fn any) Option {
	return func(cfg *validatorConfig) {
		if cfg.err != nil {
			return
		}
		switch impl := fn.(type) {
		case Function:
			cfg.registerSPI(name, impl)
		case func(args []any) (bool, *Violation):
			cfg.registerSPI(name, impl)
		default:
			cfg.err = fmt.Errorf("WithFunction(%q): %T is not a protocheck.Function; engine-native Go functions register on the expression engine (see WithExpressionEngine)", name, fn)
		}
	}
}

// registerSPI records an RFC-001 function implementation keyed by FQN.
func (cfg *validatorConfig) registerSPI(fqn string, impl Function) {
	if fqn == "" {
		cfg.err = fmt.Errorf("WithFunction: empty function FQN")
		return
	}
	if impl == nil {
		cfg.err = fmt.Errorf("WithFunction(%q): nil Function", fqn)
		return
	}
	if cfg.spiFuncs == nil {
		cfg.spiFuncs = make(map[string]Function)
	}
	if _, ok := cfg.spiFuncs[fqn]; ok {
		cfg.err = fmt.Errorf("WithFunction(%q): already registered", fqn)
		return
	}
	cfg.spiFuncs[fqn] = impl
}
