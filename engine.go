package protocheck

import (
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
)

// This file defines the RFC-001 §9.1 engine SPI: the implementation-neutral
// contract between schema-extension tooling and a validation engine. The
// shapes mirror the pinned wire format in protowire's
// proto/schema/v1/report.proto (RFC-001 §7); spiEngine adapts Validator to
// the contract. Downstream consumers program against Engine so other engines
// are drop-in.

// ─── SPI interfaces ─────────────────────────────────────────────────────────

// Engine is the RFC-001 §9.1 engine SPI. A project selects one engine at
// validator-binary build time; all engines carry this same logical contract.
//
// Register and RegisterCatalog must complete before the first Validate call;
// they are not safe to interleave with in-flight validation.
type Engine interface {
	// Register binds a validation function implementation to the
	// fully-qualified name it is declared under in the schema
	// (e.g. "myco.commons.is_e164").
	Register(fqn string, impl Function) error

	// RegisterCatalog installs the message catalog for one locale
	// (e.g. "en-US"). At format time, violation messages are rendered from
	// code + params through the catalog; a miss falls back to the
	// violation's own message.
	RegisterCatalog(locale string, catalog Catalog) error

	// Validate checks msg against its schema constraints and returns the
	// complete Report. An invalid message is not an error: validity is
	// Report.Violations being empty (with Truncated false). The error
	// return is reserved for engine failure.
	Validate(msg proto.Message) (*Report, error)
}

// Function is the engine-independent validation function contract
// (RFC-001 §6.5): (true, nil) on success, (false, &Violation{...}) on
// failure. Implementations must be pure — no I/O, no global state — as
// engines may memoize calls.
type Function func(args []any) (bool, *Violation)

// Catalog renders localized violation messages for a single locale
// (RFC-001 §7). Render returns ok == false on a catalog miss, in which case
// the engine falls back to the violation's own message.
type Catalog interface {
	Render(code string, params map[string]any) (message string, ok bool)
}

// ─── Report shapes (mirroring protowire.schema.v1 report.proto) ─────────────

// ExecutionMode is how the engine traverses a message instance
// (RFC-001 §6.4). Values match protowire.schema.v1.ExecutionMode.
type ExecutionMode int32

const (
	ExecutionModeUnspecified ExecutionMode = 0
	ExecutionModeCollectAll  ExecutionMode = 1
	ExecutionModeFailFast    ExecutionMode = 2
)

func (m ExecutionMode) String() string {
	switch m {
	case ExecutionModeCollectAll:
		return "COLLECT_ALL"
	case ExecutionModeFailFast:
		return "FAIL_FAST"
	default:
		return "UNSPECIFIED"
	}
}

// EngineInfo identifies the engine that produced a Report.
type EngineInfo struct {
	Name    string // e.g. "protocheck-go"
	Version string // module version, "" if unknown
}

// Report is the complete result of validating one message instance,
// mirroring protowire.schema.v1.Report (RFC-001 §7).
type Report struct {
	// MessageType is the fully-qualified name of the root message type
	// validated.
	MessageType string

	// Violations is empty — and Truncated false — when the instance is
	// valid.
	Violations []*EnrichedViolation

	// Mode the engine ran in; collect-all is the default.
	Mode ExecutionMode

	// Truncated is true when Violations is not exhaustive: fail-fast
	// stopped at the first violation, or an engine limit cut traversal
	// short.
	Truncated bool

	// Engine that produced this report.
	Engine EngineInfo

	// WallTimeNanos is the wall-clock validation time. 0 = not measured.
	WallTimeNanos uint64

	// catalogs resolves Format renderings; set by the engine that produced
	// the report, nil on a zero Report (Format then always falls back).
	catalogs func(locale string) (Catalog, bool)
}

