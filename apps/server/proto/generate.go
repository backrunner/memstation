package protoapi

//go:generate bash -lc "cd .. && go run github.com/bufbuild/buf/cmd/buf@v1.75.0 generate && go run golang.org/x/tools/cmd/goimports@v0.52.0 -w proto/memstation/v1/api.pb.go proto/memstation/v1/api_grpc.pb.go && go run mvdan.cc/gofumpt@v0.12.0 -w proto/memstation/v1/api.pb.go proto/memstation/v1/api_grpc.pb.go && go run mvdan.cc/gofumpt@v0.12.0 -w proto/memstation/v1/api.pb.go proto/memstation/v1/api_grpc.pb.go"
