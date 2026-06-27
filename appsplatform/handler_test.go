package appsplatform

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeAdapter is a minimal Talk-like product adapter for tests.
type fakeAdapter struct{ acted int }

func (f *fakeAdapter) Product() string { return ProductTalk }

func (f *fakeAdapter) RequiredScope(actionOrKind string) string {
	switch actionOrKind {
	case "message.post":
		return ScopeChatWrite
	case "history":
		return ScopeHistoryRead
	default:
		return ""
	}
}

func (f *fakeAdapter) CanAccessTarget(app *App, target string) (bool, bool) {
	switch target {
	case "missing":
		return false, false // 404
	case "secret":
		return false, true // 403
	default:
		return true, true
	}
}

func (f *fakeAdapter) Act(_ context.Context, app *App, req ActionRequest, emit EmitFunc) (any, error) {
	f.acted++
	if emit != nil {
		emit(EventMessageCreated, map[string]any{"target": req.Target}, nil)
	}
	return map[string]any{"posted": true, "target": req.Target}, nil
}

func (f *fakeAdapter) Read(_ context.Context, app *App, req ReadRequest) (any, error) {
	return map[string]any{"kind": req.Kind, "params": req.Params}, nil
}

// header-based admin auth: "X-User: <id>" and "X-Admin: 1".
func headerAdmin(r *http.Request) (string, bool, bool) {
	u := r.Header.Get("X-User")
	if u == "" {
		return "", false, false
	}
	return u, r.Header.Get("X-Admin") == "1", true
}

