// Go Map Lab is a local educational web application about Go map internals.
//
// The executable deliberately contains almost no business logic. main only
// reads process-level configuration, assembles the HTTP server and starts the
// listener. The map algorithms live in internal/lab, unsafe memory inspection
// lives in internal/inspector, and HTTP routing lives in internal/server.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"go-map-internals-lab/internal/server"
)

// ServerAddress is the local TCP address accepted by http.Server.
// It is a semantic alias: the underlying value is still a regular string.
type ServerAddress = string

// HeaderReadTimeout limits how long a client may spend sending HTTP headers.
type HeaderReadTimeout = time.Duration

const defaultHeaderReadTimeout HeaderReadTimeout = 5 * time.Second

func main() {
	// Bind to loopback by default: this laboratory is intended to be opened from
	// the same computer, not exposed as a production web service.
	address := flag.String(
		"addr",
		"127.0.0.1:8080",
		"address for the local web interface",
	)
	flag.Parse()

	// server.New returns an http.Handler rather than starting its own listener.
	// That keeps process concerns here and makes the application independently
	// testable with httptest in internal/server.
	handler := server.New()
	httpServer := &http.Server{
		Addr:              ServerAddress(*address),
		Handler:           handler,
		ReadHeaderTimeout: defaultHeaderReadTimeout,
	}

	fmt.Printf("\nGo Map Lab запущен: http://%s\n", *address)
	fmt.Println("Остановить: Ctrl+C")

	// ListenAndServe normally returns http.ErrServerClosed during a graceful
	// shutdown. Any other error means the listener could not continue.
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
