package main

import (
	"context"
	"log"
	"os"
)

func main() {
	// The API key is validated lazily per tool call so a missing key surfaces
	// as a readable tool error in the client instead of a dead server.
	client := NewClient(os.Getenv("LANGDOCK_BASE_URL"), os.Getenv("LANGDOCK_API_KEY"))
	if err := NewServer(client).Run(context.Background()); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
