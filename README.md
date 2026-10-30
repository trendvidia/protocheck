# protocheck

Protobuf message validation for Go.

Inspired by [protovalidate](https://protovalidate.com/), protocheck lets you declare validation constraints directly in `.proto` files. Structural rules (`required`, ranges, lengths, formats, collections) cover the common cases, and custom `check` expressions cover the rest. Expressions are written in the small expression language of [RFC-001](https://github.com/trendvidia/protowire/blob/main/docs/RFC-001-schema-extensions.md#54-expression-language-normative), the same one its `@validate` rules use, and need no runtime beyond this module — see [Check expressions](#check-expressions).

```protobuf
import "protocheck/protocheck.proto";

message User {
  string email = 1 [(protocheck.field) = {
    required: true,
    string: { email: true, max_len: 255 }
  }];

  uint32 age = 2 [(protocheck.field).uint32 = { gte: 0, lte: 150 }];

  double balance = 3 [(protocheck.field) = {
    check: [{
      id: "non_negative",
      message: "balance must not be negative",
      expression: "this >= 0.0"
    }]
  }];
}
```

```go
v, _ := protocheck.New()

err := v.Validate(user)
if err != nil {
    var ve *protocheck.ValidationError
    if errors.As(err, &ve) {
        for _, v := range ve.Violations {
            fmt.Printf("%s: %s\n", v.Path, v.Cause.FallbackMessage)
            // "email: field is required"
            // "age: value must be at most 150"
        }
    }
}
```

## Installation

```bash
go get github.com/trendvidia/protocheck/v2
```

Make `protocheck.proto` available to your codegen toolchain.

With **buf**, depend on the
[BSR module](https://buf.build/trendvidia/protocheck):

```yaml
# buf.yaml
version: v2
deps:
  - buf.build/trendvidia/protocheck
```

Your generated code must import protocheck's own Go package,
`github.com/trendvidia/protocheck/v2/protocheckpb`. Never generate Go code
for `protocheck.proto` yourself: a second copy registers the same
extensions twice. `buf generate` leaves dependencies alone by default, but
managed mode's `go_package_prefix` also rewrites the import path of
dependencies. Disable it for this module:

```yaml
# buf.gen.yaml
version: v2
managed:
  enabled: true
  override:
    - file_option: go_package_prefix
      value: example.com/acme/gen/go
  disable:
    - file_option: go_package
      module: buf.build/trendvidia/protocheck
plugins:
  - remote: buf.build/protocolbuffers/go
    out: gen/go
    opt: paths=source_relative
```

Without the `disable` entry, the generated `.pb.go` files import
`example.com/acme/gen/go/protocheck` and `go build` fails with
"no required module provides package".

Each protocheck release is pushed to the BSR under its version tag (for
example `buf.build/trendvidia/protocheck:v2.7.0`). Pin the same version
you `go get`.

The extension numbers in `protocheck.proto` changed in v2.5.0. A schema
generated against an older copy still compiles but its constraints are no
longer found, so validation silently passes. Regenerate whenever you
upgrade across that release.

With **protoc**:

```bash
protoc \
  -I path/to/protocheck/proto \
  -I . \
  --go_out=. --go_opt=paths=source_relative \
  your_service.proto
```

## How it works

```
┌──────────────┐    buf/protoc   ┌──────────────────┐
│  .proto file │  ──────────────>│  Generated .pb.go │
│  + protocheck│                 │  (with extensions)│
│    options   │                 └────────┬─────────┘
└──────────────┘                          │
                                          v
                              ┌───────────────────────┐
                              │  protocheck.Validate() │
                              │                       │
                              │  1. Read extensions   │
                              │  2. Apply std rules   │
                              │  3. Eval check exprs  │
                              │  4. Recurse nested    │
                              └───────────┬───────────┘
                                          │
                              ┌───────────v───────────┐
                              │  nil | ValidationError │
                              │       []Violation      │
                              └────────────────────────┘
```

Constraints are stored as protobuf extensions on `FieldOptions`, `MessageOptions`, and `OneofOptions`. At runtime, `protocheck.Validator` reflects over the message descriptor, reads extensions, evaluates standard rules, hands `check` expressions to the configured expression engine, and recursively validates nested messages.

## Constraint reference

### Field constraints

Every field annotation starts with `(protocheck.field)`:

```protobuf
string name = 1 [(protocheck.field) = {
  required: true,
  ignore: IGNORE_IF_UNPOPULATED,
  string: { min_len: 1 },
  check: [{ id: "...", message: "...", expression: "..." }]
}];
```

| Property | Type | Description |
|----------|------|-------------|
| `required` | `bool` | Field must be explicitly set |
| `ignore` | `Ignore` | When to skip validation (see below) |
| `check` | `[]Expression` | [Check expressions](#check-expressions) (`this` = field value) |
| `type` | oneof | Type-specific rules (see sections below) |

#### Ignore modes

| Value | Behavior |
|-------|----------|
| `IGNORE_UNSPECIFIED` | Always validate (default) |
| `IGNORE_IF_UNPOPULATED` | Skip when the field has no presence (`msg.Has(field) == false`) |
| `IGNORE_IF_DEFAULT_VALUE` | Skip when the field's value equals its proto default — including a proto3 `optional` field explicitly set to the zero value |
| `IGNORE_ALWAYS` | Never validate this field |

For proto3 scalars without `optional`, "default" and "no presence" are indistinguishable, so `IGNORE_IF_UNPOPULATED` and `IGNORE_IF_DEFAULT_VALUE` behave identically. They diverge only on proto3 `optional` fields: an explicit `""` / `0` / `false` keeps `Has()` true but matches the default, so `IGNORE_IF_DEFAULT_VALUE` skips while `IGNORE_IF_UNPOPULATED` validates.

### Numeric rules

Apply to `float`, `double`, `int32`, `int64`, `uint32`, `uint64`:

```protobuf
double price = 1 [(protocheck.field).double = {
  gt: 0.0,
  lte: 99999.99
}];

int32 quantity = 2 [(protocheck.field).int32 = {
  gte: 1,
  lte: 1000
}];
```

| Rule | Description |
|------|-------------|
| `const` | Must equal this exact value |
| `lt` / `lte` | Must be less than / at most |
| `gt` / `gte` | Must be greater than / at least |
| `in` | Must be one of these values |
| `not_in` | Must not be any of these values |
| `finite` | Must not be NaN or Inf (float/double only) |

### String rules

```protobuf
string email = 1 [(protocheck.field).string = {
  min_len: 1,
  max_len: 255,
  email: true
}];

string phone = 2 [(protocheck.field).string = {
  pattern: "^\\+?[0-9\\-\\s]+$"
}];

string country = 3 [(protocheck.field).string = {
  in: ["US", "CA", "MX", "BR"]
}];
```

| Rule | Description |
|------|-------------|
| `const` | Must equal this exact value |
| `len` / `min_len` / `max_len` | Character count (Unicode) |
| `len_bytes` / `min_bytes` / `max_bytes` | Byte length |
| `pattern` | Must match regex (RE2 syntax) |
| `prefix` / `suffix` / `contains` / `not_contains` | Substring checks |
| `in` / `not_in` | Allowed / disallowed values |

**Format validators** (well-known oneof):

| Rule | Description |
|------|-------------|
| `email` | Valid email address (RFC 5322) |
| `hostname` | Valid hostname (RFC 952) |
| `ip` / `ipv4` / `ipv6` | Valid IP address |
| `uri` / `uri_ref` | Valid absolute URI / URI reference |
| `uuid` | Valid UUID (RFC 4122) |

### Bytes rules

```protobuf
bytes payload = 1 [(protocheck.field).bytes = {
  min_len: 1,
  max_len: 65536
}];
```

| Rule | Description |
|------|-------------|
| `const` / `len` / `min_len` / `max_len` | Value and size checks |
| `prefix` / `suffix` / `contains` | Byte sequence checks |
| `in` / `not_in` | Allowed / disallowed byte sequences (compared with `bytes.Equal`) |
| `ip` / `ipv4` / `ipv6` | Valid IP address bytes (4 or 16 / exactly 4 / exactly 16) |

### Bool rules

```protobuf
bool must_accept = 1 [(protocheck.field).bool = { const: true }];
```

### Enum rules

```protobuf
Status status = 1 [(protocheck.field).enum = {
  defined_only: true,
  in: [1, 2]
}];
```

| Rule | Description |
|------|-------------|
| `const` | Must equal this value |
| `defined_only` | Must be a defined enum value (rejects unknown numbers) |
| `in` / `not_in` | Allowed / disallowed numeric values |

### Repeated rules

```protobuf
repeated string tags = 1 [(protocheck.field).repeated = {
  min_items: 1,
  max_items: 10,
  unique: true,
  items: { string: { min_len: 1 } }
}];
```

| Rule | Description |
|------|-------------|
| `min_items` / `max_items` | Size bounds |
| `unique` | All elements must be distinct |
| `items` | `FieldConstraints` applied to each element |

### Map rules

```protobuf
map<string, string> labels = 1 [(protocheck.field).map = {
  min_pairs: 1,
  max_pairs: 20,
  keys:   { string: { min_len: 1 } },
  values: { string: { max_len: 256 } }
}];
```

| Rule | Description |
|------|-------------|
| `min_pairs` / `max_pairs` | Size bounds |
| `keys` | `FieldConstraints` applied to each key |
| `values` | `FieldConstraints` applied to each value |

### Message constraints

```protobuf
message DateRange {
  option (protocheck.message).disabled = true;  // skip all validation
}
```

| Property | Type | Description |
|----------|------|-------------|
| `disabled` | `bool` | Skip all validation for this message |
| `check` | `[]Expression` | [Check expressions](#check-expressions) (`this` = the message) |

### Oneof constraints

```protobuf
oneof contact {
  option (protocheck.oneof).required = true;
  string email = 1;
  string phone = 2;
}
```

| Property | Type | Description |
|----------|------|-------------|
| `required` | `bool` | At least one field in the oneof must be set |

## Check expressions

A `check` rule's `expression` is evaluated in the
[RFC-001 §5.4 expression language](https://github.com/trendvidia/protowire/blob/main/docs/RFC-001-schema-extensions.md#54-expression-language-normative).
It must evaluate to `bool`: `true` passes, `false` reports a violation under
the rule's `id`, with its `message`. An expression that fails to parse or
evaluate also reports a violation; it never passes.

### `this`

In a field rule, `this` is the field value:

| Proto type | `this` |
|------------|--------|
| signed integers, enums | integer |
| unsigned integers | unsigned integer |
| `float`, `double` | floating point |
| `bool` | boolean |
| `string` | string |
| `bytes` | bytes |
| `google.protobuf.Timestamp` | time (compares with `now()`) |
| `google.protobuf.Duration` | duration |
| wrapper types (`Int64Value`, ...) | the unwrapped value |
| any other message | the message |

A rule on a message-typed field runs only when the field is set. With an
expression engine configured, such a rule reports a violation instead:
`EvalField` takes scalar values only.

Rules on repeated items (`repeated.items.check`) and map values
(`map.values.check`) bind each element the same way. With an engine,
message-typed elements go to `EvalMessage`, with the element's fields bound
by name.

In a message rule, `this` is the whole message.

```protobuf
double percentage = 1 [(protocheck.field) = {
  check: [{
    id: "valid_range",
    message: "must be between 0 and 100",
    expression: "this >= 0.0 && this <= 100.0"
  }]
}];

string handle = 2 [(protocheck.field) = {
  check: [{
    id: "handle_shape",
    message: "handle must start with @ and be at most 16 characters",
    expression: "this.starts_with(\"@\") && this.size() <= 16"
  }]
}];
```

### Syntax

| Category | |
|----------|---|
| Literals | integers, floats, `"strings"`, `true`, `false`, lists `[1, 2, 3]` |
| Comparison | `==`, `!=`, `<`, `<=`, `>`, `>=` (numbers of any kind compare by value; NaN follows IEEE 754, see below) |
| Membership | `x in [a, b, c]` |
| Logical | `&&`, `\|\|`, `!` |
| Enum values | by name: `this in [MODE_MATCH, Mode.MODE_REGULATE]` (`@validate` only) |
| Builtins | `size()`, `starts_with(s)`, `ends_with(s)`, `contains(s)`, `matches(re2)`, `is_nan()`, `defined()`, `unique()`, `now()` |

Builtins have a method form (`this.size()`) and a function form
(`size(this)`). There is deliberately no arithmetic and no field access:
`this.name` is not an expression.

A NaN compares the way IEEE 754 says: `<`, `<=`, `>` and `>=` are false
with a NaN on either side, `==` is false and `!=` is true. A range such as
`this >= 0.0 && this <= 1.0` therefore refuses NaN, but a negated one such
as `!(this < 0.0)` accepts it. Add `!this.is_nan()` to refuse NaN whatever
the rule's shape, or use the structural `finite` rule.

An enum binds as its number, so `this == 1` and `this == MODE_MATCH`
mean the same. A name is resolved by the compiler, which records it in
the descriptor; the engine never looks names up itself. `defined()` is
true when an enum value is one of its enum's declared values, and
`unique()` when no two list elements are equal. They replace the bracket
rules `enum.defined_only` and `repeated.unique`. A violation reports a
declared enum value by its enum-qualified name (`EnumName("t.Mode.MODE_MATCH")`),
and an undeclared one by its number:

```protobuf
type KnownTag = StyleTag @validate(this.defined() && this != STYLE_TAG_UNSPECIFIED);

message Descriptor {
  Mode mode = 1 @required @validate(this in [MODE_MATCH, MODE_REGULATE]);
  repeated KnownTag tags = 2 @validate(this.unique());
}
```

Enum names and `defined()` need a `@validate` rule. A `check` expression
has no compiler record of names, and its `this` is a plain number, so
`defined()` there is an evaluation error.

### Cross-field rules and custom functions

A rule over several fields, or any logic the language does not have, is a
Go function registered with `WithFunction` and called with the message:

```protobuf
message TempoRange {
  uint32 min_bpm = 1;
  uint32 max_bpm = 2;

  option (protocheck.message) = {
    check: [{
      id: "tempo_ordered",
      message: "min_bpm must not exceed max_bpm",
      expression: "tempo_ordered(this)"
    }]
  };
}
```

```go
v, err := protocheck.New(
    protocheck.WithFunction("acme.v1.tempo_ordered",
        protocheck.Function(func(args []any) (bool, *protocheck.Violation) {
            t := args[0].(*acmepb.TempoRange)
            return t.GetMinBpm() <= t.GetMaxBpm(), nil
        })),
)
```

An expression can call a function by its full name (`acme.v1.tempo_ordered`)
or by any dot-separated suffix that only one registered function has
(`tempo_ordered`, `v1.tempo_ordered`). If the function returns a
`*Violation` and the rule has no `message`, the function's message is used.

### `@validate` and `@required` rules

`New()` enforces a schema's `@required`, `@default` and `@validate` rules as
well as its bracket rules, with no option. It reads them from the descriptor
of the message being validated and the files that descriptor imports:

```go
v, err := protocheck.New(
    protocheck.WithFunction("acme.v1.tempo_ordered", protocheck.Function(tempoOrdered)),
)
```

A function no one registered fails closed: a rule that calls it fails with
`protowire.function.unimplemented`, or with the rule's own `code` if it has
one. So does a rule that cannot be loaded: `Validate` returns an error that is
not a `*ValidationError`, and the message counts as not validated.

Add `WithDescriptors` to find those problems at startup. `New` then checks the
listed files and their imports, and `WithStrictValidation` makes a missing
function fail `New`. `WithStrictValidation` only covers files that
`WithDescriptors` reaches. Descriptors that hold source info, such as compiler
output, also give violations their source locations; the descriptors linked
into a Go binary have none.

```go
v, err := protocheck.New(
    protocheck.WithFunction("acme.v1.tempo_ordered", protocheck.Function(tempoOrdered)),
    protocheck.WithDescriptors(acmepb.File_acme_v1_song_proto),
    protocheck.WithStrictValidation(),
)
```

### Other engines

`WithExpressionEngine` plugs in an `ExpressionEngine`
(`Info`/`EvalField`/`EvalMessage`/`Close`). A configured engine evaluates
every `check` expression in its own language instead, so choosing one fixes
the language your schemas' expressions are written in.

For CEL, use [protovalidate](https://github.com/bufbuild/protovalidate-go)
with `buf.validate` annotations instead of `protocheck` ones. To run it
inside protowire-go's pxf / pb / SBE decoders, use the adapter module in
this repository:

```go
import pvadapter "github.com/trendvidia/protocheck/protovalidate"

v, err := pvadapter.New()                  // options pass through to protovalidate.New
opts := pxf.UnmarshalOptions{Validator: v}
```

It is versioned on its own (`protovalidate/v0.x` tags).

## Complete example

```protobuf
syntax = "proto3";
import "protocheck/protocheck.proto";

message CreateOrderRequest {
  string customer_id = 1 [(protocheck.field) = {
    required: true,
    string: { uuid: true }
  }];

  repeated LineItem items = 2 [(protocheck.field).repeated = {
    min_items: 1,
    max_items: 100,
    items: { required: true }
  }];

  Address shipping_address = 3 [(protocheck.field).required = true];

  string promo_code = 4 [(protocheck.field) = {
    ignore: IGNORE_IF_UNPOPULATED,
    string: { pattern: "^[A-Z0-9]{4,12}$" }
  }];
}

message LineItem {
  string sku = 1 [(protocheck.field) = {
    required: true,
    string: { min_len: 1, max_len: 64 }
  }];

  uint32 quantity = 2 [(protocheck.field).uint32 = { gte: 1, lte: 999 }];

  double unit_price = 3 [(protocheck.field).double = { gt: 0.0 }];
}

message Address {
  string line1 = 1 [(protocheck.field) = {
    required: true,
    string: { min_len: 1 }
  }];

  string city = 2 [(protocheck.field) = {
    required: true,
    string: { min_len: 1 }
  }];

  string postal_code = 3 [(protocheck.field) = {
    required: true,
    string: { min_len: 1 }
  }];

  string country_code = 4 [(protocheck.field) = {
    required: true,
    string: { len: 2, pattern: "^[A-Z]{2}$" }
  }];
}
```

```go
package main

import (
    "errors"
    "fmt"
    "log"

    "github.com/trendvidia/protocheck/v2"
    orderpb "example.com/gen/order"
)

func main() {
    v, err := protocheck.New()
    if err != nil {
        log.Fatal(err)
    }

    req := &orderpb.CreateOrderRequest{
        CustomerId: "not-a-uuid",
        Items:      nil, // missing required items
    }

    err = v.Validate(req)

    var ve *protocheck.ValidationError
    if errors.As(err, &ve) {
        for _, v := range ve.Violations {
            fmt.Printf("%-30s %-20s %s\n", v.Path, v.Cause.Code, v.Cause.FallbackMessage)
        }
    }
    // Output:
    // customer_id                    string.uuid          value must be a valid UUID
    // items                          repeated.min_items   list must have at least 1 items
    // shipping_address               required             field is required
}
```

## Validation behavior

- **Recursive**: nested messages are validated automatically. A message field with no `(protocheck.field)` annotation still has its nested constraints checked when populated.
- **Oneof fields**: only the selected field is validated; unset alternatives are skipped.
- **Proto3 defaults**: unset scalar fields have their default value (`""`, `0`, `false`). Use `ignore: IGNORE_IF_UNPOPULATED` to skip validation when the field has no presence, or `IGNORE_IF_DEFAULT_VALUE` to skip whenever the value equals the default (see [Ignore modes](#ignore-modes)).
- **Caching**: each `check` expression is parsed once per `Validator` and reused across all subsequent `Validate` calls.
- **Thread-safe after construction**: once `New()` returns, the `Validator` is safe to share across goroutines. Don't pass the same `*validatorConfig` between `Option`s; `New(opts...)` builds it.

### Violation paths and codes

Validation failures surface as `EnrichedViolation`s (mirroring the RFC-001
`report.proto` wire shape): a `Cause *Violation` carrying `Code`, `Params`,
and `FallbackMessage`, wrapped with the engine context — a structured
`Path`, the `RuleKind`, and `ForKey` for map-key violations.

- **`Path`** is structured (`FieldPath`, a slice of segments with field
  names, numbers, and typed subscripts — `Index`, `StringKey`, `IntKey`,
  `UintKey`, `BoolKey`), so map keys and repeated indices are unambiguous.
  `Path.String()` derives the dotted rendering:

  ```
  shipping_address.city                       # nested message
  items[3].quantity                           # repeated element
  labels[en]                                  # map entry (ForKey marks key violations)
  channel                                     # oneof (path is the oneof name for "required")
  ```

  The top-level message contributes no prefix — paths start at its fields.

- **`Cause.Code`** follows `<type>.<rule>` for standard rules and uses the user-supplied `id` from `check` for custom expressions:

  | Family | IDs |
  |--------|-----|
  | Presence | `required` |
  | Numeric (`float`/`double`/`int32`/`int64`/`uint32`/`uint64`) | `<type>.const`, `<type>.lt`, `<type>.lte`, `<type>.gt`, `<type>.gte`, `<type>.in`, `<type>.not_in` |
  | Float / Double extras | `float.finite`, `double.finite` |
  | Bool | `bool.const` |
  | String | `string.const`, `string.len`, `string.min_len`, `string.max_len`, `string.len_bytes`, `string.min_bytes`, `string.max_bytes`, `string.pattern`, `string.prefix`, `string.suffix`, `string.contains`, `string.not_contains`, `string.in`, `string.not_in`, `string.email`, `string.hostname`, `string.ip`, `string.ipv4`, `string.ipv6`, `string.uri`, `string.uri_ref`, `string.uuid` |
  | Bytes | `bytes.const`, `bytes.len`, `bytes.min_len`, `bytes.max_len`, `bytes.prefix`, `bytes.suffix`, `bytes.contains`, `bytes.in`, `bytes.not_in`, `bytes.ip`, `bytes.ipv4`, `bytes.ipv6` |
  | Enum | `enum.const`, `enum.defined_only`, `enum.in`, `enum.not_in` |
  | Repeated | `repeated.min_items`, `repeated.max_items`, `repeated.unique` |
  | Map | `map.min_pairs`, `map.max_pairs` |
  | Custom expressions | the `id` you set in `check: [{ id: "...", ... }]` |

## protowire-go integration (`protowire`)

The `protowire` package adapts the engine to protowire-go's validation
seam ([`check.Validator`](https://github.com/trendvidia/protowire-go/tree/main/check),
≥ v1.3.1), so the pxf / pb / SBE decoders run protocheck validation
post-decode and fail with a `*check.Error` on violations — the
protocheck counterpart of the `protovalidate` adapter module:

```go
import (
    "github.com/trendvidia/protocheck/v2"
    pcw "github.com/trendvidia/protocheck/v2/protowire"
    "github.com/trendvidia/protowire-go/check"
    "github.com/trendvidia/protowire-go/encoding/pxf"
)

v, err := pcw.New(protocheck.WithFailFast())   // options pass through to protocheck.New
defer v.Close()

opts := pxf.UnmarshalOptions{Validator: v}     // pb: pb.UnmarshalOptions{Validator: v}
                                               // sbe: sbe.CodecOptions{Validator: v}.NewCodec(fd)
err = opts.Unmarshal(data, msg)
var ce *check.Error
if errors.As(err, &ce) {
    for _, viol := range ce.Report.Violations {
        fmt.Println(viol.Path, viol.RuleID, viol.Message)
    }
}
```

`Wrap(v *protocheck.Validator)` adapts an engine you already hold. Rule
IDs come through namespaced `rfc001.…` (`rfc001.string.min_len`,
`rfc001.protowire.required`); the protovalidate adapter uses
`buf.validate.…`, so mixed pipelines can attribute reports by prefix.
Map-key violations keep the entry's path with a `map key: ` message
prefix.

protowire-go never depends on this repo; the dependency points the other
way.

**Cross-repo development**: use a `go.work` spanning the checkouts so
local changes to either side are picked up without `replace` churn:

```bash
go work init ./protocheck ./protocheck/protovalidate ./protowire-go
```

CI and release builds must run with the workspace off (`GOWORK=off`) to
verify the committed `go.mod` versions.

## Project structure

```
protocheck/
├── proto/protocheck/protocheck.proto   # Constraint definitions (import this)
├── protocheckpb/protocheck.pb.go       # Generated Go code for constraints
├── protocheck.go                       # Validator: Validate(), field/message traversal
├── options.go                          # Option types (WithFunction, WithDescriptors, ...)
├── engine_expr.go                      # ExpressionEngine seam, WithExpressionEngine
├── exprlang.go                         # RFC-001 @validate expression dialect
├── carrier_eval.go                     # RFC-001 carrier rule evaluation
├── rules.go                            # Standard rule evaluation (string, numeric, etc.)
├── violations.go                       # ValidationError, Violation types
├── protowire/                          # protowire-go check.Validator adapter
├── protovalidate/                      # Nested module: protovalidate (CEL) adapter
├── protocheck_test.go                  # Tests
├── internal/testpb/                    # Test proto definitions
├── buf.yaml                            # buf workspace + lint config
├── buf.gen.yaml                        # buf codegen plugin config
├── Makefile                            # generate / lint / format / test / ...
├── go.mod
└── go.sum
```

## Development

```bash
make help        # list all targets
make generate    # regenerate .pb.go from .proto via buf
make lint        # buf lint
make format      # buf format -w (apply formatting in place)
make check       # CI-friendly: format check + lint, no writes
make build       # go build ./...
make test        # go test -count=1 ./...
make tidy        # go mod tidy
```

Requires [`buf`](https://buf.build/docs/installation) on `PATH`. Codegen uses
the remote `buf.build/protocolbuffers/go` plugin, so `protoc-gen-go` does not
need to be installed locally.
