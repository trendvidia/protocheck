// Copyright 2026 TrendVidia LLC
// SPDX-License-Identifier: MIT

// Package protovalidate adapts buf.build/go/protovalidate to the
// protowire-go validation seam ([check.Validator]).
//
// This is the community validation engine: rules are declared with
// buf.validate annotations in the .proto schema and evaluated by
// protovalidate's CEL runtime. It lives in protocheck — the validator
// abstraction layer — as its own nested Go module so neither protocheck
// core nor protowire-go picks up the protovalidate dependency set;
// consumers opt in with:
//
//	go get github.com/trendvidia/protocheck/protovalidate
//
//	v, err := protovalidate.New()
//	...
//	opts := pxf.UnmarshalOptions{Validator: v}
//
// Violation rule IDs are namespaced under "buf.validate." (e.g.
// "buf.validate.string.min_len", or "buf.validate.<cel-id>" for custom
// CEL rules) so reports remain distinguishable from other engines'.
// Field paths use protovalidate's rendering, which matches the dotted
// scheme of check.Violation except that string map keys are quoted
// (`tags["prod"]` rather than `tags[prod]`).
package protovalidate

import (
	"errors"
	"fmt"

	pv "buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"

	"github.com/trendvidia/protowire-go/check"
)

// Validator adapts a [pv.Validator] to the [check.Validator] seam.
// It is safe for concurrent use.
type Validator struct {
	inner pv.Validator
}

var _ check.Validator = (*Validator)(nil)

// New builds a Validator backed by a fresh protovalidate validator.
// Options are passed through to [pv.New].
func New(options ...pv.ValidatorOption) (*Validator, error) {
	inner, err := pv.New(options...)
	if err != nil {
		return nil, err
	}
	return &Validator{inner: inner}, nil
}

// Wrap adapts an existing protovalidate validator, for callers that
// already hold one (e.g. shared with non-protowire validation paths).
func Wrap(v pv.Validator) *Validator {
	return &Validator{inner: v}
}

// Validate implements [check.Validator]. Rule violations land in the
// returned Report; a decoded value that is not a proto.Message, and
// protovalidate compilation or runtime failures, are engine errors.
func (v *Validator) Validate(decoded any) (*check.Report, error) {
	msg, ok := decoded.(proto.Message)
	if !ok {
		return nil, fmt.Errorf("protovalidate: cannot validate %T: not a proto.Message", decoded)
	}
	err := v.inner.Validate(msg)
	if err == nil {
		return &check.Report{}, nil
	}
	var ve *pv.ValidationError
	if !errors.As(err, &ve) {
		return nil, err
	}
	rep := &check.Report{Violations: make([]check.Violation, 0, len(ve.Violations))}
	for _, viol := range ve.Violations {
		p := viol.Proto
		rep.Violations = append(rep.Violations, check.Violation{
			Path:    pv.FieldPathString(p.GetField()),
			RuleID:  ruleID(p.GetRuleId()),
			Message: p.GetMessage(),
		})
	}
	return rep, nil
}

func ruleID(id string) string {
	if id == "" {
		return "buf.validate"
	}
	return "buf.validate." + id
}
