# Changelog

All notable changes to protocheck are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Fixed

- **`New()` enforces `@validate`, `@required` and `@default` rules with no
  option** (#20). They used to run only for files listed in
  `WithDescriptors`, and nothing warned when a file was missing, so a
  validator built without the option passed every value on those fields. A
  `Validate` now reads the rules from the descriptor of the message's own
  file and its imports. Programs that never listed their files start
  refusing bad values, and a rule calling a function they never registered
  starts failing with `protowire.function.unimplemented`.

- **`WithDescriptors` follows imports**: a listed file's imports are
  verified and their rules enforced like the file's own (#20). With
  `WithStrictValidation`, a function that only an imported file declares or
  calls must now be registered, or `New` fails.

- **`Validate` refuses a nil message.** `Validate(nil)` panicked, and a typed
  nil such as `(*pb.Song)(nil)` passed as valid. Both now return an error that
  is not a `*ValidationError`, from `Engine().Validate` too. An unset
  message-typed field is unchanged: it is still skipped.

### Changed

- **`Validate` can return an error that is not a `*ValidationError`**: when a
  rule outside `WithDescriptors` cannot be loaded, the message is not
  validated and `Validate` says so (#20). `New` reports the same problem for
  listed files, as it always has.

## [2.8.0] — 2026-10-30

A minor release for RFC-001 §5.4's new expression features: enum value
names, `defined()`, `unique()` and `is_nan()` in `@validate` rules
(#11, #9). Expressions now compare large integers exactly (#10) and
order NaN the IEEE 754 way (#9), so some values that passed a rule
before now fail it. A failed rule on an enum reports the value by name.
protocheck now imports the shared schema types from
trendvidia/protos and its module graph names no buf.build module.

### Added

- **Enum value names, `defined()` and `unique()` in `@validate` rules**
  (#11, RFC-001 §5.4 as amended by trendvidia/protowire#432). A rule can
  name an enum value (`this in [MODE_MATCH, Mode.MODE_REGULATE]`); the
  engine takes its number from `Expression.enum_values`, which the
  compiler records (trendvidia/protocompile#275), and never resolves a
  name itself. `defined()` is true when an enum value is declared in its
  enum, and `unique()` when no two list elements are equal under `==`.
  Both agree with the bracket rules `enum.defined_only` and
  `repeated.unique` on the same inputs. An enum still binds as its
  number for every operator and for declared functions.

- **`is_nan()` builtin** (#9, RFC-001 §5.4), in method and function form:
  true for a floating-point NaN, false for every other number. A negated
  ordering such as `!(this < 0.0)` still accepts NaN, so
  `!this.is_nan()` is how a rule refuses it. In `@validate` rules it needs
  protocompile v0.36.0 or later, which knows the builtin
  (trendvidia/protocompile#265).

### Changed

- **Dependencies**: protocompile v0.35.1 → v0.36.1, which records enum
  value names in `Expression.enum_values` and checks expression defaults
  and boolean rules (trendvidia/protocompile#275, #286), and protowire-go
  v1.12.0 → v1.12.1 (protovalidate adapter module too; the adapter also
  takes the buf.validate protos' generation revision .2). protowire-go
  v1.12.1 no longer requires buf.build modules, so protocheck's own module
  graph names none (`go mod graph | grep -c buf.build`: 2 → 0); the
  protovalidate adapter still depends on protovalidate, which buf.build
  hosts. Adds
  trendvidia/protos v0.1.2 as a direct dependency: the schema types
  (`schemav1`) are imported from `github.com/trendvidia/protos` instead of
  protocompile's alias package `gen/protowire/schema/v1` (#16), so
  protocheck links no `protocompile/gen` package.

- **Tests compile against the shared schemas in `protos.Files`**, the
  protos version `go.mod` pins, instead of copies under `testdata/`.
  `testdata/protowire/schema/v1/annotations.proto` and
  `testdata/pxf/annotations.proto` are gone, so a protos bump is the only
  re-sync these files need.

- **A failed `@validate` rule reports an enum value by name** (#11).
  `ActualValue` was the value's number; it is now an `EnumName` holding
  the enum-qualified name (`"t.Mode.MODE_MATCH"`), which converts to the
  wire `Value`'s `enum_name`. A number the enum does not declare has no
  name and is still reported as its number. Code that type-asserts an
  enum's `ActualValue` to `int` needs to handle `EnumName` too.

### Fixed

- **Large integers compare exactly in expressions** (#10). Every number
  widened to float64 before comparing, and a float64 holds integers
  exactly only up to 2^53, so `this < 9007199254740993` was false for
  `this = 9007199254740992` and the two compared equal. Integers now
  compare as integers across the whole int64 and uint64 ranges (including
  signed against unsigned), and a float against an integer compares by
  exact value.

- **NaN no longer passes range checks in expressions** (#9). The built-in
  language ordered NaN as equal to every number, so
  `@validate(this >= 0.0 && this <= 1.0)` accepted NaN. NaN now follows
  IEEE 754, as RFC-001 §5.4 states since trendvidia/protowire#429: `<`,
  `<=`, `>` and `>=` are false with a NaN on either side, `==` is false
  and `!=` is true. This applies to `@validate` rules and to `check`
  expressions evaluated by the built-in language.

## [2.7.1] - 2026-10-28

### Changed

- **Dependencies**: protowire-go v1.5.0 → v1.12.0 (protovalidate adapter
  module: v1.10.0 → v1.12.0), protocompile v0.20.0 → v0.35.1, and
  google.golang.org/protobuf v1.36.11 → v1.36.12.

## [2.7.0] - 2026-10-28

The repository was re-created ahead of going public, with one squashed
commit. Entries below no longer cite issue or pull-request numbers from
before that point; links to other repositories are unchanged.

### Fixed

- **`check` rules on message-typed fields are evaluated**. They were
  skipped, so they always passed. Without an engine, `this` binds per
  RFC-001 §6.2 (a Timestamp as `time.Time`, a Duration as
  `time.Duration`, a wrapper as its unwrapped value, any other message as
  itself), and an unset field is not evaluated (§6.1). With an engine
  configured, the rule reports a violation naming the engine, because
  `EvalField` takes scalar values only; it no longer passes unevaluated.

- **`values.check` on message-valued map fields is evaluated**. It was
  skipped, so it always passed. With an engine it goes to `EvalMessage`, as
  repeated message items already did.
- **Repeated message items and map values bind `this` per RFC-001 §6.2**
  in the built-in language. A Timestamp, Duration or wrapper item was
  bound as the raw message, so every comparison failed with an evaluation
  error (`cannot order *timestamppb.Timestamp against time.Time`); it now
  binds as `time.Time`, `time.Duration` or the unwrapped value. Other
  messages bind as before.

### Changed

- **`protocheck.proto` is published on the BSR** as
  [`buf.build/trendvidia/protocheck`](https://buf.build/trendvidia/protocheck),
  labelled with each release tag. The README now shows a `deps` entry
  instead of vendoring the file, and the managed-mode rule that keeps
  generated code importing `protocheckpb`.
- **`check` expressions work without an expression engine.** With no
  `WithExpressionEngine`, a protocheckpb `check` rule used to report
  `no expression engine configured`. It is now evaluated in the RFC-001
  §5.4 language that `@validate` rules use: `this` is the field value, or
  the message in a message-level rule, and functions registered with
  `WithFunction` are callable by full name or by a suffix only one of them
  has. The language has no field access or arithmetic, so a cross-field
  rule is a registered function taking the message. Source written for an
  engine's own language fails to parse and reports a violation; it never
  passes. A configured engine still evaluates every `check` expression.
- **README rewritten around the built-in expression language**. It still
  described protocheck as powered by a specific engine and paired `protocheck.New()`
  with `check` rules that then failed. It now documents the expression
  language, cross-field rules through registered functions, plugging in
  another engine, CEL through protovalidate, and the buf managed-mode
  override order for vendoring `protocheck.proto`.
- `go.mod` no longer replaces `google.golang.org/protobuf` with the
  `trendvidia/protobuf-go` fork. The replace only existed so the
  round-trip harness, removed in 2.4.0, could build the fork's
  `protoc-gen-go`; consumers never saw it, since `replace` applies only to
  the main module. This module now builds and tests against the same
  upstream protobuf its consumers get (v1.36.11).
- Nested `protovalidate` module: protovalidate v1.2.0 → **v1.4.0**,
  protowire-go v1.3.1 → v1.10.0. protowire-go v1.10.0 requires a 2024
  build of the protovalidate Go bindings that protovalidate v1.2.0 cannot
  compile against (`undefined: validate.FieldPath`); v1.4.0 requires a
  newer build, which wins. Released under its own `protovalidate/v0.2.0` tag.

## [2.6.0] - 2026-09-07

### Added

- **`now()` and temporal ordering in `@validate` rules**. RFC-001
  §5.4 (trendvidia/protowire#282) made this engine's dialect the one
  normative expression language and added the `now()` §6.2 already
  promised. `now()` is a receiverless builtin bound once per `Validate`
  call, so every rule in one run sees the same instant (§6.2 rule 5);
  `now(x)` and `this.now()` are parse errors. A `google.protobuf.Timestamp`
  field now binds `this` as `time.Time` and a `Duration` as
  `time.Duration` (§6.2 rule 2), and `==` `!=` `<` `<=` `>` `>=` order
  temporal values of the same kind; a temporal against a number or a
  string is an evaluation error. A violation's `actual_value` renders a
  Timestamp as RFC 3339 and a Duration as Go's duration string.
  Conformance fixture `24_expression_language.proto` (every builtin in
  both forms, `now()`, every operator) is vendored and evaluates end to
  end.

- **Wrapper fields bind unwrapped** (RFC-001 §6.2 rule 1).
  `google.protobuf.StringValue`, `Int64Value` and the other wrappers now
  bind `this` as the wrapped scalar, so `this.starts_with("a")` on a
  `StringValue` field works as the RFC reads; before, `this` was the
  wrapper message and every string builtin failed on it.

### Fixed

- **`bytes` fields bind for carrier rules.** `protoValueToGo` had no
  `bytes` case, so a `@validate` on a `bytes` field evaluated with `this`
  as nil (`size is not defined on <nil>`); fixture 24's `token` rule is
  the first in the corpus to exercise one.

## [2.5.0] - 2026-08-31

### Changed

- **Constraint extension numbers move to protowire's registered block**
  (trendvidia/protowire#244). The three constraint extensions leave the
  unregistered `51000`-series squat for the slice of `1314`-`1363` allocated
  to validation constraints — the block granted to protowire in
  [protocolbuffers/protobuf#28919](https://github.com/protocolbuffers/protobuf/pull/28919):

  | | | |
  |---|---|---|
  | `protocheck.field` | `51000` | `1347` |
  | `protocheck.message` | `51001` | `1348` |
  | `protocheck.oneof` | `51002` | `1349` |

  **This is a descriptor-contract break.** A `.proto` compiled against the
  old `protocheck.proto` carries `51000`-series numbers and this release does
  not read them: constraints silently stop being found, so a message that
  previously failed validation now passes. There is no dual-read. Recompile
  schemas against the matching `protocheck.proto`.

  The Go API is unchanged — no exported function, type or signature moves.
  `protocheckpb` is regenerated with `buf`.

- `testdata/pxf/annotations.proto`, the vendored copy of protowire's PXF
  annotations, moves to `1314`-`1316` in step. Leaving it behind made every
  keyed test fail: `(pxf.key)` stopped resolving and keyed repeated fields
  degraded to plain repeated fields with no error.

- `go.mod`: `protowire-go` v1.3.1 → **v1.5.0**, the release that reads the
  renumbered PXF annotations.

The nested `protovalidate` module is unchanged and keeps its own tag.

## [2.4.0] - 2026-07-25

### Added

- Map KEY validation on the carrier path (RFC-001 §6.4 as amended by
  protowire#141/#153):
  key-type alias rules — carried by the §8 lowering on the synthetic
  entry's key field — now evaluate per entry key with `ForKey` set, the
  key type's own chain in `TypeChain`, and the entry's subscripted path
  (identical to the value dimension's; `ForKey` disambiguates, §7).
  Within one entry the key dimension precedes the value dimension, per
  the synthetic entry's field order; entries dispatch in sorted key
  order so reports are deterministic under protoreflect's randomized
  map iteration. Map-field carrier entries themselves stay
  per-value-only. Conformance fixture
  `15_collections_golden` is vendored and pinned by golden equality.
- The nested `github.com/trendvidia/protocheck/protovalidate` module gets
  its first tag, `protovalidate/v0.1.0` — it was previously importable
  only at a pseudo-version. It versions independently of protocheck core.

### Changed

- `Report.Proto()` / `ReportFromProto` now carry `ForKey` and
  `SourceRef` (wire `for_key`, protocompile#116; wire `source_ref`,
  protowire#142/#161):
  the two documented lossy edges are gone and the conversion is
  lossless in both directions. Requires protocompile ≥ v0.20.0
  bindings (pin bumped). The vendored fixture-07 golden gains the
  `source_ref` every violation now carries on the wire.
- Collection-field rules keep their source-map locations (the rule's
  annotation site — the alias's `@validate` for per-element rules, the
  field's own `@validate` for collection-level rules) instead of the
  fixture-07 declaration-site override, which now applies to singular
  fields only — matching the fixture-15 golden's pins.

### Removed

- The `roundtrip/` test package: the RFC-001 §9.3 end-to-end round trip
  moved back to trendvidia/protowire (`internal/schemaext`, next to the
  canonical fixtures — trendvidia/protowire#157), viable now that this
  module's dependency graph is fully public. The `protowire` module
  dependency goes with it.

## [2.3.0] - 2026-07-24

protocheck is now the engine-agnostic validation layer of RFC-001: the
message walk, violation/report model, §9.1 engine SPI, §9.2 verification,
and the carrier expression dialect stay here; engine-language expression
evaluation is pluggable (§9.4). This release removes the expression
engine dependency entirely, clearing the path to open-sourcing this module.

> **Breaking (within v2):** `WithFunction` now accepts only §9.2 SPI
> `Function`s, and protocheckpb Check expressions require a configured
> engine. Kept a minor bump deliberately: the module is private and being
> re-cut for open-sourcing; re-majoring to v3 was considered and skipped.

### Added

- **`ExpressionEngine` seam + `WithExpressionEngine`** — engines
  implement `Info`/`EvalField`/`EvalMessage`/`Close`; the previously
  built-in engine moved out of this module.
  With no engine configured, structural and RFC-001 carrier rules still
  run; hitting a protocheckpb Check expression reports a violation naming
  the missing engine. Engine-free reports identify as `protocheck-go`.
- **protovalidate adapter adopted** as nested module
  `github.com/trendvidia/protocheck/protovalidate`, relocated from
  `protowire-go/check/protovalidate` (deprecated in place): seam-level
  engine adapters live with the abstraction layer.
- **MIT LICENSE** (same as protowire), at the repo root and copied into
  the protovalidate nested module (nested-module zips don't inherit the
  root license).

### Changed

- `WithFunction` is SPI-only; engine-native reflective Go functions
  (`check.<name>(...)`) register on the engine
  instead. The engine-specific SPI facade is renamed `spiEngine`
  internally.

### Removed

- The expression engine dependency (and its transitive closure) from this
  module's graph.

## [2.2.0] - 2026-07-24

### Added

- **`ReportFromProto`** — the wire → Go half of the report conversion,
  completing the round trip: `Proto ∘ ReportFromProto` is the identity
  on wire Reports (property-tested over every `Value` kind, subscript
  type, and rule kind, plus the vendored conformance golden). New
  captured-value marker types `EnumName` and `Null` keep the wire's
  enum-name and explicit-null kinds distinct from plain strings and
  absence; `Any`-wrapped messages pass through without double-wrapping.
  Documented lossy edges: `ForKey` (bindings predate wire `for_key`,
  protocompile#116) and `SourceRef` (no wire descriptor-path key —
  proposed upstream as protowire#142).

## [2.1.0] - 2026-07-24

The v2.1 follow-up batch plus the
protowire-go decoder seam: per-element collection rules, `@sensitive`
redaction, `RULE_KIND_DEFAULT`, and wire-report emission with fixture-07
cross-port golden conformance.

### Added

- **Wire-report conversion + fixture-07 golden conformance** (RFC-001
  goal 5).
  `Report.Proto()` renders the `protowire.schema.v1.Report` wire message
  (protocompile's generated bindings) — the shape gRPC adapters embed in
  `google.rpc.Status.details` (§6.6 rule 6). The §5.3 worked-example
  fixtures (protowire v1.4.0, vendored with one documented delta —
  protowire#140) validate to a Report **semantically equal to the
  cross-port golden**, asserted in tests with the golden's own
  exclusions (engine, wall_time_nanos). Two semantics were aligned to
  the golden in the process: `EnrichedViolation.Source` now cites the
  violated element's *declaration site* in the consuming file (from
  SourceCodeInfo, falling back to the rule's annotation location when
  debug info is absent — previously alias-expanded rules pointed at the
  alias's file), and the expression dialect gained the engine builtins
  in function form (`matches(this, p)`, `ends_with`, `starts_with`,
  `contains`, `size`) per §8.1's presumed-builtin rule.
  `EnrichedViolation.ForKey` is not yet converted — the generated
  bindings predate wire `for_key` (protocompile#116).
- **`RULE_KIND_DEFAULT` emission** (RFC-001 §6.4 as pinned by
  protowire#133):
  violations from rules evaluated against a `@default`-substituted value
  now carry `RuleKindDefault`, superseding the rule's own
  `VALIDATE`/`TYPE_REFINEMENT` kind — marking a schema-authoring error
  (the declared default itself fails the field's rules). `ActualValue`
  carries the substituted default; rule origin stays recoverable from
  `Cause.Code` and `TypeChain`.

- **`@sensitive` redaction** (RFC-001 §6.7). Violations
  on fields classified `@sensitive` withhold the offending value:
  `EnrichedViolation.ActualValue` stays nil and the new `ValueRedacted`
  (wire `value_redacted`) is set, distinguishing redaction from genuine
  absence — `@required` violations keep `ValueRedacted` false since no
  value was captured. Classification follows the annotation library's
  contract: directly on a field, via type-alias expansion, or on a
  message — inherited by all its fields and transitively through
  message-typed (and map-value) fields. The transitive closure is
  type-level, so a message type reachable from a sensitive message is
  treated as sensitive everywhere (the safe over-approximation).

- **`protowire` adapter package**
  ([protowire-go#49](https://github.com/trendvidia/protowire-go/issues/49)
  Phase 3): adapts the engine to protowire-go's `check.Validator` seam
  so the pxf / pb / sbe decoders run RFC-001 validation post-decode.
  `New(...)` passes options through to `protocheck.New`; `Wrap(...)`
  adapts an already-held engine. Violations map to `check.Report` with
  rule IDs namespaced `rfc001.…` (`rfc001.string.min_len`,
  `rfc001.protowire.required`); map-key violations (`ForKey`) keep the
  entry path with a `map key: ` message prefix. Engine failures and
  non-proto values propagate as engine errors, never `*check.Error`.
  protowire-go bumped to a post-seam pseudo-version (v1.3.1-0.2026…).
- **Carrier rules on repeated and map fields** (RFC-001 §6.4).
  Alias-expanded `@validate` rules on a collection field now apply **per
  element** — `this` binds to each element, violations carry subscripted
  paths (`emails[1]`, key subscripts on keyed lists), `TYPE_REFINEMENT`
  kind, the type chain, and the element as `ActualValue`. Directly
  written `@validate` on the field applies to the **collection as a
  whole** (`this` binds to `[]any` / `map[string]any`; `size()` supports
  both), runs even when the collection is empty, and reports at the
  unsubscripted field path. Map *value* aliases lower as of
  protocompile v0.19.0 (see below) and dispatch per entry value with
  key-subscripted paths (`contacts[bob]`); map *key* aliases stay on the
  synthetic entry's key field.

### Changed

- protocompile upgraded v0.18.0 → v0.19.0
  ([release notes](https://github.com/trendvidia/protocompile/releases/tag/v0.19.0)):
  - **Map value aliases now lower onto the map field's own
    `AnnotationList`** (protocompile#112, fixes protocompile#109) — the
    carrier walk now dispatches `TYPE_REFINEMENT` rules per map entry
    value (was: silently skipped). `baseTypeName` roots a map field's
    refinement chain at the *value* type (`["string", "coll.Email"]`,
    not the synthetic `…Entry` message).
  - **Typed `map_entry` options** (protocompile#113, fixes
    protocompile#110): dropped the `restoreMapEntryOptions` test
    workaround that re-marked synthetic map entries before
    `protodesc.NewFiles`.
  - Public `Report`/`Violation`/`ExecutionMode` bindings now generated
    into protocompile's `gen/protowire/schema/v1` (protocompile#111) —
    available for the wire-report work (§6.6 rule 6); not yet consumed
    here.

- protocompile upgraded v0.17.1 → v0.18.0, which resolves
  [protocompile#101](https://github.com/trendvidia/protocompile/issues/101):
  alias-expanded annotations and `type_chain` links are now lowered
  base-first, matching RFC-001 §6.4 and the fixture-07 golden. No
  protocheck behavior change — the carrier index reconstructs rule order
  from `TypeDecl.base_type_fqn` links and attributes alias rules by
  declaration-site location, so it is independent of carrier emission
  order — but descriptor-path ordinals in `SourceRef` now reflect the
  corrected base-first positions for fields with multiple same-named
  annotations.

## [2.0.0] - 2026-07-24

The RFC-001 schema-extensions release: the clean-break v2 with the wire-aligned violation model,
the engine SPI, carrier-rule validation, and catalog-based i18n.

### Changed

- **BREAKING: wire-aligned violation model** (RFC-001 §7). The Go
  `Violation{FieldPath, ConstraintID, Message}` is replaced by the shapes
  pinned in protowire's `proto/schema/v1/report.proto`: `Violation` is now
  the engine-independent cause — `Code`, `Params` (structured, for
  locale-catalog interpolation), `FallbackMessage` — and
  `ValidationError.Violations` / `Report.Violations` carry
  `*EnrichedViolation` wrapping it with a structured `Path`
  (`FieldPath` segments with field names, numbers, and typed subscripts:
  `Index`, `StringKey`, `IntKey`, `UintKey`, `BoolKey`), the `RuleKind`
  (`VALIDATE`/`REQUIRED`/`DEFAULT`/`TYPE_REFINEMENT`), and `ForKey` for
  map-key violations (mirroring `EnrichedViolation.for_key`, added to the
  wire shape in protowire v1.3.0). Map entry paths drop the `.key`/`.value` suffixes:
  the subscript addresses the entry value per the wire contract, and
  `ForKey` marks key-rule violations. Standard rules now populate
  `Params` with their thresholds keyed by rule field name
  (`{"min_len": 3}`, `{"pattern": ...}`, ...).
- **`Report.Format(locale)`** renders one line per violation with messages
  localized through the catalog registered for that locale via
  `Engine.RegisterCatalog`; catalog misses fall back to
  `FallbackMessage`.

- **Module path is now `github.com/trendvidia/protocheck/v2`** — the
  RFC-001 M4 chain lands as a
  clean-break v2.0.0. v2 development happens on `main` with the `/v2`
  module suffix; v1 consumers are unaffected until they opt in with
  `go get github.com/trendvidia/protocheck/v2`.

- **Expression evaluation is ~12–15× faster** with ~60× less allocation. Expression
  rules now evaluate on a bounded per-Env `eval.EvalPool` (VM reuse) instead
  of constructing a fresh VM — with full stdlib re-registration — per
  evaluation: ~194µs/712KB → ~13–16µs/12KB per Validate on the benchmark
  fixtures. Standard rules are unaffected. (Compiled programs were already
  cached by the engine, so recompilation was not actually occurring; per-eval VM construction was the real cost.)

### Added

- **Catalog support + i18n** (RFC-001 §7), completing
  the protocheck side of the RFC-001 schema-extensions chain.
  `NewMapCatalog(entries)` is the in-memory reference `Catalog`: entries
  keyed by violation code, message templates with `{param}` placeholders
  interpolating the violation's `Params` (unmatched placeholders pass
  through verbatim so missing translations stay visible).
  `WithCatalog(locale, cat)` registers catalogs at construction — the
  Option counterpart of `Engine.RegisterCatalog`, sharing one registry
  and duplicate-locale checks. `Report.Format(locale)` renders through
  the locale's catalog with per-violation fallback on any miss (unknown
  locale or missing code).
- **Constraints over keyed repeated fields** (protowire#116). Elements of
  a `repeated <Message>` field carrying a valid `(pxf.key)` annotation
  are addressed by their key value — `endpoints[admin].url` instead of
  `endpoints[1].url`, matching the entry name in PXF sources — with an
  index fallback when the key is unset. `repeated.unique` on a keyed
  field asserts uniqueness of the key values (the anonymous list form
  admits duplicates at the data level), positioned on the duplicate
  entry with `params: {key}`. `items.check` expressions now run per
  message element with the element's fields — the key included — in
  scope. Keyed-ness resolves through `pxf.KeyField` from protowire-go
  (now a direct dependency, v1.3.0).

- **RFC-001 carrier-rule validation** (§6.4/§8): messages whose
  descriptors carry lowered `protowire.schema.v1` annotations (extension
  50400) are now validated against them — `@required` (code
  `protowire.required`), `@default` substitution (rules evaluate against
  the substituted value), and `@validate` rules, including rules
  macro-expanded from `type` aliases. Rules evaluate base → derived →
  field-level regardless of carrier emission order. `@validate`
  expressions are parsed at descriptor-load time by protocheck's own
  RFC-expression dialect (comparisons, `in` lists, `&&`/`||`/`!`, string
  builtin methods, and declared-function calls dispatched through the
  §9.2 registry — Unimplemented placeholders surface as `unimplemented`
  violations). Violation codes follow the §7 ladder: use-site `code`
  override, else the function-returned code, else the function
  declaration's `[error_code]`, else a code derived from the function
  FQN. Conformance fixtures 01–06 (vendored under `testdata/`) validate
  correctly under collect-all and fail-fast.
- **Violation enrichment completed** (`EnrichedViolation`): `TypeChain`
  (base-first refinement provenance), `RuleKind`
  `TYPE_REFINEMENT`/`REQUIRED` emission, `ActualValue` capture, `Source`
  (from the embedded source map, carrier 50404), and `SourceRef` — the
  `(file, descriptor_path)` key of the rule in the canonical RFC-001
  §8.3.1 grammar, produced via `fdp.DescriptorPath`.
- **Function registration + init-time verification** (RFC-001 §9.2).
  `WithDescriptors(files...)` makes `New` walk the supplied schemas and
  reconcile every function FQN they declare (carrier 50401) or call from a
  lowered annotation expression (carrier 50400, `Expression.calls`) against
  the registered implementations. Missing-impl default is lenient: an
  `UnimplementedFunction` placeholder that fails at first call with a
  `"unimplemented"` violation; `WithStrictValidation()` turns misses into
  `New` errors naming each FQN with its call-site source location.
  Implementations register at construction via
  `WithFunction(fqn, protocheck.Function(...))` or afterwards via
  `Engine.Register`, which now upgrades placeholders in place. Adds
  `github.com/trendvidia/protocompile` (the `protowire.schema.v1` carrier
  bindings; linking them also lets in-process descriptor unmarshals resolve
  extensions 50400–50404).
- **Engine SPI** (RFC-001 §9.1): `Engine`,
  `Function`, `Catalog`, and `Report` — the implementation-neutral contract
  between schema-extension tooling and a validation engine, mirroring the
  wire shape pinned in protowire's `proto/schema/v1/report.proto`
  (`ExecutionMode`, `EngineInfo`, `Truncated`, `WallTimeNanos`).
  `Validator.Engine()` returns the SPI view backed by the existing
  validator; `protocheck.New(...)` and direct `*Validator` use
  are unchanged. Functions registered via `Engine.Register` are recorded
  for the descriptor-declared function binding.
- **`WithFailFast()`** — opt-in fail-fast validation (RFC-001 §6.4). Validate stops
  at the first violation and reports exactly one; the message walk
  short-circuits, skipping remaining rules, nested messages, and repeated/map
  elements. The default remains collect-all.
- **`Validator.Close()`** releases the pooled evaluation VMs. Optional for
  process-lifetime validators; must not race an in-flight Validate.
- `BenchmarkValidate*` benchmarks and a shared-validator concurrency race
  test.

### Fixed

- **Message-typed list elements and map values are now always validated
  against their own constraints.** Previously, elements of a repeated
  message field were only recursed into when `repeated.items` rules were
  present, and map message values only when `map.values` rules were —
  constraint-carrying messages nested in collections were otherwise
  silently skipped.
- `readExtension` falls back to decoding unknown-field bytes, so
  protocheck constraints are honored on descriptors whose options were
  built without this process's extension registry — a
  `FileDescriptorSet` compiled by an external toolchain, or
  `FileDescriptorProto`s assembled programmatically.
- **uint64/fixed64 fields no longer wrap negative in expression bindings**. Unsigned fields
  were narrowed through `int` before binding, so a value above `MaxInt64`
  wrapped negative and a range expression like `this < 1000` wrongly passed
  for `18446744073709551615`. Unsigned kinds now bind as `eval.Uint`
  (an expression engine upgrade) and reach expressions — and
  `WithFunction` customs with unsigned parameters — unnarrowed. Standard
  numeric rules were always correct and are unchanged.
- **`WithFunction` customs with narrow numeric parameters (`int32`,
  `float32`, ...) no longer panic at call time.** The native bridge passed a
  plain `int`/`float64` to `reflect.Value.Call`, which rejects mismatched
  widths; arguments are now converted to the declared parameter type.

### Changed

- Expression engine upgraded (adds `eval.Uint`).
- **Expression engine upgraded.** protocheck's integration surface is
  unchanged across the range, and the one breaking change applies only to
  the engine's command-line tools, so validation behavior is unaffected.
  Transitive bumps:
  protowire-go v1.2.2, x/crypto v0.51.0, x/net v0.55.0, x/sys v0.45.0.
- **`WithFunction` natives now convert values through the calling VM's own
  handle table**, making custom functions
  safe by construction when multiple eval VMs execute concurrently in one
  process. Covered by a new concurrent-validators race test.

## [1.2.0] - 2026-07-01

### Changed

- **Moved to the expression engine's v2 import path.**
  No protocheck API changes; validation behavior is unchanged.

## [1.1.0] - 2026-06-04

### Added

- **`bytes.in` / `bytes.not_in`** are now implemented. They were declared in the
  proto schema but previously silent no-ops; values are now compared with
  `bytes.Equal`.
- **End-to-end test for protocompile-shaped descriptors** (`Validate` against
  `dynamicpb.NewMessage` whose option extensions are `*dynamicpb.Message`).
- **Test backfill**: bytes rules, untested string rules (`const`, `len`,
  `len_bytes`, `min_bytes`, `max_bytes`, `prefix`, `suffix`, `contains`,
  `not_contains`, `uri_ref`), numeric rules across `float`/`double`/`int32`/
  `int64`/`uint32`/`uint64` (including `const`, `finite`, `in`, `not_in`),
  enum `const`/`not_in`, bool `const`, ignore-mode semantics, disabled-message
  nested recursion, expression compile/eval errors, and `WithFunction` error
  paths.

### Fixed

- **`IGNORE_IF_DEFAULT_VALUE` now matches its documented behavior.** It was
  previously a silent alias of `IGNORE_IF_UNPOPULATED` (skip only when
  `msg.Has(field) == false`). It now also skips when a proto3 `optional` field
  is explicitly set to its zero value, matching the README and protovalidate
  semantics. **Behavior change**: any code that set a proto3 `optional` field
  to the default expecting validation to still run will see those validations
  stop running. Switch to `IGNORE_IF_UNPOPULATED` if you need the old shape.
- **`bytes.ip` length check.** Previously `net.IP(val) == nil` was always false
  for any non-nil slice, so any non-empty bytes passed as a "valid IP." Now
  requires exactly 4 or 16 bytes.
- **`Validate` no longer panics on dynamicpb-shaped descriptors.** Extension
  reads use `protoreflect` directly instead of `proto.GetExtension`, falling
  back to marshal + unmarshal into the concrete type when the stored value is
  `*dynamicpb.Message`.

### Changed

- **Build tooling migrated from `protoc` to `buf`.** Adds `buf.yaml` and
  `buf.gen.yaml` (remote `buf.build/protocolbuffers/go` plugin, no local
  `protoc-gen-go` install required). The `Makefile` now exposes `generate`,
  `lint`, `format`, `check`, `build`, `test`, `tidy`, and `clean` targets.
- **Generated code updated** to `protoc-gen-go` v1.36.5 from the long-stale
  v1.23.0; the unused `github.com/golang/protobuf` dependency is removed.

## [1.0.1] - 2026-06-03

- Expression engine bumped for the protowire-go migration.

## [1.0.0] - 2026-04-21

- Initial release.

[Unreleased]: https://github.com/trendvidia/protocheck/compare/v2.8.0...HEAD
[2.8.0]: https://github.com/trendvidia/protocheck/compare/v2.7.1...v2.8.0
[2.7.1]: https://github.com/trendvidia/protocheck/compare/v2.7.0...v2.7.1
[2.7.0]: https://github.com/trendvidia/protocheck/releases/tag/v2.7.0
