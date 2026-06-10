package sshkeys

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/supabase-community/supabase-go"
)

// captured holds what the fake PostgREST endpoint observed for one request.
type captured struct {
	method string
	path   string
	query  string
	prefer string
	body   string
}

// newTestStore spins up an httptest server with the supplied handler and returns
// a Store backed by a real *supabase.Client pointed at that server. The client is
// constructed exactly as cli-foundation does it (schema "blacksail"), so requests
// hit <server>/rest/v1/ssh_keys and exercise the genuine postgrest-go transport.
func newTestStore(t *testing.T, handler http.HandlerFunc) *Store {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client, err := supabase.NewClient(srv.URL, "anon-key", &supabase.ClientOptions{Schema: "blacksail"})
	if err != nil {
		t.Fatalf("supabase.NewClient: %v", err)
	}
	return NewStore(client)
}

// readBody drains and returns the request body as a string.
func readBody(t *testing.T, r *http.Request) string {
	t.Helper()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// writeJSON writes a 200/206 JSON response with the given raw JSON payload.
func writeJSON(w http.ResponseWriter, status int, raw string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, raw)
}

// writePGError emulates a PostgREST 4xx error body (code+message) as the real API
// returns it. postgrest-go collapses this into an error string "(<code>) <msg>".
func writePGError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"code":    code,
		"message": message,
	})
}

func TestRegister_UpsertOnConflictOwnerHost_StatusPending(t *testing.T) {
	var got captured
	store := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		got = captured{
			method: r.Method,
			path:   r.URL.Path,
			query:  r.URL.RawQuery,
			prefer: r.Header.Get("Prefer"),
			body:   readBody(t, r),
		}
		// Upsert returns the representation of the upserted row.
		writeJSON(w, http.StatusCreated, `[{"id":"row-1","name":"bk-alice-h1","host":"h1","dokku_user":"dokku","public_key":"ssh-ed25519 AAAA alice","fingerprint":"SHA256:abc","status":"pending","created_at":"2026-01-01T00:00:00Z"}]`)
	})

	rec, err := store.Register(KeyRecord{
		Name:        "bk-alice-h1",
		Host:        "h1",
		DokkuUser:   "dokku",
		PublicKey:   "ssh-ed25519 AAAA alice",
		Fingerprint: "SHA256:abc",
		Owner:       "should-not-be-sent",
	})
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	if got.method != http.MethodPost {
		t.Errorf("method = %q, want POST", got.method)
	}
	if !strings.HasSuffix(got.path, "/rest/v1/ssh_keys") {
		t.Errorf("path = %q, want suffix /rest/v1/ssh_keys", got.path)
	}
	if !strings.Contains(got.query, "on_conflict=owner%2Chost") && !strings.Contains(got.query, "on_conflict=owner,host") {
		t.Errorf("query = %q, want on_conflict=owner,host", got.query)
	}
	if !strings.Contains(got.prefer, "resolution=merge-duplicates") {
		t.Errorf("Prefer = %q, want resolution=merge-duplicates (upsert)", got.prefer)
	}

	// Body must carry status=pending and the public key, and must NOT leak owner.
	var payload map[string]any
	if err := json.Unmarshal([]byte(got.body), &payload); err != nil {
		t.Fatalf("payload not JSON object: %v (body=%s)", err, got.body)
	}
	if payload["status"] != string(StatusPending) {
		t.Errorf("payload status = %v, want pending", payload["status"])
	}
	if payload["public_key"] != "ssh-ed25519 AAAA alice" {
		t.Errorf("payload public_key = %v", payload["public_key"])
	}
	if payload["host"] != "h1" {
		t.Errorf("payload host = %v, want h1", payload["host"])
	}
	if v, ok := payload["owner"]; ok && v != "" {
		t.Errorf("payload must not send owner (DB sets auth.uid()), got %v", v)
	}

	// Returned record reflects the server representation.
	if rec.ID != "row-1" || rec.Status != StatusPending {
		t.Errorf("returned rec = %+v, want id row-1 status pending", rec)
	}
}

func TestListMine_ReturnsCannedRows(t *testing.T) {
	var got captured
	store := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		got = captured{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery}
		writeJSON(w, http.StatusOK, `[
			{"id":"a","name":"bk-alice-h1","host":"h1","fingerprint":"SHA256:a","status":"pending","created_at":"t1"},
			{"id":"b","name":"bk-alice-h2","host":"h2","fingerprint":"SHA256:b","status":"installed","created_at":"t2"}
		]`)
	})

	recs, err := store.ListMine()
	if err != nil {
		t.Fatalf("ListMine error: %v", err)
	}
	if got.method != http.MethodGet {
		t.Errorf("method = %q, want GET", got.method)
	}
	if !strings.HasSuffix(got.path, "/rest/v1/ssh_keys") {
		t.Errorf("path = %q, want ssh_keys", got.path)
	}
	if len(recs) != 2 {
		t.Fatalf("len(recs) = %d, want 2", len(recs))
	}
	if recs[0].ID != "a" || recs[1].Status != StatusInstalled {
		t.Errorf("decoded recs = %+v", recs)
	}
}

func TestListMine_EmptyReturnsEmptySliceNotError(t *testing.T) {
	store := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, `[]`)
	})
	recs, err := store.ListMine()
	if err != nil {
		t.Fatalf("ListMine on empty must not error, got %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("len(recs) = %d, want 0", len(recs))
	}
}

