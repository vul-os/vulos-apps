package appsplatform

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// MountConfig configures the mountable HTTP handler set a product embeds.
type MountConfig struct {
	// Adapter is the product seam (required). Its Product() determines which
	// apps this mount lists and serves.
	Adapter ProductAdapter

	// Registry stores apps (required). Use the StandaloneRegistry default or a
	// cloud control-plane implementation (see seam.go).
	Registry Registry

	// Dispatcher delivers outbound events / SSE for this product (required). Its
	// product must match Adapter.Product().
	Dispatcher *Dispatcher

	// Admin authenticates the management API caller using the product's OWN
	// session (required for the management routes; if nil they respond 401).
	Admin AdminAuthFunc

	// BasePath is the mount prefix (default "/api/apps"). The returned handler
	// uses absolute patterns under it; mount it at BasePath and BasePath+"/".
	BasePath string
}

// Handler is the mounted handler set plus the resolved base path.
type Handler struct {
	http.Handler
	BasePath string
}

// NewHandler wires the product-agnostic HTTP surface over the config and returns
// an http.Handler (a *http.ServeMux with absolute routes). It exposes:
//
//	Management (product session via cfg.Admin):
//	  GET    {base}                     list installed apps for this product (the
//	                                    consolidation contract Workspace reads)
//	  POST   {base}                     install an app (secrets shown once)
//	  GET    {base}/{id}                app summary
//	  PUT    {base}/{id}                update app
//	  DELETE {base}/{id}                uninstall app
//	  POST   {base}/{id}/rotate/token   rotate the app token (shown once)
//	  POST   {base}/{id}/rotate/secret  rotate the signing secret (shown once)
//	  GET    {base}/commands            slash-command catalog (composer autocomplete)
//
//	Runtime (Bearer app token via TokenAuth):
//	  GET    {base}/v1/auth.test        {app_id, name, scopes, products}
//	  POST   {base}/v1/act              perform a product action via the adapter
//	  GET    {base}/v1/read             read product content via the adapter
//	  GET    {base}/v1/events           SSE event stream (socket-mode style)
//
//	Incoming webhook (unauthenticated; the id is the secret):
//	  POST   {base}/hooks/{id}          post via the adapter as the app
func NewHandler(cfg MountConfig) (*Handler, error) {
	if cfg.Adapter == nil {
		return nil, errors.New("appsplatform: MountConfig.Adapter is required")
	}
	if cfg.Registry == nil {
		return nil, errors.New("appsplatform: MountConfig.Registry is required")
	}
	if cfg.Dispatcher == nil {
		return nil, errors.New("appsplatform: MountConfig.Dispatcher is required")
	}
	base := cfg.BasePath
	if base == "" {
		base = "/api/apps"
	}
	base = "/" + strings.Trim(base, "/")

	h := &handler{cfg: cfg, base: base, product: cfg.Adapter.Product()}
	mux := http.NewServeMux()

	// Management routes (product session auth).
	mux.HandleFunc("GET "+base, h.admin(h.list))
	mux.HandleFunc("POST "+base, h.admin(h.create))
	mux.HandleFunc("GET "+base+"/commands", h.admin(h.commands))
	mux.HandleFunc("GET "+base+"/{id}", h.admin(h.get))
	mux.HandleFunc("PUT "+base+"/{id}", h.admin(h.update))
	mux.HandleFunc("DELETE "+base+"/{id}", h.admin(h.delete))
	// Rotate routes are one level deeper than the incoming-webhook route
	// ("hooks/{id}") so the ServeMux never sees an ambiguous literal-vs-wildcard
	// overlap at the same depth.
	mux.HandleFunc("POST "+base+"/{id}/rotate/token", h.admin(h.rotateToken))
	mux.HandleFunc("POST "+base+"/{id}/rotate/secret", h.admin(h.rotateSecret))

	// Runtime routes (Bearer app token). Registered individually (not as a
	// subtree) so literal "/v1/" segments stay more specific than the "{id}"
	// management routes and the ServeMux does not see an ambiguous overlap.
	tok := func(fn http.HandlerFunc) http.Handler { return TokenAuth(cfg.Registry, fn) }
	mux.Handle("GET "+base+"/v1/auth.test", tok(h.authTest))
	mux.Handle("POST "+base+"/v1/act", tok(h.act))
	mux.Handle("GET "+base+"/v1/read", tok(h.read))
	mux.Handle("GET "+base+"/v1/events", tok(h.events))

	// Incoming webhook (unauthenticated).
	mux.HandleFunc("POST "+base+"/hooks/{id}", h.incoming)

	return &Handler{Handler: mux, BasePath: base}, nil
}

type handler struct {
	cfg     MountConfig
	base    string
	product string
}

// ---- management (session-authed) --------------------------------------------

