// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestReadinessIncludesPlatformServices(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer up.Close()
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer down.Close()

	d := &Deps{Ready: []ReadyCheck{
		{Name: "postgres", Critical: true, Check: func(context.Context) error { return nil }},
		HTTPReadyCheck("proxy", up.URL),
		HTTPReadyCheck("reposerver", down.URL),
	}}
	r := chi.NewMux()
	NewAPI(r, d)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/health/ready", nil))

	var body StatusBody
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	// A failing non-critical service degrades readiness but keeps HTTP 200.
	if w.Code != 200 || body.Status != "degraded" || body.Checks["proxy"] != "ok" || body.Checks["reposerver"] != "unavailable" {
		t.Fatalf("got %d %+v", w.Code, body)
	}
}
