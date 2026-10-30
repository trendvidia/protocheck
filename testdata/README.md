# Vendored RFC-001 conformance artifacts

Copied verbatim from trendvidia/protowire (MIT):

- `schema-extensions/` — conformance fixtures 01-06 and 24 (`testdata/schema-extensions/`)

Used as compile inputs for the carrier-dispatch tests. Re-copy when the
upstream fixtures change. The annotation libraries they import
(`protowire/schema/v1/annotations.proto`, `pxf/annotations.proto`) are
not copied: the tests read them from `protos.Files` in
github.com/trendvidia/protos, at the version `go.mod` pins.

## Local delta (remove when upstream syncs)

- `07_report_golden/myco/users/user.proto:40` adds
  `message = "country not supported"` to the country rule — the upstream
  fixture omits it while the golden pins that fallback_message
  (protowire#140). Same-line edit, so the golden's cited source
  locations stay valid.
