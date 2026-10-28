// Package protowire adapts the protocheck engine to protowire-go's
// validation seam ([check.Validator]) — the enhanced-flavor engine for
// the pxf / pb / sbe decoders (protowire-go#49 Phase 3; #36 here).
//
//	v, err := protowire.New(protocheck.WithFailFast())
//	...
//	opts := pxf.UnmarshalOptions{Validator: v}
//
// Violation rule IDs are namespaced under "rfc001." (e.g.
// "rfc001.string.min_len", "rfc001.protowire.required") so reports stay
// distinguishable from other engines' — the community protovalidate
// adapter uses "buf.validate.…". Field paths use protocheck's dotted
// rendering, which matches the seam's scheme exactly ("address.zip_code",
// "items[3]", "attrs[env]"). A violation against a map entry's *key*
// (EnrichedViolation.ForKey) keeps the entry's path; its message is
// prefixed "map key: " since the seam carries no key/value flag.
package protowire

import (
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/trendvidia/protocheck/v2"
	"github.com/trendvidia/protowire-go/check"
)

// Validator adapts a [*protocheck.Validator] to the [check.Validator]
// seam. It is safe for concurrent use, matching the engine's contract.
type Validator struct {
	inner *protocheck.Validator
}

var _ check.Validator = (*Validator)(nil)

// New builds a Validator backed by a fresh protocheck engine. Options
// are passed through to [protocheck.New].
func New(opts ...protocheck.Option) (*Validator, error) {
	inner, err := protocheck.New(opts...)
	if err != nil {
		return nil, err
	}
	return &Validator{inner: inner}, nil
}

// Wrap adapts an existing engine, for callers that already hold one
// (e.g. shared with non-protowire validation paths).
func Wrap(v *protocheck.Validator) *Validator {
	return &Validator{inner: v}
}

// Close releases the underlying engine's pooled evaluation VMs (see
// [protocheck.Validator.Close]). It closes the wrapped engine even when
// the Validator came from [Wrap] — callers sharing an engine should
// close it at the owner, not through the adapter.
func (v *Validator) Close() error {
	return v.inner.Close()
}

// Validate implements [check.Validator]. Constraint violations land in
// the returned Report; a decoded value that is not a proto.Message, and
// any non-violation engine failure, are engine errors.
func (v *Validator) Validate(decoded any) (*check.Report, error) {
	msg, ok := decoded.(proto.Message)
	if !ok {
		return nil, fmt.Errorf("protocheck/protowire: cannot validate %T: not a proto.Message", decoded)
	}
	err := v.inner.Validate(msg)
	if err == nil {
		return &check.Report{}, nil
	}
	var ve *protocheck.ValidationError
	if !errors.As(err, &ve) {
		return nil, err
	}
	rep := &check.Report{Violations: make([]check.Violation, 0, len(ve.Violations))}
	for _, ev := range ve.Violations {
		message := ev.Cause.FallbackMessage
		if ev.ForKey {
			message = "map key: " + message
		}
		rep.Violations = append(rep.Violations, check.Violation{
			Path:    ev.Path.String(),
			RuleID:  "rfc001." + ev.Cause.Code,
			Message: message,
		})
	}
	return rep, nil
}
