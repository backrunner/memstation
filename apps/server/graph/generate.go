package graph

//go:generate bash -lc "go run github.com/99designs/gqlgen generate && go run golang.org/x/tools/cmd/goimports@v0.52.0 -w resolver/schema.resolvers.go && go run mvdan.cc/gofumpt@v0.12.0 -w resolver/schema.resolvers.go"
