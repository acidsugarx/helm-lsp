package main

import (
	"log"
	"os"

	"github.com/acidsugarx/helm-lsp/pkg/lsp"
)

func main() {
	// Configure logging to a file to avoid polluting stdout (LSP JSON-RPC)
	logFile, err := os.OpenFile("/tmp/helm-lsp.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err == nil {
		log.SetOutput(logFile)
	} else {
		log.SetOutput(os.Stderr)
	}
	log.Println("Starting helm-lsp...")

	srv := lsp.NewServer()

	if err := srv.Run(); err != nil {
		log.Printf("LSP server stopped with error: %v\n", err)
		os.Exit(1)
	}
}
