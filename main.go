// Go Map Lab is a local educational web application about Go map internals.
//
// The project deliberately uses only the standard library. This keeps the
// launch process simple: "go run ." is enough to start the laboratory.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"go-map-internals-lab/internal/server"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "address for the local web interface")
	flag.Parse()

	handler := server.New()
	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	fmt.Printf("\nGo Map Lab запущен: http://%s\n", *addr)
	fmt.Println("Остановить: Ctrl+C")
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