func newTestHandler(t *testing.T, product string) (*Handler, Registry, *fakeAdapter) {
	t.Helper()
	reg := NewMemoryRegistry()
	ad := &fakeAdapter{}
	var p ProductAdapter = ad
	if product == ProductMail {
		p = mailAdapter{}
	}
	h, err := NewHandler(MountConfig{
		Adapter:    p,
		Registry:   reg,
		Dispatcher: NewDispatcher(reg, p.Product()),
		Admin:      headerAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	return h, reg, ad
}

type mailAdapter struct{}

func (mailAdapter) Product() string                           { return ProductMail }
func (mailAdapter) RequiredScope(string) string               { return "" }
func (mailAdapter) CanAccessTarget(*App, string) (bool, bool) { return true, true }
func (mailAdapter) Act(context.Context, *App, ActionRequest, EmitFunc) (any, error) {
	return nil, nil
}
func (mailAdapter) Read(context.Context, *App, ReadRequest) (any, error) { return nil, nil }

func do(h http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestAdminCreateListGetDelete(t *testing.T) {
	h, _, _ := newTestHandler(t, ProductTalk)
	alice := map[string]string{"X-User": "alice"}

	// Create.
	w := do(h, "POST", "/api/apps", `{"name":"Echo","scopes":["chat:write"],"description":"echoes"}`, alice)
	if w.Code != http.StatusCreated {
		t.Fatalf("create code %d: %s", w.Code, w.Body)
	}
	var created struct {
		App           Summary `json:"app"`
		Token         string  `json:"token"`
		SigningSecret string  `json:"signing_secret"`
	}
	json.Unmarshal(w.Body.Bytes(), &created)
	if created.Token == "" || created.SigningSecret == "" {
		t.Fatal("secrets not returned at create")
	}
	if !contains(created.App.Products, ProductTalk) {
		t.Fatalf("create should default-target the hosting product: %v", created.App.Products)
	}
	// Secrets must never appear in the summary JSON.
	if strings.Contains(string(mustJSON(created.App)), "vat_") || strings.Contains(string(mustJSON(created.App)), "vas_") {
		t.Fatal("summary leaked a secret")
	}
	id := created.App.ID

	// List (alice sees her own).
	w = do(h, "GET", "/api/apps", "", alice)
	var list []Summary
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 1 {
		t.Fatalf("expected 1 app, got %d", len(list))
	}

	// Cross-owner GET is a 404 (no existence leak).
	w = do(h, "GET", "/api/apps/"+id, "", map[string]string{"X-User": "bob"})
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-owner get should 404, got %d", w.Code)
	}

	// Delete.
	w = do(h, "DELETE", "/api/apps/"+id, "", alice)
	if w.Code != http.StatusOK {
		t.Fatalf("delete code %d", w.Code)
	}
}

func TestListFiltersByProduct(t *testing.T) {
	h, reg, _ := newTestHandler(t, ProductTalk)
	// A mail-only app must not appear in Talk's GET /api/apps.
	reg.Create(CreateParams{Name: "mailer", OwnerID: "alice", Products: []string{ProductMail}})
	reg.Create(CreateParams{Name: "talker", OwnerID: "alice", Products: []string{ProductTalk}})
	w := do(h, "GET", "/api/apps", "", map[string]string{"X-User": "alice", "X-Admin": "1"})
	var list []Summary
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 1 || list[0].Name != "talker" {
		t.Fatalf("product filter failed: %+v", list)
	}
}

func TestAdminUnauthenticated(t *testing.T) {
	h, _, _ := newTestHandler(t, ProductTalk)
	if w := do(h, "GET", "/api/apps", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without session, got %d", w.Code)
	}
}

func TestRuntimeAuthTestAndTokenReject(t *testing.T) {
	h, reg, _ := newTestHandler(t, ProductTalk)
	c, _ := reg.Create(CreateParams{Name: "x", OwnerID: "a", Products: []string{ProductTalk}, Scopes: []string{ScopeChatWrite}})

	w := do(h, "GET", "/api/apps/v1/auth.test", "", map[string]string{"Authorization": "Bearer " + c.Token})
	if w.Code != http.StatusOK {
		t.Fatalf("auth.test code %d: %s", w.Code, w.Body)
	}
	// Bad token → 401.
	if w := do(h, "GET", "/api/apps/v1/auth.test", "", map[string]string{"Authorization": "Bearer vat_nope"}); w.Code != http.StatusUnauthorized {
		t.Fatalf("bad token should 401, got %d", w.Code)
	}
	// Missing token → 401.
	if w := do(h, "GET", "/api/apps/v1/auth.test", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("missing token should 401, got %d", w.Code)
	}
}

func TestRuntimeScopeAndTargetEnforcement(t *testing.T) {
	h, reg, ad := newTestHandler(t, ProductTalk)
	// App without chat:write.
	noScope, _ := reg.Create(CreateParams{Name: "n", OwnerID: "a", Products: []string{ProductTalk}})
	bearer := func(tok string) map[string]string {
		return map[string]string{"Authorization": "Bearer " + tok, "Content-Type": "application/json"}
	}
	w := do(h, "POST", "/api/apps/v1/act", `{"action":"message.post","target":"general"}`, bearer(noScope.Token))
	if w.Code != http.StatusForbidden {
		t.Fatalf("missing scope should 403, got %d: %s", w.Code, w.Body)
	}

	// App with chat:write.
	ok, _ := reg.Create(CreateParams{Name: "y", OwnerID: "a", Products: []string{ProductTalk}, Scopes: []string{ScopeChatWrite}})
	w = do(h, "POST", "/api/apps/v1/act", `{"action":"message.post","target":"general"}`, bearer(ok.Token))
	if w.Code != http.StatusOK || ad.acted != 1 {
		t.Fatalf("act should succeed, got %d acted=%d: %s", w.Code, ad.acted, w.Body)
	}
	// Target visibility: secret → 403, missing → 404.
	if w := do(h, "POST", "/api/apps/v1/act", `{"action":"message.post","target":"secret"}`, bearer(ok.Token)); w.Code != http.StatusForbidden {
		t.Fatalf("inaccessible target should 403, got %d", w.Code)
	}
	if w := do(h, "POST", "/api/apps/v1/act", `{"action":"message.post","target":"missing"}`, bearer(ok.Token)); w.Code != http.StatusNotFound {
		t.Fatalf("unknown target should 404, got %d", w.Code)
	}
}

func TestRuntimeProductTargetingRejected(t *testing.T) {
	// Mount is Talk; an app that targets only Mail must be 403 at runtime.
	h, reg, _ := newTestHandler(t, ProductTalk)
	c, _ := reg.Create(CreateParams{Name: "m", OwnerID: "a", Products: []string{ProductMail}})
	w := do(h, "GET", "/api/apps/v1/auth.test", "", map[string]string{"Authorization": "Bearer " + c.Token})
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-product token should 403, got %d: %s", w.Code, w.Body)
	}
}

func TestIncomingWebhook(t *testing.T) {
	h, reg, ad := newTestHandler(t, ProductTalk)
	c, _ := reg.Create(CreateParams{Name: "hook", OwnerID: "a", Products: []string{ProductTalk}, DefaultTarget: "general", IncomingEnabled: true})
	w := do(h, "POST", "/api/apps/hooks/"+c.App.Incoming.ID, `{"text":"ci done"}`, map[string]string{"Content-Type": "application/json"})
	if w.Code != http.StatusCreated || ad.acted != 1 {
		t.Fatalf("incoming webhook failed: code %d acted %d: %s", w.Code, ad.acted, w.Body)
	}
	// Unknown webhook id → 404.
	if w := do(h, "POST", "/api/apps/hooks/deadbeef", `{"text":"x"}`, nil); w.Code != http.StatusNotFound {
		t.Fatalf("unknown webhook should 404, got %d", w.Code)
	}
	// Disabled webhook → 404.
	reg.Update(c.App.ID, UpdateParams{IncomingEnabled: ptr(false)})
	if w := do(h, "POST", "/api/apps/hooks/"+c.App.Incoming.ID, `{"text":"x"}`, nil); w.Code != http.StatusNotFound {
		t.Fatalf("disabled webhook should 404, got %d", w.Code)
	}
}

func TestRotateTokenEndpoint(t *testing.T) {
	h, reg, _ := newTestHandler(t, ProductTalk)
	c, _ := reg.Create(CreateParams{Name: "r", OwnerID: "alice", Products: []string{ProductTalk}})
	w := do(h, "POST", "/api/apps/"+c.App.ID+"/rotate/token", "", map[string]string{"X-User": "alice"})
	if w.Code != http.StatusOK {
		t.Fatalf("rotate-token code %d", w.Code)
	}
	var resp struct {
		Token string `json:"token"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Token == "" || resp.Token == c.Token {
		t.Fatal("rotate-token should return a new token")
	}
	// Old token now rejected at runtime.
	if w := do(h, "GET", "/api/apps/v1/auth.test", "", map[string]string{"Authorization": "Bearer " + c.Token}); w.Code != http.StatusUnauthorized {
		t.Fatalf("old token should be invalid, got %d", w.Code)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