func TestListPending_FiltersPending(t *testing.T) {
	var got captured
	store := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		got = captured{method: r.Method, query: r.URL.RawQuery}
		writeJSON(w, http.StatusOK, `[{"id":"p1","status":"pending","host":"h1","created_at":"t"}]`)
	})
	recs, err := store.ListPending()
	if err != nil {
		t.Fatalf("ListPending error: %v", err)
	}
	if !strings.Contains(got.query, "status=eq.pending") {
		t.Errorf("query = %q, want status=eq.pending filter", got.query)
	}
	if len(recs) != 1 || recs[0].Status != StatusPending {
		t.Errorf("recs = %+v", recs)
	}
}

func TestListPending_RLSDenied_MapsErrPermission(t *testing.T) {
	store := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		// PostgREST RLS / insufficient privilege => HTTP 403, PG code 42501.
		writePGError(w, http.StatusForbidden, "42501", "permission denied for table ssh_keys")
	})
	_, err := store.ListPending()
	if err == nil {
		t.Fatal("ListPending must error when RLS denies")
	}
	if !errors.Is(err, ErrPermission) {
		t.Errorf("err = %v, want errors.Is ErrPermission", err)
	}
}

func TestListPending_JWTUnauthorized_MapsErrPermission(t *testing.T) {
	store := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		// PostgREST JWT failure => HTTP 401, code PGRST301.
		writePGError(w, http.StatusUnauthorized, "PGRST301", "JWT expired")
	})
	_, err := store.ListPending()
	if !errors.Is(err, ErrPermission) {
		t.Errorf("err = %v, want errors.Is ErrPermission", err)
	}
}

func TestMarkInstalled_PatchesStatusAndAudit(t *testing.T) {
	var got captured
	store := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		got = captured{method: r.Method, query: r.URL.RawQuery, body: readBody(t, r)}
		writeJSON(w, http.StatusOK, `[{"id":"k1","status":"installed"}]`)
	})
	if err := store.MarkInstalled("k1", "admin-uid"); err != nil {
		t.Fatalf("MarkInstalled error: %v", err)
	}
	if got.method != http.MethodPatch {
		t.Errorf("method = %q, want PATCH", got.method)
	}
	if !strings.Contains(got.query, "id=eq.k1") {
		t.Errorf("query = %q, want id=eq.k1", got.query)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(got.body), &payload); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, got.body)
	}
	if payload["status"] != string(StatusInstalled) {
		t.Errorf("status = %v, want installed", payload["status"])
	}
	if payload["installed_by"] != "admin-uid" {
		t.Errorf("installed_by = %v, want admin-uid", payload["installed_by"])
	}
	if payload["installed_at"] == nil || payload["installed_at"] == "" {
		t.Errorf("installed_at must be set, got %v", payload["installed_at"])
	}
}

func TestMarkRevoked_PatchesStatusAndAudit(t *testing.T) {
	var got captured
	store := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		got = captured{method: r.Method, query: r.URL.RawQuery, body: readBody(t, r)}
		writeJSON(w, http.StatusOK, `[{"id":"k2","status":"revoked"}]`)
	})
	if err := store.MarkRevoked("k2", "admin-uid"); err != nil {
		t.Fatalf("MarkRevoked error: %v", err)
	}
	if got.method != http.MethodPatch {
		t.Errorf("method = %q, want PATCH", got.method)
	}
	if !strings.Contains(got.query, "id=eq.k2") {
		t.Errorf("query = %q, want id=eq.k2", got.query)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(got.body), &payload); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, got.body)
	}
	if payload["status"] != string(StatusRevoked) {
		t.Errorf("status = %v, want revoked", payload["status"])
	}
	if payload["revoked_by"] != "admin-uid" {
		t.Errorf("revoked_by = %v, want admin-uid", payload["revoked_by"])
	}
	if payload["revoked_at"] == nil || payload["revoked_at"] == "" {
		t.Errorf("revoked_at must be set, got %v", payload["revoked_at"])
	}
}

func TestMarkInstalled_RLSDenied_MapsErrPermission(t *testing.T) {
	store := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		writePGError(w, http.StatusForbidden, "42501", "permission denied")
	})
	err := store.MarkInstalled("k1", "not-admin")
	if !errors.Is(err, ErrPermission) {
		t.Errorf("err = %v, want ErrPermission", err)
	}
}

func TestFind_ByFingerprint(t *testing.T) {
	var got captured
	store := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		got = captured{query: r.URL.RawQuery}
		writeJSON(w, http.StatusOK, `[{"id":"f1","fingerprint":"SHA256:xyz","name":"bk-bob-h1","status":"installed"}]`)
	})
	rec, err := store.Find("SHA256:xyz")
	if err != nil {
		t.Fatalf("Find error: %v", err)
	}
	// Must locate by fingerprint OR name (an or= filter referencing both columns).
	if !strings.Contains(got.query, "fingerprint") || !strings.Contains(got.query, "name") {
		t.Errorf("query = %q, want filter on fingerprint and name", got.query)
	}
	if rec.ID != "f1" {
		t.Errorf("rec = %+v, want id f1", rec)
	}
}

func TestFind_Empty_MapsErrNotFound(t *testing.T) {
	store := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, `[]`)
	})
	_, err := store.Find("does-not-exist")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want errors.Is ErrNotFound", err)
	}
}

func TestFind_RLSDenied_MapsErrPermission(t *testing.T) {
	store := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		writePGError(w, http.StatusForbidden, "42501", "permission denied")
	})
	_, err := store.Find("SHA256:xyz")
	if !errors.Is(err, ErrPermission) {
		t.Errorf("err = %v, want ErrPermission", err)
	}
}