// admin wraps a management handler with the product's session auth, passing the
// caller identity along via the request context is unnecessary — we resolve it
// once and hand it to the inner func.
func (h *handler) admin(fn func(w http.ResponseWriter, r *http.Request, owner string, isAdmin bool)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.cfg.Admin == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "management API unavailable"})
			return
		}
		owner, isAdmin, ok := h.cfg.Admin(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "not authenticated"})
			return
		}
		fn(w, r, owner, isAdmin)
	}
}

// ownedApp loads an app and enforces owner-scoping: a non-admin sees only their
// own apps, and a cross-owner access returns 404 (no existence leak). It also
// enforces product-targeting so one product can't manage another's apps.
func (h *handler) ownedApp(w http.ResponseWriter, id, owner string, isAdmin bool) (*App, bool) {
	a, err := h.cfg.Registry.Get(id)
	if err != nil || a == nil || !a.TargetsProduct(h.product) || (!isAdmin && a.OwnerID != owner) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "app not found"})
		return nil, false
	}
	return a, true
}

// list GET {base} — installed apps for THIS product. This is the consolidation
// contract: Workspace calls each product's GET /api/apps and merges the results.
func (h *handler) list(w http.ResponseWriter, r *http.Request, owner string, isAdmin bool) {
	apps, err := h.cfg.Registry.List(owner, isAdmin)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "registry unavailable"})
		return
	}
	out := make([]Summary, 0, len(apps))
	for _, a := range apps {
		if a.TargetsProduct(h.product) {
			out = append(out, a.ToSummary(h.base))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) create(w http.ResponseWriter, r *http.Request, owner string, isAdmin bool) {
	var req struct {
		Name            string         `json:"name"`
		Icon            string         `json:"icon"`
		Description     string         `json:"description"`
		Scopes          []string       `json:"scopes"`
		Products        []string       `json:"products"`
		Events          []string       `json:"events"`
		SlashCommands   []SlashCommand `json:"slash_commands"`
		WebhookURL      string         `json:"webhook_url"`
		DefaultTarget   string         `json:"default_target"`
		IncomingEnabled *bool          `json:"incoming_enabled"`
	}
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name required"})
		return
	}
	// Default: target this product (so the place's "install" just works), and
	// enable the incoming webhook unless explicitly disabled.
	if len(req.Products) == 0 {
		req.Products = []string{h.product}
	}
	incoming := true
	if req.IncomingEnabled != nil {
		incoming = *req.IncomingEnabled
	}
	created, err := h.cfg.Registry.Create(CreateParams{
		Name:            req.Name,
		Icon:            req.Icon,
		Description:     req.Description,
		OwnerID:         owner,
		Scopes:          req.Scopes,
		Products:        req.Products,
		Events:          req.Events,
		SlashCommands:   req.SlashCommands,
		WebhookURL:      req.WebhookURL,
		DefaultTarget:   req.DefaultTarget,
		IncomingEnabled: incoming,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"app":                  created.App.ToSummary(h.base),
		"token":                created.Token,
		"signing_secret":       created.SigningSecret,
		"incoming_webhook_url": IncomingWebhookPath(h.base, created.App.Incoming.ID),
	})
}

func (h *handler) get(w http.ResponseWriter, r *http.Request, owner string, isAdmin bool) {
	a, ok := h.ownedApp(w, r.PathValue("id"), owner, isAdmin)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, a.ToSummary(h.base))
}

func (h *handler) update(w http.ResponseWriter, r *http.Request, owner string, isAdmin bool) {
	a, ok := h.ownedApp(w, r.PathValue("id"), owner, isAdmin)
	if !ok {
		return
	}
	var req struct {
		Name            *string         `json:"name"`
		Icon            *string         `json:"icon"`
		Description     *string         `json:"description"`
		Scopes          *[]string       `json:"scopes"`
		Products        *[]string       `json:"products"`
		Events          *[]string       `json:"events"`
		SlashCommands   *[]SlashCommand `json:"slash_commands"`
		WebhookURL      *string         `json:"webhook_url"`
		DefaultTarget   *string         `json:"default_target"`
		IncomingEnabled *bool           `json:"incoming_enabled"`
	}
	if !decode(w, r, &req) {
		return
	}
	updated, err := h.cfg.Registry.Update(a.ID, UpdateParams{
		Name:            req.Name,
		Icon:            req.Icon,
		Description:     req.Description,
		Scopes:          req.Scopes,
		Products:        req.Products,
		Events:          req.Events,
		SlashCommands:   req.SlashCommands,
		WebhookURL:      req.WebhookURL,
		DefaultTarget:   req.DefaultTarget,
		IncomingEnabled: req.IncomingEnabled,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, updated.ToSummary(h.base))
}

func (h *handler) delete(w http.ResponseWriter, r *http.Request, owner string, isAdmin bool) {
	a, ok := h.ownedApp(w, r.PathValue("id"), owner, isAdmin)
	if !ok {
		return
	}
	if err := h.cfg.Registry.Delete(a.ID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *handler) rotateToken(w http.ResponseWriter, r *http.Request, owner string, isAdmin bool) {
	a, ok := h.ownedApp(w, r.PathValue("id"), owner, isAdmin)
	if !ok {
		return
	}
	token, err := h.cfg.Registry.RotateToken(a.ID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token})
}

func (h *handler) rotateSecret(w http.ResponseWriter, r *http.Request, owner string, isAdmin bool) {
	a, ok := h.ownedApp(w, r.PathValue("id"), owner, isAdmin)
	if !ok {
		return
	}
	secret, err := h.cfg.Registry.RotateSecret(a.ID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"signing_secret": secret})
}

