package lsp

import (
	"sync"
)

// DocumentStore thread-safely manages text documents sent by the client.
type DocumentStore struct {
	mu   sync.RWMutex
	docs map[string]string // URI -> Content
}

// NewDocumentStore creates a new document store.
func NewDocumentStore() *DocumentStore {
	return &DocumentStore{
		docs: make(map[string]string),
	}
}

// Set stores or updates a document's content.
func (s *DocumentStore) Set(uri, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs[uri] = content
}

// Get retrieves a document's content.
func (s *DocumentStore) Get(uri string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	content, ok := s.docs[uri]
	return content, ok
}

// Delete removes a document from the store.
func (s *DocumentStore) Delete(uri string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.docs, uri)
}
