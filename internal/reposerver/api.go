// SPDX-License-Identifier: Apache-2.0

package reposerver

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/kopia/kopia/repo/splitter"
)

// Backend is what the management API drives (implemented by *Service).
type Backend interface {
	Status(ctx context.Context) Status
	Initialize(ctx context.Context, password, splitter string) (Status, error)
	PutUser(ctx context.Context, username, password string) error
	DeleteUser(ctx context.Context, username string) error
	AddReadGrant(ctx context.Context, user, srcUser, srcHost string) (string, error)
	DeleteReadGrant(ctx context.Context, id string) error
}

// Validation limits of the management API.
const (
	MinRepositoryPassword = 32
	MinUserPassword       = 16
	maxPassword           = 1024
	maxBody               = 64 << 10
)

var (
	// UsernameRE is a Kopia server user name (user@host) DBR² may manage.
	UsernameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*@[a-z0-9][a-z0-9._-]*$`)
	// labelRE is one side (user or host) of a snapshot source.
	labelRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	aclIDRE = regexp.MustCompile(`^[0-9a-f]{16,64}$`)
)

// Problem is an RFC 9457 problem document.
type Problem struct {
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
	Code   string `json:"code"`
}

// API is the internal management HTTP API (bearer internal token).
type API struct {
	Backend Backend
	Token   string
	Log     *slog.Logger
}

// Handler returns the management API router.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", a.status)
	mux.HandleFunc("POST /v1/initialize", a.initialize)
	mux.HandleFunc("PUT /v1/users/{username}", a.putUser)
	mux.HandleFunc("DELETE /v1/users/{username}", a.deleteUser)
	mux.HandleFunc("POST /v1/acl/read-grants", a.addGrant)
	mux.HandleFunc("DELETE /v1/acl/read-grants/{id}", a.deleteGrant)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeProblem(w, http.StatusNotFound, "not_found", "Not found", "")
	})
	return a.auth(mux)
}

func (a *API) auth(next http.Handler) http.Handler {
	want := []byte("Bearer " + a.Token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if a.Token == "" || subtle.ConstantTimeCompare(got, want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="dbr2-reposerver"`)
			writeProblem(w, http.StatusUnauthorized, "unauthorized", "Unauthorized", "a valid internal token is required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeProblem(w http.ResponseWriter, status int, code, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Problem{Title: title, Status: status, Detail: detail, Code: code})
}

// fail maps backend errors to problems. Details never contain secrets:
// Kopia output is scrubbed by the runner.
func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrAlreadyInitialized):
		writeProblem(w, http.StatusConflict, "already_initialized", "Repository already initialized", err.Error())
	case errors.Is(err, ErrNotInitialized):
		writeProblem(w, http.StatusConflict, "not_initialized", "Repository not initialized", err.Error())
	case errors.Is(err, ErrStorageNotReady):
		writeProblem(w, http.StatusUnprocessableEntity, "storage_not_ready", "Repository storage not ready", err.Error())
	case errors.Is(err, ErrInvalidPassword):
		writeProblem(w, http.StatusUnprocessableEntity, "invalid_password", "Invalid repository password", err.Error())
	case errors.Is(err, ErrNotFound):
		writeProblem(w, http.StatusNotFound, "not_found", "Not found", "")
	default:
		a.Log.Error("management operation failed", "method", r.Method, "path", r.URL.Path, "err", err)
		var ke *KopiaError
		if errors.As(err, &ke) {
			writeProblem(w, http.StatusInternalServerError, "kopia_error", "Kopia command failed", err.Error())
			return
		}
		writeProblem(w, http.StatusInternalServerError, "internal", "Internal error", err.Error())
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBody))
	if err := dec.Decode(v); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "Invalid request body", err.Error())
		return false
	}
	return true
}

func invalid(w http.ResponseWriter, detail string) {
	writeProblem(w, http.StatusBadRequest, "invalid_request", "Invalid request", detail)
}

func validPassword(p string, minLen int) bool {
	return len(p) >= minLen && len(p) <= maxPassword && !strings.ContainsAny(p, "\x00\r\n")
}

func (a *API) status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.Backend.Status(r.Context()))
}

func (a *API) initialize(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
		Splitter string `json:"splitter"`
	}
	if !decode(w, r, &req) {
		return
	}
	if !validPassword(req.Password, MinRepositoryPassword) {
		invalid(w, "password must be 32 to 1024 characters without line breaks")
		return
	}
	if req.Splitter != "" && !slices.Contains(splitter.SupportedAlgorithms(), req.Splitter) {
		invalid(w, "unsupported splitter "+req.Splitter)
		return
	}
	st, err := a.Backend.Initialize(r.Context(), req.Password, req.Splitter)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (a *API) putUser(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("username")
	if !UsernameRE.MatchString(name) {
		invalid(w, "username must match "+UsernameRE.String())
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	if !validPassword(req.Password, MinUserPassword) {
		invalid(w, "password must be 16 to 1024 characters without line breaks")
		return
	}
	if err := a.Backend.PutUser(r.Context(), name, req.Password); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) deleteUser(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("username")
	if !UsernameRE.MatchString(name) {
		invalid(w, "username must match "+UsernameRE.String())
		return
	}
	if err := a.Backend.DeleteUser(r.Context(), name); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) addGrant(w http.ResponseWriter, r *http.Request) {
	var req struct {
		User       string `json:"user"`
		SourceUser string `json:"source_user"`
		SourceHost string `json:"source_host"`
	}
	if !decode(w, r, &req) {
		return
	}
	switch {
	case !UsernameRE.MatchString(req.User):
		invalid(w, "user must match "+UsernameRE.String())
		return
	case !labelRE.MatchString(req.SourceUser) || !labelRE.MatchString(req.SourceHost):
		invalid(w, "source_user and source_host must match "+labelRE.String())
		return
	case req.User == req.SourceUser+"@"+req.SourceHost:
		invalid(w, "a user already reads its own snapshots")
		return
	}
	id, err := a.Backend.AddReadGrant(r.Context(), req.User, req.SourceUser, req.SourceHost)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (a *API) deleteGrant(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !aclIDRE.MatchString(id) {
		writeProblem(w, http.StatusNotFound, "not_found", "Not found", "")
		return
	}
	if err := a.Backend.DeleteReadGrant(r.Context(), id); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
