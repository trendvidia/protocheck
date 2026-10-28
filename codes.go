package protocheck

import "strconv"

// This file exposes the reserved violation codes of RFC-001 §7 as typed
// constants, together with their spec-pinned fallback messages. Codes
// beginning with "protowire." are minted only by spec-mandated codegen and
// engine machinery — user rules and function implementations MUST NOT use
// this namespace. Spec-mandated codegen references these constants and
// helpers rather than string literals, so cross-port report equality on
// code and fallback_message holds by construction.

const (
	// CodeRequired is returned when a @required field is absent (§6.1).
	CodeRequired = "protowire.required"

	// CodeDepthExceeded is returned when the recursion depth limit is
	// reached during traversal (§6.4). The effective limit travels in
	// Params["limit"].
	CodeDepthExceeded = "protowire.depth_exceeded"

	// CodeFunctionUnimplemented is returned when a declared function is
	// invoked with no registered implementation: the §9.2 lenient
	// placeholders and the §9.3 UnimplementedFunctions stubs.
	CodeFunctionUnimplemented = "protowire.function.unimplemented"

	// CodeFunctionInvalidArgument is returned when a generated
	// registration adapter's arity or argument-type guard fails before
	// the user's function implementation is invoked (§9.3).
	CodeFunctionInvalidArgument = "protowire.function.invalid_argument"
)

// Spec-pinned static fallback messages for the reserved codes whose text
// carries no datum (§7): where a param already carries the datum, the
// fallback string stays static.
const (
	MsgRequired      = "field is required"
	MsgDepthExceeded = "recursion depth limit exceeded"
)

// MsgFunctionUnimplemented renders the spec-pinned fallback message for
// CodeFunctionUnimplemented: "<function>: not implemented", where fqn is
// the declared function's fully-qualified name.
func MsgFunctionUnimplemented(fqn string) string {
	return fqn + ": not implemented"
}

// MsgFunctionArity renders the spec-pinned arity-guard fallback message
// for CodeFunctionInvalidArgument: "<function>: expected <n> argument(s)".
func MsgFunctionArity(fqn string, n int) string {
	return fqn + ": expected " + strconv.Itoa(n) + " argument(s)"
}

// MsgFunctionArgType renders the spec-pinned argument-type-guard fallback
// message for CodeFunctionInvalidArgument: "<function>: argument <i> is
// not <type>". i is zero-based; schemaType is the parameter type as
// declared in the schema (§6.5 signature types, e.g. "string", "int64",
// a message FQN) — never a host-language type name, which would break
// cross-port equality.
func MsgFunctionArgType(fqn string, i int, schemaType string) string {
	return fqn + ": argument " + strconv.Itoa(i) + " is not " + schemaType
}
