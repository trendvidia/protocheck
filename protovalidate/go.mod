// Nested module: the protovalidate adapter for the protowire-go check
// seam, relocated from protowire-go/check/protovalidate. Lives in
// protocheck (the validator abstraction layer) but stays its own module
// so protocheck core carries none of the protovalidate dependency set.
module github.com/trendvidia/protocheck/protovalidate

go 1.26.2

require (
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.12-20260825204119-511051f7f437.2
	buf.build/go/protovalidate v1.4.0
	github.com/bufbuild/protocompile v0.14.1
	github.com/stretchr/testify v1.12.1
	github.com/trendvidia/protowire-go v1.12.1
	google.golang.org/protobuf v1.36.12
)

require (
	cel.dev/cel-go v0.32.0 // indirect
	cel.dev/expr v0.25.3 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/exp v0.0.0-20260820142414-ca536658362e // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260819154853-08b0e4226688 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260819154853-08b0e4226688 // indirect
)
