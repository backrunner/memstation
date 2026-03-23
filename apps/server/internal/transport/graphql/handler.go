package graphql

import (
	"net/http"

	gqlhandler "github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/playground"

	"memstation/apps/server/graph/generated"
	"memstation/apps/server/graph/resolver"
)

func Handler(resolver *resolver.Resolver) http.Handler {
	return gqlhandler.NewDefaultServer(generated.NewExecutableSchema(generated.Config{Resolvers: resolver}))
}

func Playground() http.Handler {
	return playground.Handler("MemStation GraphQL", "/graphql")
}