// Format renders one line per violation — "path: message [code]" — with
// each message localized through the catalog registered for locale
// (RFC-001 §7). A catalog miss (no catalog for the locale, or no entry for
// a code) falls back to the violation's FallbackMessage.
func (r *Report) Format(locale string) []string {
	var cat Catalog
	if r.catalogs != nil {
		cat, _ = r.catalogs(locale)
	}
	out := make([]string, len(r.Violations))
	for i, viol := range r.Violations {
		msg := viol.Cause.FallbackMessage
		if cat != nil {
			if rendered, ok := cat.Render(viol.Cause.Code, viol.Cause.Params); ok {
				msg = rendered
			}
		}
		var b strings.Builder
		if len(viol.Path) > 0 {
			b.WriteString(viol.Path.String())
			if viol.ForKey {
				b.WriteString(" (key)")
			}
			b.WriteString(": ")
		}
		b.WriteString(msg)
		fmt.Fprintf(&b, " [%s]", viol.Cause.Code)
		out[i] = b.String()
	}
	return out
}

// ─── SPI facade ─────────────────────────────────────────────────────────────

// spiEngine implements the §9.1 Engine SPI as a thin wrapper around a
// *Validator: registration lands in the core function registry, catalogs
// in the core catalog table, and Validate wraps the walk in a Report. It
// is engine-agnostic — expression evaluation is the ExpressionEngine's
// concern, not this facade's.
type spiEngine struct {
	v *Validator

	mu       sync.Mutex
	catalogs map[string]Catalog
}

// Engine returns the RFC-001 SPI view of this Validator. Repeated calls
// return the same Engine, so functions and catalogs registered through it
// accumulate on the one instance.
func (v *Validator) Engine() Engine {
	return v.engine
}

func newSPIEngine(v *Validator, catalogs map[string]Catalog) *spiEngine {
	e := &spiEngine{
		v:        v,
		catalogs: make(map[string]Catalog, len(catalogs)),
	}
	for locale, cat := range catalogs {
		e.catalogs[locale] = cat
	}
	return e
}

// Register records impl under its schema fully-qualified name, replacing an
// Unimplemented placeholder installed by lenient init-time verification
// (RFC-001 §9.2). Registering over a real implementation is an error.
func (e *spiEngine) Register(fqn string, impl Function) error {
	return e.v.registerFunction(fqn, impl)
}

// catalog returns the Catalog registered for locale, if any.
func (e *spiEngine) catalog(locale string) (Catalog, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cat, ok := e.catalogs[locale]
	return cat, ok
}

// RegisterCatalog installs the message catalog for one locale.
func (e *spiEngine) RegisterCatalog(locale string, catalog Catalog) error {
	if locale == "" {
		return errors.New("protocheck: RegisterCatalog: empty locale")
	}
	if catalog == nil {
		return fmt.Errorf("protocheck: RegisterCatalog(%q): nil Catalog", locale)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.catalogs[locale]; ok {
		return fmt.Errorf("protocheck: RegisterCatalog(%q): already registered", locale)
	}
	e.catalogs[locale] = catalog
	return nil
}

// Validate checks msg against all protocheck constraints and wraps the
// outcome in a Report.
func (e *spiEngine) Validate(msg proto.Message) (*Report, error) {
	if msg == nil {
		return nil, errNilMessage
	}
	report := &Report{
		MessageType: string(msg.ProtoReflect().Descriptor().FullName()),
		Mode:        ExecutionModeCollectAll,
		Engine:      e.v.engineInfo(),
		catalogs:    e.catalog,
	}
	if e.v.failFast {
		report.Mode = ExecutionModeFailFast
	}

	start := time.Now()
	err := e.v.Validate(msg)
	report.WallTimeNanos = uint64(time.Since(start))

	if err != nil {
		var verr *ValidationError
		if !errors.As(err, &verr) {
			return nil, err
		}
		report.Violations = verr.Violations
		// Fail-fast stopped the walk at the first violation, so the report
		// is not exhaustive.
		report.Truncated = e.v.failFast
	}
	return report, nil
}

// coreVersion resolves this module's version from build info: the main
// module when protocheck is built directly, a dependency entry otherwise.
var coreVersion = sync.OnceValue(func() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	const modulePath = "github.com/trendvidia/protocheck/v2"
	if bi.Main.Path == modulePath {
		return bi.Main.Version
	}
	for _, dep := range bi.Deps {
		if dep.Path == modulePath {
			return dep.Version
		}
	}
	return ""
})
