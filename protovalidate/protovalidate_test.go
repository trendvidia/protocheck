// Copyright 2026 TrendVidia LLC
// SPDX-License-Identifier: MIT

package protovalidate_test

import (
	"context"
	"errors"
	"testing"

	// Registers buf/validate/validate.proto (and its extensions) in the
	// global registries so protocompile can resolve the import below.
	_ "buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	"github.com/bufbuild/protocompile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"

	pvcheck "github.com/trendvidia/protocheck/protovalidate"
	"github.com/trendvidia/protowire-go/check"
	"github.com/trendvidia/protowire-go/encoding/pxf"
)

const userProtoSrc = `syntax = "proto3";

import "buf/validate/validate.proto";

message User {
  string email = 1 [(buf.validate.field).string.min_len = 3];
  uint32 age = 2 [(buf.validate.field).uint32.lt = 150];
}
`

func compileUser(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()
	resolver := protocompile.WithStandardImports(protocompile.CompositeResolver{
		&protocompile.SourceResolver{
			Accessor: protocompile.SourceAccessorFromMap(map[string]string{
				"user.proto": userProtoSrc,
			}),
		},
		protocompile.ResolverFunc(func(path string) (protocompile.SearchResult, error) {
			fd, err := protoregistry.GlobalFiles.FindFileByPath(path)
			if err != nil {
				return protocompile.SearchResult{}, err
			}
			return protocompile.SearchResult{Desc: fd}, nil
		}),
	})
	comp := protocompile.Compiler{Resolver: resolver}
	files, err := comp.Compile(context.Background(), "user.proto")
	require.NoError(t, err)
	desc := files[0].Messages().ByName("User")
	require.NotNil(t, desc)
	return desc
}

func newUser(t *testing.T, desc protoreflect.MessageDescriptor, email string, age uint32) *dynamicpb.Message {
	t.Helper()
	msg := dynamicpb.NewMessage(desc)
	msg.Set(desc.Fields().ByName("email"), protoreflect.ValueOfString(email))
	msg.Set(desc.Fields().ByName("age"), protoreflect.ValueOfUint32(age))
	return msg
}

func TestValidateMapsViolations(t *testing.T) {
	desc := compileUser(t)
	v, err := pvcheck.New()
	require.NoError(t, err)

	rep, err := v.Validate(newUser(t, desc, "x", 200))
	require.NoError(t, err, "violations are report content, not an engine error")
	require.NotNil(t, rep)
	require.Len(t, rep.Violations, 2)

	byPath := map[string]check.Violation{}
	for _, viol := range rep.Violations {
		byPath[viol.Path] = viol
	}
	email, ok := byPath["email"]
	require.True(t, ok, "expected a violation for email, got %v", rep.Violations)
	assert.Equal(t, "buf.validate.string.min_len", email.RuleID)
	assert.NotEmpty(t, email.Message)

	age, ok := byPath["age"]
	require.True(t, ok, "expected a violation for age, got %v", rep.Violations)
	assert.Equal(t, "buf.validate.uint32.lt", age.RuleID)
}

func TestValidateCleanPass(t *testing.T) {
	desc := compileUser(t)
	v, err := pvcheck.New()
	require.NoError(t, err)

	rep, err := v.Validate(newUser(t, desc, "a@b.co", 30))
	require.NoError(t, err)
	assert.True(t, rep.OK())
}

func TestValidateNonProtoValue(t *testing.T) {
	v, err := pvcheck.New()
	require.NoError(t, err)

	rep, err := v.Validate(struct{ Name string }{"x"})
	assert.Nil(t, rep)
	require.Error(t, err)
	var ce *check.Error
	assert.False(t, errors.As(err, &ce), "engine errors must not be check.Error")
}

func TestPXFDecodeWithProtovalidate(t *testing.T) {
	desc := compileUser(t)
	v, err := pvcheck.New()
	require.NoError(t, err)

	opts := pxf.UnmarshalOptions{Validator: v}

	// Violating document fails the decode with a *check.Error carrying
	// the mapped report.
	_, _, err = opts.UnmarshalFullDescriptor([]byte(`email = "x"`+"\n"+`age = 200`), desc)
	var ce *check.Error
	require.ErrorAs(t, err, &ce)
	assert.Len(t, ce.Report.Violations, 2)

	// Valid document decodes cleanly.
	msg, result, err := opts.UnmarshalFullDescriptor([]byte(`email = "a@b.co"`+"\n"+`age = 30`), desc)
	require.NoError(t, err)
	require.NotNil(t, msg)
	assert.True(t, result.Report().OK())
}
