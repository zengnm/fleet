package fleetd

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	fleetsvc "fleetd/internal/fleet"
	"fleetd/internal/store"
	"fleetd/pkg/spec"
)

//go:embed templates/*.html
var fleetTemplates embed.FS

type Server struct {
	cfg         Config
	store       store.Store
	backend     *Backend
	fleet       *fleetsvc.Service
	auth        *Authenticator
	runtimeAuth *RuntimeAuthenticator
	templates   *template.Template
}

type pageData struct {
	Title      string
	Page       string
	ReturnTo   string
	LoginError string
	Principal  string
	Error      string
	Claims     []spec.FleetPendingClaim
	Nodes      []spec.FleetOwnedNode
	Node       *spec.FleetOwnedNode
}

func NewServer(ctx context.Context, cfg Config) (*Server, error) {
	backing, err := store.NewSQLite(ctx, cfg.StoreDSN)
	if err != nil {
		return nil, err
	}
	authenticator, err := NewAuthenticator(cfg)
	if err != nil {
		return nil, err
	}
	runtimeAuthenticator, err := NewRuntimeAuthenticator(cfg)
	if err != nil {
		return nil, err
	}
	backend := NewBackend(cfg, backing)
	service := fleetsvc.NewService(backing, backend)
	templates, err := template.New("fleetd").ParseFS(fleetTemplates, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Server{
		cfg:         cfg,
		store:       backing,
		backend:     backend,
		fleet:       service,
		auth:        authenticator,
		runtimeAuth: runtimeAuthenticator,
		templates:   templates,
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /", s.handleRoot)
	mux.HandleFunc("GET /fleet", s.withUserPage(s.handleFleetRoot))
	mux.HandleFunc("GET /fleet/login", s.handleLoginPage)
	mux.HandleFunc("POST /fleet/login", s.handleLogin)
	mux.HandleFunc("POST /fleet/logout", s.handleLogout)
	mux.HandleFunc("GET /fleet/claims", s.withUserPage(s.handleClaims))
	mux.HandleFunc("POST /fleet/claims/", s.withUserPage(s.handleClaimRoutes))
	mux.HandleFunc("GET /fleet/nodes", s.withUserPage(s.handleNodes))
	mux.HandleFunc("GET /fleet/nodes/", s.withUserPage(s.handleNode))
	mux.HandleFunc("GET /runtime/fleet/nodes", s.withRuntimeAuth(s.handleRuntimeNodes))
	mux.HandleFunc("GET /runtime/fleet/nodes/", s.withRuntimeAuth(s.handleRuntimeNodeRoutes))
	mux.HandleFunc("POST /runtime/fleet/nodes/", s.withRuntimeAuth(s.handleRuntimeNodeRoutes))
	return mux
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") {
		s.backend.ServeWS(w, r)
		return
	}
	http.Redirect(w, r, "/fleet", http.StatusSeeOther)
}

func (s *Server) Run(ctx context.Context) error {
	server := &http.Server{
		Addr:              s.cfg.ListenAddr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	return server.ListenAndServe()
}

func (s *Server) withUserPage(next func(http.ResponseWriter, *http.Request, *Principal)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, err := s.auth.Authenticate(r)
		if err != nil {
			http.Redirect(w, r, "/fleet/login?return_to="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
			return
		}
		next(w, r, principal)
	}
}

func (s *Server) withRuntimeAuth(next func(http.ResponseWriter, *http.Request, *Principal)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, err := s.runtimeAuth.Authenticate(r)
		if err != nil {
			s.writeError(w, http.StatusUnauthorized, "unauthorized", err)
			return
		}
		next(w, r, principal)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) handleFleetRoot(w http.ResponseWriter, r *http.Request, _ *Principal) {
	http.Redirect(w, r, "/fleet/claims", http.StatusSeeOther)
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	returnTo := strings.TrimSpace(r.URL.Query().Get("return_to"))
	if returnTo == "" {
		returnTo = "/fleet/claims"
	}
	_ = s.templates.ExecuteTemplate(w, "login.html", pageData{
		Title:      "Fleet 登录",
		Page:       "login",
		ReturnTo:   returnTo,
		LoginError: r.URL.Query().Get("error"),
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/fleet/login?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	returnTo := strings.TrimSpace(r.FormValue("return_to"))
	if returnTo == "" {
		returnTo = "/fleet/claims"
	}
	http.SetCookie(w, &http.Cookie{
		Name:     fleetCookieName,
		Value:    strings.TrimSpace(r.FormValue("token")),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, returnTo, http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     fleetCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/fleet/login", http.StatusSeeOther)
}

func (s *Server) handleClaims(w http.ResponseWriter, r *http.Request, principal *Principal) {
	claims, err := s.fleet.ListClaims(r.Context())
	if err != nil {
		s.renderPage(w, pageData{
			Title:     "设备认领",
			Page:      "claims",
			Principal: principal.Subject,
			Error:     err.Error(),
		})
		return
	}
	s.renderPage(w, pageData{
		Title:     "设备认领",
		Page:      "claims",
		Principal: principal.Subject,
		Claims:    claims,
		Error:     r.URL.Query().Get("error"),
	})
}

func (s *Server) handleClaimRoutes(w http.ResponseWriter, r *http.Request, principal *Principal) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/fleet/claims/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	pairingID, action := parts[0], parts[1]
	switch action {
	case "approve":
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/fleet/claims?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
			return
		}
		_, _, err := s.fleet.ApproveClaim(r.Context(), principal.Subject, pairingID, strings.TrimSpace(r.FormValue("device_id_suffix")))
		if err != nil {
			http.Redirect(w, r, "/fleet/claims?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/fleet/nodes", http.StatusSeeOther)
	case "reject":
		if err := s.fleet.RejectClaim(r.Context(), pairingID); err != nil {
			http.Redirect(w, r, "/fleet/claims?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/fleet/claims", http.StatusSeeOther)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request, principal *Principal) {
	nodes, err := s.fleet.ListNodes(r.Context(), principal.Subject)
	if err != nil {
		s.renderPage(w, pageData{
			Title:     "我的节点",
			Page:      "nodes",
			Principal: principal.Subject,
			Error:     err.Error(),
		})
		return
	}
	s.renderPage(w, pageData{
		Title:     "我的节点",
		Page:      "nodes",
		Principal: principal.Subject,
		Nodes:     nodes,
		Error:     r.URL.Query().Get("error"),
	})
}

func (s *Server) handleNode(w http.ResponseWriter, r *http.Request, principal *Principal) {
	nodeID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/fleet/nodes/"), "/")
	node, err := s.fleet.GetNode(r.Context(), principal.Subject, nodeID)
	if err != nil {
		http.Redirect(w, r, "/fleet/nodes?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	s.renderPage(w, pageData{
		Title:     "节点详情",
		Page:      "node",
		Principal: principal.Subject,
		Node:      &node,
	})
}

func (s *Server) handleRuntimeNodes(w http.ResponseWriter, r *http.Request, principal *Principal) {
	nodes, err := s.fleet.ListNodes(r.Context(), principal.Subject)
	if err != nil {
		s.writeFleetError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "nodes": nodes})
}

func (s *Server) handleRuntimeNodeRoutes(w http.ResponseWriter, r *http.Request, principal *Principal) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/runtime/fleet/nodes/"), "/")
	if path == "" {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(path, "/")
	nodeID := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	switch {
	case r.Method == http.MethodGet && action == "":
		node, err := s.fleet.GetNode(r.Context(), principal.Subject, nodeID)
		if err != nil {
			s.writeFleetError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "node": node})
	case r.Method == http.MethodPost && action == "invoke":
		var request spec.FleetInvokeRequest
		if err := decodeJSONBody(r, &request); err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_json", err)
			return
		}
		result, err := s.fleet.InvokeNode(r.Context(), principal.Subject, nodeID, request.Command, request.Params)
		if err != nil {
			s.writeFleetError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "result": result})
	case r.Method == http.MethodPost && action == "run":
		var request spec.FleetRunRequest
		if err := decodeJSONBody(r, &request); err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_json", err)
			return
		}
		result, err := s.fleet.RunNode(r.Context(), principal.Subject, nodeID, request)
		if err != nil {
			s.writeFleetError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "result": result})
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) writeFleetError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, fleetsvc.ErrClaimNotFound):
		s.writeError(w, http.StatusNotFound, "claim_not_found", err)
	case errors.Is(err, fleetsvc.ErrNodeNotFound):
		s.writeError(w, http.StatusNotFound, "node_not_found", err)
	case errors.Is(err, fleetsvc.ErrNodeOffline):
		s.writeError(w, http.StatusConflict, "node_offline", err)
	case errors.Is(err, fleetsvc.ErrForbidden):
		s.writeError(w, http.StatusForbidden, "forbidden", err)
	case errors.Is(err, fleetsvc.ErrClaimConfirmation):
		s.writeError(w, http.StatusBadRequest, "claim_confirmation_mismatch", err)
	case errors.Is(err, fleetsvc.ErrApprovalRequired):
		s.writeError(w, http.StatusConflict, "approval_required", err)
	case errors.Is(err, fleetsvc.ErrBackendUnavailable):
		s.writeError(w, http.StatusServiceUnavailable, "backend_unavailable", err)
	case errors.Is(err, store.ErrNotFound):
		s.writeError(w, http.StatusNotFound, "not_found", err)
	default:
		s.writeError(w, http.StatusInternalServerError, "fleet_error", err)
	}
}

func (s *Server) writeError(w http.ResponseWriter, status int, code string, err error) {
	writeJSON(w, status, spec.Envelope{
		Status: "error",
		Error: &spec.APIError{
			Code:    code,
			Message: err.Error(),
		},
	})
}

func (s *Server) renderPage(w http.ResponseWriter, data pageData) {
	if strings.TrimSpace(data.Principal) == "" {
		data.Principal = "anonymous"
	}
	if data.Page == "login" {
		_ = s.templates.ExecuteTemplate(w, "login.html", data)
		return
	}
	_ = s.templates.ExecuteTemplate(w, "fleet.html", data)
}

func decodeJSONBody(r *http.Request, target any) error {
	defer r.Body.Close()
	if r.Body == nil {
		return nil
	}
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(target); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		http.Error(w, fmt.Sprintf("encode json: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
