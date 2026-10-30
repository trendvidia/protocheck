// Nested module: the ConnectRPC interceptor. It lives in protocheck but stays
// its own module so protocheck core carries no Connect dependency.
module github.com/trendvidia/protocheck/protocheckconnect

go 1.26.2

require (
	connectrpc.com/connect v1.21.1
	github.com/trendvidia/protocheck/v2 v2.8.1-0.20261030172514-206d9a7dedde
	github.com/trendvidia/protos v0.1.2
	google.golang.org/protobuf v1.36.12
)

require (
	github.com/petermattis/goid v0.0.0-20260113132338-7c7de50cc741 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/tidwall/btree v1.8.2 // indirect
	github.com/trendvidia/protocompile v0.36.1 // indirect
	github.com/trendvidia/protowire-go v1.12.1 // indirect
	golang.org/x/exp v0.0.0-20250911091902-df9299821621 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260807164820-c8921c73eeea // indirect
)
