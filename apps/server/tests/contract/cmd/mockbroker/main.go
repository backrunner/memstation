// Explicit development fixture; never installed by application bootstrap.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"memstation/apps/server/tests/testutil/mockbroker"
)

func main() {
	address := flag.String("listen", "127.0.0.1:18490", "loopback listen address")
	flag.Parse()
	credential := os.Getenv("MEMSTATION_MOCK_BROKER_CREDENTIAL")
	if credential == "" {
		log.Fatal("MEMSTATION_MOCK_BROKER_CREDENTIAL is required")
	}
	server := &http.Server{Addr: *address, Handler: &mockbroker.Handler{Credential: credential}, ReadHeaderTimeout: 5 * time.Second}
	log.Print("deterministic broker fixture; no real model quality or cost evidence")
	log.Fatal(server.ListenAndServe())
}
