package aurbuild

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Enzord758/archmaid/internal/aursearch"
)

// aurClientClosed is a client whose server is gone.
func aurClientClosed(t *testing.T) *aursearch.Client {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	return aursearch.New(aursearch.Config{BaseURL: base + "/rpc/v5"})
}
