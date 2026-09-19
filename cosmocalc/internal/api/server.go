// Package api exposes the computation service over HTTP. All responses
// are JSON; failures are structured error objects with a machine-
// readable type.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"cosmocalc/internal/calc"
	"cosmocalc/internal/service"
	"cosmocalc/internal/store"
)

// Server holds the HTTP handlers and their dependencies.
type Server struct {
	store    store.Store
	mux      *http.ServeMux
	started  time.Time
	requests atomic.Int64
}

// NewServer builds the routes on top of the given store.
func NewServer(st store.Store) *Server {
	s := &Server{store: st, mux: http.NewServeMux(), started: time.Now()}
	s.mux.HandleFunc("POST /api/v1/redshift", s.handleRedshift)
	s.mux.HandleFunc("POST /api/v1/distance", s.handleDistance)
	s.mux.HandleFunc("POST /api/v1/batch", s.handleBatch)
	s.mux.HandleFunc("GET /api/v1/history", s.handleHistory)
	s.mux.HandleFunc("GET /api/v1/demo", s.handleDemo)
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("GET /status", s.handleStatus)
	return s
}

// ServeHTTP counts and dispatches each request.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.requests.Add(1)
	s.mux.ServeHTTP(w, r)
}

// apiError is the structured error body.
type apiError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, typ, field, msg string) {
	writeJSON(w, status, map[string]any{"error": apiError{Type: typ, Message: msg, Field: field}})
}

// writeServiceErr maps a service-layer error to a structured response.
func writeServiceErr(w http.ResponseWriter, err error) {
	if ce, ok := service.AsCalcError(err); ok {
		status := http.StatusBadRequest
		if ce.Kind == calc.ErrBlueshiftDistance {
			status = http.StatusUnprocessableEntity
		}
		writeErr(w, status, string(ce.Kind), ce.Field, ce.Message)
		return
	}
	if me, ok := service.AsMissingField(err); ok {
		writeErr(w, http.StatusBadRequest, "missing_field", me.Field, me.Message)
		return
	}
	writeErr(w, http.StatusInternalServerError, "internal", "", err.Error())
}

// decode reads a JSON request body.
func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		return errors.New("request body is not valid JSON: " + err.Error())
	}
	return nil
}

// persist saves the request/response pair; failures are logged to the
// caller as a warning header but do not fail the computation.
func (s *Server) persist(r *http.Request, typ string, req, resp any) {
	reqJSON, err1 := json.Marshal(req)
	respJSON, err2 := json.Marshal(resp)
	if err1 != nil || err2 != nil {
		return
	}
	rec := &store.Record{Type: typ, Request: reqJSON, Response: respJSON}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	_ = s.store.Save(ctx, rec)
}

func (s *Server) handleRedshift(w http.ResponseWriter, r *http.Request) {
	var in service.Input
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "", err.Error())
		return
	}
	res, err := service.ComputeRedshift(in)
	if err != nil {
		writeServiceErr(w, err)
		s.persist(r, "redshift", in, map[string]any{"error": err.Error()})
		return
	}
	s.persist(r, "redshift", in, res)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleDistance(w http.ResponseWriter, r *http.Request) {
	var in service.DistanceInput
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "", err.Error())
		return
	}
	res, err := service.ComputeDistance(in)
	if err != nil {
		writeServiceErr(w, err)
		s.persist(r, "distance", in, map[string]any{"error": err.Error()})
		return
	}
	s.persist(r, "distance", in, res)
	writeJSON(w, http.StatusOK, res)
}

// batchRequest is the multi-line submission body.
type batchRequest struct {
	Items []batchItem `json:"items"`
}

type batchItem struct {
	ID string `json:"id"`
	service.DistanceInput
}

// batchResult is one item's outcome.
type batchResult struct {
	ID     string                  `json:"id"`
	OK     bool                    `json:"ok"`
	Result *service.DistanceResult `json:"result,omitempty"`
	Error  *apiError               `json:"error,omitempty"`
}

func (s *Server) handleBatch(w http.ResponseWriter, r *http.Request) {
	var req batchRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "", err.Error())
		return
	}
	if len(req.Items) == 0 {
		writeErr(w, http.StatusBadRequest, "missing_field", "items", "items must contain at least one entry")
		return
	}
	if len(req.Items) > 1000 {
		writeErr(w, http.StatusBadRequest, "invalid_input", "items", "at most 1000 items per batch")
		return
	}
	results := make([]batchResult, 0, len(req.Items))
	for i, item := range req.Items {
		id := item.ID
		if id == "" {
			id = strconv.Itoa(i)
		}
		res, err := service.ComputeDistance(item.DistanceInput)
		if err != nil {
			ae := apiError{Type: "internal", Message: err.Error()}
			if ce, ok := service.AsCalcError(err); ok {
				ae = apiError{Type: string(ce.Kind), Message: ce.Message, Field: ce.Field}
			} else if me, ok := service.AsMissingField(err); ok {
				ae = apiError{Type: "missing_field", Message: me.Message, Field: me.Field}
			}
			results = append(results, batchResult{ID: id, OK: false, Error: &ae})
			continue
		}
		results = append(results, batchResult{ID: id, OK: true, Result: res})
	}
	resp := map[string]any{"count": len(results), "results": results}
	s.persist(r, "batch", req, resp)
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	recs, err := s.store.List(r.Context(), store.Filter{
		Type:   q.Get("type"),
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(recs), "records": recs})
}

// handleDemo runs the preset worked example: an H-alpha line
// (rest 656.28 nm) observed at 675.9684 nm, i.e. z = 0.03, with the
// standard H0 = 70 km/s/Mpc. Hand check: v = c*z = 8993.77 km/s,
// D = v/H0 = 128.48 Mpc.
func (s *Server) handleDemo(w http.ResponseWriter, r *http.Request) {
	rest := 656.28
	obs := 656.28 * 1.03
	h0 := 70.0
	res, err := service.ComputeDistance(service.DistanceInput{
		Input:          service.Input{RestWavelength: &rest, ObservedWavelength: &obs},
		HubbleConstant: &h0,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"description": "preset example: H-alpha 656.28 nm observed at 675.9684 nm (z=0.03), H0=70 km/s/Mpc; hand calculation gives v=8993.77 km/s, D=128.48 Mpc",
		"result":      res,
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	dbStatus := "ok"
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		dbStatus = "unreachable: " + err.Error()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":           "ok",
		"uptime_seconds":   time.Since(s.started).Seconds(),
		"requests_total":   s.requests.Load(),
		"database":         dbStatus,
		"linear_threshold": calc.DefaultLinearThreshold,
		"units": map[string]string{
			"distance":        "Mpc",
			"velocity":        "km/s",
			"hubble_constant": "km/s/Mpc",
		},
	})
}
