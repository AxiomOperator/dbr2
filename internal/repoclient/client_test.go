// SPDX-License-Identifier: Apache-2.0

package repoclient

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStateExportImport(t *testing.T) {
	var imported string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":"unauthorized","message":"bad token"}`))
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/state-export":
			w.Header().Set("Content-Type", StateContentType)
			_, _ = w.Write([]byte("TAR"))
		case "POST /v1/state-import":
			if r.Header.Get("Content-Type") != StateContentType {
				w.WriteHeader(http.StatusUnsupportedMediaType)
				return
			}
			b, _ := io.ReadAll(r.Body)
			imported = string(b)
			if imported == "again" {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"code":"already_initialized","message":"initialized"}`))
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	c := New(srv.URL, "tok")
	rc, err := c.StateExport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "TAR" {
		t.Fatalf("export body %q", b)
	}
	if err := c.StateImport(ctx, strings.NewReader("TAR")); err != nil || imported != "TAR" {
		t.Fatalf("import: %v %q", err, imported)
	}
	if err := c.StateImport(ctx, strings.NewReader("again")); !IsCode(err, "already_initialized") {
		t.Fatalf("want already_initialized, got %v", err)
	}
	if _, err := New(srv.URL, "bad").StateExport(ctx); !IsCode(err, "unauthorized") {
		t.Fatalf("want unauthorized, got %v", err)
	}
}
