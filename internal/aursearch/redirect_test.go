package aursearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestRedirectOffHostRefused (#271): the multi-info POST carries the user's
// installed package names; a redirect to another host (here a second
// loopback server, another port) must not be followed.
func TestRedirectOffHostRefused(t *testing.T) {
	var elsewhere atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhere.Add(1)
		reply(w, "multiinfo")
	}))
	t.Cleanup(other.Close)
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/rpc/v5/info", http.StatusTemporaryRedirect)
	})
	if _, err := c.Info(context.Background(), []string{"yay"}); err == nil {
		t.Error("followed a redirect to another host")
	}
	if elsewhere.Load() != 0 {
		t.Errorf("the other host got %d requests", elsewhere.Load())
	}
}

// TestRedirectSameHostFollowed: a redirect within the AUR's host still works.
func TestRedirectSameHostFollowed(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rpc/v5/moved" {
			reply(w, "multiinfo", map[string]any{"Name": "yay", "Version": "1-1", "PackageBase": "yay"})
			return
		}
		http.Redirect(w, r, "/rpc/v5/moved", http.StatusTemporaryRedirect)
	})
	if got, err := c.Info(context.Background(), []string{"yay"}); err != nil || len(got) != 1 {
		t.Errorf("= %v, %v", got, err)
	}
}
