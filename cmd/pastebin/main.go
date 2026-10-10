package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"pastebin/internal/httpserver"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	flag.Parse()
	server := &http.Server{
		Addr:              *addr,
		Handler:           httpserver.New(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       60 * time.Second,
		// SSE sets a deadline per write; a server-wide write timeout would
		// otherwise terminate healthy long-lived streams.
	}
	log.Printf("listening on %s", *addr)
	log.Fatal(server.ListenAndServe())
}
