package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"goyard/internal/config"
	"goyard/internal/metrics"
	"goyard/internal/presence"
	"goyard/internal/store"
	"goyard/internal/supervisor"
)

var supportedQuicktypes = map[string]struct{}{
	"png":  {},
	"gif":  {},
	"webp": {},
	"jpg":  {},
	"jpeg": {},
}

type API struct {
	cfg        *config.Config
	manager    *presence.Manager
	store      store.Store
	httpClient *http.Client
}

func NewAPI(cfg *config.Config, manager *presence.Manager, store store.Store) *API {
	return &API{
		cfg:     cfg,
		manager: manager,
		store:   store,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (a *API) SetHTTPClient(client *http.Client) {
	a.httpClient = client
}

// ErrorResponse represents the JSON shape {"success": false, "error": {"code": ..., "message": ...}}
type ErrorResponse struct {
	Success bool       `json:"success"`
	Error   ErrorField `json:"error"`
}

type ErrorField struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type DataResponse struct {
	Success bool `json:"success"`
	Data    any  `json:"data"`
}

func writeJSON(w http.ResponseWriter, status int, val any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(val)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, ErrorResponse{
		Success: false,
		Error: ErrorField{
			Code:    code,
			Message: message,
		},
	})
}

// Middleware: CORS, Recover, Metrics tracking
func (a *API) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CORS headers on all requests
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		w.Header().Set("Access-Control-Max-Age", "600")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// Response status recorder for metrics
		rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		defer func() {
			if rec := recover(); rec != nil {
				supervisor.RecoverAndLog("http-handler")
				metrics.DefaultMetrics.RecordHTTPResponse(http.StatusInternalServerError)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			} else {
				metrics.DefaultMetrics.RecordHTTPResponse(rw.status)
			}
		}()

		next.ServeHTTP(rw, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (a *API) RegisterRoutes(mux *http.ServeMux) {
	// Root info
	mux.HandleFunc("GET /{$}", a.handleRoot)

	// Health
	mux.HandleFunc("GET /health", a.handleHealth)

	// Metrics
	mux.HandleFunc("GET /metrics", metrics.DefaultMetrics.PrometheusHandler())

	// Discord invite redirect
	if a.cfg.DiscordInvite != "" {
		mux.HandleFunc("GET /discord", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, a.cfg.DiscordInvite, http.StatusFound)
		})
	}

	// v1 users routes
	mux.HandleFunc("GET /v1/users/@me", a.handleGetUserMe)
	mux.HandleFunc("GET /v1/users/{id}", a.handleGetUser)
	mux.HandleFunc("PUT /v1/users/{id}/kv/{field}", a.handlePutKV)
	mux.HandleFunc("PATCH /v1/users/{id}/kv", a.handlePatchKV)
	mux.HandleFunc("DELETE /v1/users/{id}/kv/{field}", a.handleDeleteKV)

	// 404 for other /v1/...
	mux.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "Route does not exist")
	})

	// Root wildcard quicklink handler /{file}
	mux.HandleFunc("GET /{file}", a.handleQuicklink)

	// Catch-all for not found
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "Route does not exist")
	})
}

func (a *API) handleRoot(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"info":                 "Lanyard provides Discord presences as an API and WebSocket. Find out more here: https://github.com/Phineas/lanyard",
		"monitored_user_count": a.manager.Count(),
		"discord_invite":       a.cfg.DiscordInvite,
	}
	writeJSON(w, http.StatusOK, DataResponse{Success: true, Data: resp})
}

func (a *API) handleHealth(w http.ResponseWriter, r *http.Request) {
	gwConnected := metrics.DefaultMetrics.GatewayConnected.Load() == 1
	status := "ok"
	if !gwConnected {
		// degraded status when gateway not connected
		status = "degraded"
	}
	resp := map[string]any{
		"ok":                true,
		"status":            status,
		"gateway_connected": gwConnected,
		"monitored_users":   a.manager.Count(),
		"sockets":           metrics.DefaultMetrics.ConnectedSessions.Load(),
		"uptime_seconds":    int64(time.Since(metrics.DefaultMetrics.StartTime).Seconds()),
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) getAuthUserID(r *http.Request) (string, error) {
	key := r.Header.Get("Authorization")
	if key == "" {
		return "", errors.New("missing key")
	}
	uid, ok, err := a.store.UserIDForKey(r.Context(), key)
	if err != nil || !ok {
		return "", errors.New("invalid key")
	}
	return uid, nil
}

func (a *API) handleGetUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	pretty, ok := a.manager.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "user_not_monitored", "User is not being monitored by Lanyard")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `{"success":true,"data":%s}`, string(pretty))
}

func (a *API) handleGetUserMe(w http.ResponseWriter, r *http.Request) {
	uid, err := a.getAuthUserID(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "no_permission", "You do not have permission to access this resource")
		return
	}
	pretty, ok := a.manager.Get(uid)
	if !ok {
		writeError(w, http.StatusNotFound, "user_not_monitored", "User is not being monitored by Lanyard")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `{"success":true,"data":%s}`, string(pretty))
}

