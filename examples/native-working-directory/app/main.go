// Package main serves package-local configuration over an allocated loopback port.
package main

import (
	"log"
	"net"
	"net/http"
	"os"
)

func main() {
	message, err := os.ReadFile("message.txt")
	if err != nil {
		log.Fatal(err)
	}
	log.Fatal(http.ListenAndServe(net.JoinHostPort("127.0.0.1", os.Getenv("PORT")), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(message)
	})))
}