func (h *handler) commands(w http.ResponseWriter, r *http.Request, owner string, isAdmin bool) {
	writeJSON(w, http.StatusOK, h.cfg.Registry.AllSlashCommands(h.product))
}

// ---- runtime (token-authed) -------------------------------------------------

// runtimeApp returns the authenticated app and enforces that it targets this
// product. Writes 401/403 and returns false on denial.
func (h *handler) runtimeApp(w http.ResponseWriter, r *http.Request) (*App, bool) {
	a, ok := AppFromContext(r.Context())
	if !ok || a == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "app not authenticated"})
		return nil, false
	}
	if !a.TargetsProduct(h.product) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "app does not target this product"})
		return nil, false
	}
	return a, true
}

func (h *handler) authTest(w http.ResponseWriter, r *http.Request) {
	a, ok := h.runtimeApp(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"app_id": a.ID, "name": a.Name, "scopes": nonNilStr(a.Scopes), "products": nonNilStr(a.Products),
	})
}

func (h *handler) act(w http.ResponseWriter, r *http.Request) {
	a, ok := h.runtimeApp(w, r)
	if !ok {
		return
	}
	var req ActionRequest
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Action) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "action required"})
		return
	}
	if !h.checkScopeAndTarget(w, a, req.Action, req.Target) {
		return
	}
	result, err := h.cfg.Adapter.Act(r.Context(), a, req, h.cfg.Dispatcher.EmitFunc())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": result})
}

func (h *handler) read(w http.ResponseWriter, r *http.Request) {
	a, ok := h.runtimeApp(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	req := ReadRequest{Kind: q.Get("kind"), Target: q.Get("target"), Params: map[string]string{}}
	for k, v := range q {
		if k == "kind" || k == "target" {
			continue
		}
		if len(v) > 0 {
			req.Params[k] = v[0]
		}
	}
	if strings.TrimSpace(req.Kind) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "kind required"})
		return
	}
	if !h.checkScopeAndTarget(w, a, req.Kind, req.Target) {
		return
	}
	result, err := h.cfg.Adapter.Read(r.Context(), a, req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// checkScopeAndTarget enforces the scope the adapter requires for an
// action/kind, then the app's access to the target. Writes the error and returns
// false on denial.
func (h *handler) checkScopeAndTarget(w http.ResponseWriter, a *App, actionOrKind, target string) bool {
	if scope := h.cfg.Adapter.RequiredScope(actionOrKind); scope != "" && !a.HasScope(scope) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "missing required scope: " + scope})
		return false
	}
	if strings.TrimSpace(target) != "" {
		allowed, exists := h.cfg.Adapter.CanAccessTarget(a, target)
		if !exists {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "target not found"})
			return false
		}
		if !allowed {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "app cannot access this target"})
			return false
		}
	}
	return true
}

func (h *handler) events(w http.ResponseWriter, r *http.Request) {
	a, ok := h.runtimeApp(w, r)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "streaming unsupported"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, unsub := h.cfg.Dispatcher.Subscribe(a.ID)
	defer unsub()
	for {
		select {
		case <-r.Context().Done():
			return
		case body, open := <-ch:
			if !open {
				return
			}
			w.Write([]byte("data: "))
			w.Write(body)
			w.Write([]byte("\n\n"))
			flusher.Flush()
		}
	}
}

// ---- incoming webhook (unauthenticated) -------------------------------------

func (h *handler) incoming(w http.ResponseWriter, r *http.Request) {
	a, err := h.cfg.Registry.GetByIncomingWebhookID(r.PathValue("id"))
	if err != nil || a == nil || !a.TargetsProduct(h.product) || !a.Incoming.Enabled {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown webhook"})
		return
	}
	// The body is the product-specific action payload; target falls back to the
	// app's default target. The adapter decides how to interpret it.
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var probe struct {
		Target string `json:"target"`
	}
	_ = json.Unmarshal(body, &probe)
	target := strings.TrimSpace(probe.Target)
	if target == "" {
		target = a.DefaultTarget
	}
	result, err := h.cfg.Adapter.Act(r.Context(), a, ActionRequest{
		Action:  "incoming_webhook",
		Target:  target,
		Payload: json.RawMessage(body),
	}, h.cfg.Dispatcher.EmitFunc())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "result": result})
}

// ---- helpers ----------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(v); err != nil && err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json: " + err.Error()})
		return false
	}
	return true
}