func (a *API) authorizeWrite(w http.ResponseWriter, r *http.Request, targetID string) (string, bool) {
	authUID, err := a.getAuthUserID(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "no_permission", "You do not have permission to access this resource")
		return "", false
	}
	if targetID != "@me" && targetID != authUID {
		writeError(w, http.StatusUnauthorized, "no_permission", "You do not have permission to access this resource")
		return "", false
	}
	// Check if user is monitored
	if _, ok := a.manager.Get(authUID); !ok {
		writeError(w, http.StatusNotFound, "user_not_monitored", "User is not being monitored by Lanyard")
		return "", false
	}
	return authUID, true
}

func (a *API) handlePutKV(w http.ResponseWriter, r *http.Request) {
	targetID := r.PathValue("id")
	field := r.PathValue("field")

	authUID, ok := a.authorizeWrite(w, r, targetID)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "Payload too large")
			return
		}
		writeError(w, http.StatusBadRequest, "bad_request", "Failed to read body")
		return
	}

	val := string(body)
	if err := a.store.SetKV(r.Context(), authUID, field, val); err != nil {
		metrics.DefaultMetrics.KVValidationFailures.Add(1)
		writeError(w, http.StatusNotFound, "kv_validation_failed", err.Error())
		return
	}

	newKV, _ := a.store.GetKV(r.Context(), authUID)
	a.manager.Sync(authUID, presence.Patch{KV: newKV}, false)

	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handlePatchKV(w http.ResponseWriter, r *http.Request) {
	targetID := r.PathValue("id")

	authUID, ok := a.authorizeWrite(w, r, targetID)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "Payload too large")
			return
		}
		writeError(w, http.StatusBadRequest, "bad_request", "Failed to read body")
		return
	}

	var rawMap map[string]any
	if err := json.Unmarshal(body, &rawMap); err != nil {
		writeError(w, http.StatusNotFound, "invalid_kv_value", "body must be an object")
		return
	}

	pairs := make(map[string]string, len(rawMap))
	for k, v := range rawMap {
		str, isStr := v.(string)
		if !isStr {
			writeError(w, http.StatusNotFound, "invalid_kv_value", "body must be an object")
			return
		}
		pairs[k] = str
	}

	if err := a.store.MultiSetKV(r.Context(), authUID, pairs); err != nil {
		metrics.DefaultMetrics.KVValidationFailures.Add(1)
		writeError(w, http.StatusNotFound, "kv_validation_failed", err.Error())
		return
	}

	newKV, _ := a.store.GetKV(r.Context(), authUID)
	a.manager.Sync(authUID, presence.Patch{KV: newKV}, false)

	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleDeleteKV(w http.ResponseWriter, r *http.Request) {
	targetID := r.PathValue("id")
	field := r.PathValue("field")

	authUID, ok := a.authorizeWrite(w, r, targetID)
	if !ok {
		return
	}

	_ = a.store.DelKV(r.Context(), authUID, field)
	newKV, _ := a.store.GetKV(r.Context(), authUID)
	a.manager.Sync(authUID, presence.Patch{KV: newKV}, false)

	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleQuicklink(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("file")
	parts := strings.Split(file, ".")
	if len(parts) != 2 {
		writeError(w, http.StatusNotFound, "not_found", "Route does not exist")
		return
	}

	userID := parts[0]
	ext := parts[1]

	if _, ok := supportedQuicktypes[ext]; !ok {
		writeError(w, http.StatusNotFound, "not_found", "Route does not exist")
		return
	}

	prettyJSON, ok := a.manager.Get(userID)
	if !ok {
		writeError(w, http.StatusNotFound, "user_not_monitored", "User is not being monitored by Lanyard")
		return
	}

	var parsed struct {
		DiscordUser struct {
			Avatar        *string `json:"avatar"`
			Discriminator string  `json:"discriminator"`
		} `json:"discord_user"`
	}
	_ = json.Unmarshal(prettyJSON, &parsed)

	var targetURL string
	avatar := parsed.DiscordUser.Avatar

	if avatar != nil {
		reqExt := ext
		if !strings.HasPrefix(*avatar, "a_") && ext == "gif" {
			reqExt = "jpg"
		}
		targetURL = fmt.Sprintf("https://cdn.discordapp.com/avatars/%s/%s.%s?size=1024", userID, *avatar, reqExt)
	} else {
		discrim := parsed.DiscordUser.Discriminator
		var index int64
		if discrim == "0" {
			// (id >> 22) mod 6
			idBig, ok := new(big.Int).SetString(userID, 10)
			if ok {
				idShifted := new(big.Int).Rsh(idBig, 22)
				index = new(big.Int).Mod(idShifted, big.NewInt(6)).Int64()
			}
		} else {
			if d, err := strconv.ParseInt(discrim, 10, 64); err == nil {
				index = d % 5
			}
		}
		targetURL = fmt.Sprintf("https://cdn.discordapp.com/embed/avatars/%d.png", index)
	}

	// Proxy upstream
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, targetURL, nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_error", "Failed to fetch avatar from Discord CDN")
		return
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_error", "Failed to fetch avatar from Discord CDN")
		return
	}
	defer resp.Body.Close()

	for k, vv := range resp.Header {
		if strings.EqualFold(k, "Content-Length") {
			continue
		}
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	limitReader := io.LimitReader(resp.Body, 8*1024*1024)
	_, _ = io.Copy(w, limitReader)
}
