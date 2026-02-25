package main

import (
	"log"
	"os"

	"github.com/acidsugarx/helm-lsp/internal/handler"
)

type stdIODev struct{}

func (stdIODev) Read(p []byte) (int, error) {
	return os.Stdin.Read(p)
}

func (stdIODev) Write(p []byte) (int, error) {
	return os.Stdout.Write(p)
}

func (stdIODev) Close() error {
	if err := os.Stdin.Close(); err != nil {
		return err
	}
	return os.Stdout.Close()
}

func main() {
	// Configure logging to a file to avoid polluting stdout (LSP JSON-RPC)
	logFile, err := os.OpenFile("/tmp/helm-lsp.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err == nil {
		log.SetOutput(logFile)
	} else {
		log.SetOutput(os.Stderr)
	}
	log.Println("Starting helm-lsp...")

	handler.StartHandler(stdIODev{})
}
