// Nested module: the ConnectRPC interceptor. It lives in protocheck but stays
// its own module so protocheck core carries no Connect dependency.
module github.com/trendvidia/protocheck/protocheckconnect

go 1.26.2

require (
	connectrpc.com/connect v1.21.1
	github.com/trendvidia/protocheck/v2 v2.10.1
	github.com/trendvidia/protos v0.1.4
	google.golang.org/protobuf v1.36.12
)

require (
	github.com/trendvidia/protocompile v0.38.0 // indirect
	github.com/trendvidia/protowire-go v1.13.0 // indirect
)
