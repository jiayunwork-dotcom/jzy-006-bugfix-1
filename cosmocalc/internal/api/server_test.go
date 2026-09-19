package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"cosmocalc/internal/store"
)

func newTestServer() *Server {
	return NewServer(store.NewMemStore())
}

func doReq(s *Server, method, path string, body any) (*httptest.ResponseRecorder, map[string]any, error) {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return nil, nil, fmt.Errorf("encode body: %w", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	var parsed map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
			return rec, nil, fmt.Errorf("response is not JSON: %w (body: %s)", err, rec.Body.String())
		}
	}
	return rec, parsed, nil
}

func do(t *testing.T, s *Server, method, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec, parsed, err := doReq(s, method, path, body)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return rec, parsed
}

func f64(v any) float64 { return v.(float64) }

func TestRedshiftEndpoint_WavelengthMode(t *testing.T) {
	s := newTestServer()
	rec, body := do(t, s, "POST", "/api/v1/redshift", map[string]any{
		"rest_wavelength":     656.28,
		"observed_wavelength": 656.28 * 1.03,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %v", rec.Code, body)
	}
	if math.Abs(f64(body["redshift"])-0.03) > 1e-9 {
		t.Fatalf("redshift = %v, want 0.03", body["redshift"])
	}
	if body["shift_type"] != "redshift" {
		t.Fatalf("shift_type = %v, want redshift", body["shift_type"])
	}
	if body["relation"] != "cosmological_linear" {
		t.Fatalf("default relation = %v, want cosmological_linear", body["relation"])
	}
}

func TestRedshiftEndpoint_ZeroRedshiftZeroVelocity(t *testing.T) {
	s := newTestServer()
	_, body := do(t, s, "POST", "/api/v1/redshift", map[string]any{
		"rest_wavelength":     500.0,
		"observed_wavelength": 500.0,
	})
	if f64(body["redshift"]) != 0 || f64(body["velocity_kms"]) != 0 {
		t.Fatalf("z=0 must give v=0, got %v", body)
	}
	if body["shift_type"] != "rest" {
		t.Fatalf("shift_type = %v, want rest", body["shift_type"])
	}
}

func TestDistanceEndpoint_BeyondLinearFlagged(t *testing.T) {
	s := newTestServer()
	rec, body := do(t, s, "POST", "/api/v1/distance", map[string]any{
		"redshift":        0.5,
		"hubble_constant": 70.0,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %v", rec.Code, body)
	}
	if body["beyond_linear_threshold"] != true || body["linear_regime"] != false {
		t.Fatalf("z=0.5 must be flagged beyond linear regime: %v", body)
	}
	if body["warning"] == nil || body["warning"] == "" {
		t.Fatalf("a warning must accompany beyond-linear results: %v", body)
	}
	// Linear approximation still returned: D = c*z/H0.
	wantD := 299792.458 * 0.5 / 70
	if math.Abs(f64(body["distance_mpc"])-wantD) > 1e-6 {
		t.Fatalf("distance = %v, want %v", body["distance_mpc"], wantD)
	}
}

func TestDistanceEndpoint_WithinLinearNotFlagged(t *testing.T) {
	s := newTestServer()
	_, body := do(t, s, "POST", "/api/v1/distance", map[string]any{
		"redshift":        0.03,
		"hubble_constant": 70.0,
	})
	if body["beyond_linear_threshold"] != false || body["linear_regime"] != true {
		t.Fatalf("z=0.03 must be inside the linear regime: %v", body)
	}
}

func TestDistanceEndpoint_BlueshiftRejected(t *testing.T) {
	s := newTestServer()
	rec, body := do(t, s, "POST", "/api/v1/distance", map[string]any{
		"rest_wavelength":     600.0,
		"observed_wavelength": 500.0,
		"hubble_constant":     70.0,
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %v", rec.Code, body)
	}
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("missing structured error: %v", body)
	}
	if errObj["type"] != "blueshift_distance" {
		t.Fatalf("error type = %v, want blueshift_distance", errObj["type"])
	}
	// Crucially, no distance field is returned.
	if _, leaked := body["distance_mpc"]; leaked {
		t.Fatalf("blueshift must not be answered with a distance: %v", body)
	}
}

func TestDistanceEndpoint_RelativisticSwitch(t *testing.T) {
	s := newTestServer()
	_, lin := do(t, s, "POST", "/api/v1/distance", map[string]any{
		"redshift": 1.0, "hubble_constant": 70.0,
	})
	_, rel := do(t, s, "POST", "/api/v1/distance", map[string]any{
		"redshift": 1.0, "hubble_constant": 70.0, "relativistic": true,
	})
	if lin["relation"] != "cosmological_linear" || rel["relation"] != "relativistic_doppler" {
		t.Fatalf("relation labels wrong: lin=%v rel=%v", lin["relation"], rel["relation"])
	}
	vLin, vRel := f64(lin["velocity_kms"]), f64(rel["velocity_kms"])
	if math.Abs(vLin-299792.458) > 1e-6 {
		t.Fatalf("linear v(z=1) = %v, want c", vLin)
	}
	if math.Abs(vRel-0.6*299792.458) > 1e-3 {
		t.Fatalf("relativistic v(z=1) = %v, want 0.6c", vRel)
	}
	if f64(lin["distance_mpc"]) <= f64(rel["distance_mpc"]) {
		t.Fatal("at z=1 the linear distance must exceed the relativistic one")
	}
}

func TestValidationErrors(t *testing.T) {
	s := newTestServer()
	cases := []struct {
		name     string
		path     string
		body     map[string]any
		wantType string
	}{
		{"negative rest wavelength", "/api/v1/redshift",
			map[string]any{"rest_wavelength": -1, "observed_wavelength": 600}, "non_positive_wavelength"},
		{"zero observed wavelength", "/api/v1/redshift",
			map[string]any{"rest_wavelength": 500, "observed_wavelength": 0}, "non_positive_wavelength"},
		{"missing input", "/api/v1/redshift", map[string]any{}, "missing_field"},
		{"lone rest wavelength", "/api/v1/redshift",
			map[string]any{"rest_wavelength": 500}, "missing_field"},
		{"zero hubble", "/api/v1/distance",
			map[string]any{"redshift": 0.03, "hubble_constant": 0}, "non_positive_hubble_constant"},
		{"negative hubble", "/api/v1/distance",
			map[string]any{"redshift": 0.03, "hubble_constant": -70}, "non_positive_hubble_constant"},
		{"missing hubble", "/api/v1/distance",
			map[string]any{"redshift": 0.03}, "missing_field"},
	}
	for _, c := range cases {
		rec, body := do(t, s, "POST", c.path, c.body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400; body = %v", c.name, rec.Code, body)
		}
		errObj, ok := body["error"].(map[string]any)
		if !ok {
			t.Errorf("%s: missing structured error: %v", c.name, body)
			continue
		}
		if errObj["type"] != c.wantType {
			t.Errorf("%s: error type = %v, want %v", c.name, errObj["type"], c.wantType)
		}
	}
}

func TestBatchEndpoint_MixedItems(t *testing.T) {
	s := newTestServer()
	rec, body := do(t, s, "POST", "/api/v1/batch", map[string]any{
		"items": []map[string]any{
			{"id": "ok-line", "rest_wavelength": 656.28, "observed_wavelength": 675.9684, "hubble_constant": 70},
			{"id": "blueshift-line", "rest_wavelength": 600, "observed_wavelength": 500, "hubble_constant": 70},
			{"id": "bad-hubble", "redshift": 0.02, "hubble_constant": -1},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %v", rec.Code, body)
	}
	results := body["results"].([]any)
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	byID := map[string]map[string]any{}
	for _, r := range results {
		rm := r.(map[string]any)
		byID[rm["id"].(string)] = rm
	}
	if byID["ok-line"]["ok"] != true {
		t.Fatalf("valid item must succeed: %v", byID["ok-line"])
	}
	okRes := byID["ok-line"]["result"].(map[string]any)
	if math.Abs(f64(okRes["redshift"])-0.03) > 1e-6 {
		t.Fatalf("batch redshift = %v, want ~0.03", okRes["redshift"])
	}
	if byID["blueshift-line"]["ok"] != false {
		t.Fatalf("blueshift item must fail: %v", byID["blueshift-line"])
	}
	bsErr := byID["blueshift-line"]["error"].(map[string]any)
	if bsErr["type"] != "blueshift_distance" {
		t.Fatalf("blueshift item error = %v, want blueshift_distance", bsErr["type"])
	}
	if byID["bad-hubble"]["ok"] != false {
		t.Fatalf("bad-hubble item must fail: %v", byID["bad-hubble"])
	}
}

func TestHistoryPersistence(t *testing.T) {
	s := newTestServer()
	do(t, s, "POST", "/api/v1/redshift", map[string]any{"redshift": 0.03})
	do(t, s, "POST", "/api/v1/distance", map[string]any{"redshift": 0.03, "hubble_constant": 70})
	do(t, s, "POST", "/api/v1/distance", map[string]any{"redshift": 0.05, "hubble_constant": 70})

	_, all := do(t, s, "GET", "/api/v1/history", nil)
	if f64(all["count"]) != 3 {
		t.Fatalf("history count = %v, want 3", all["count"])
	}
	_, distances := do(t, s, "GET", "/api/v1/history?type=distance", nil)
	if f64(distances["count"]) != 2 {
		t.Fatalf("distance history count = %v, want 2", distances["count"])
	}
	records := distances["records"].([]any)
	first := records[0].(map[string]any)
	if first["type"] != "distance" {
		t.Fatalf("record type = %v, want distance", first["type"])
	}
	// Newest first: the z=0.05 request comes before z=0.03.
	resp := first["response"].(map[string]any)
	if math.Abs(f64(resp["redshift"])-0.05) > 1e-9 {
		t.Fatalf("newest distance record redshift = %v, want 0.05", resp["redshift"])
	}
	if first["id"] == nil || first["created_at"] == nil {
		t.Fatalf("record must carry id and created_at: %v", first)
	}
}

// TestBatchHistory_EachLinePersistedIndependently is the regression test
// for the bug where a batch landed as a single "batch" summary record:
// one redshift, one single distance, and one 4-line distance batch must
// result in 6 history records in total — 1 redshift and 5 distances —
// each retrievable by type, with its own id/created_at/type.
func TestBatchHistory_EachLinePersistedIndependently(t *testing.T) {
	s := newTestServer()

	do(t, s, "POST", "/api/v1/redshift", map[string]any{"redshift": 0.02})
	do(t, s, "POST", "/api/v1/distance", map[string]any{"redshift": 0.02, "hubble_constant": 70})
	do(t, s, "POST", "/api/v1/batch", map[string]any{
		"items": []map[string]any{
			{"id": "z03", "redshift": 0.03, "hubble_constant": 70},
			{"id": "z04", "redshift": 0.04, "hubble_constant": 70},
			{"id": "z05", "redshift": 0.05, "hubble_constant": 70},
			{"id": "z06", "redshift": 0.06, "hubble_constant": 70},
		},
	})

	// 6 computations happened: exactly 6 records must exist.
	_, all := do(t, s, "GET", "/api/v1/history?limit=100", nil)
	if got := f64(all["count"]); got != 6 {
		t.Fatalf("all-history count = %v, want 6", got)
	}
	allRecs := all["records"].([]any)
	for _, r := range allRecs {
		rm := r.(map[string]any)
		if rm["id"] == nil || rm["created_at"] == nil || rm["type"] == nil {
			t.Fatalf("record must carry id, created_at and type: %v", rm)
		}
		if typ := rm["type"].(string); typ != "redshift" && typ != "distance" {
			t.Fatalf("unexpected record type %q (no batch summaries)", typ)
		}
	}

	// The four batch lines are ordinary distance records, plus the
	// single distance: 5 in total.
	_, distances := do(t, s, "GET", "/api/v1/history?type=distance&limit=100", nil)
	if got := f64(distances["count"]); got != 5 {
		t.Fatalf("distance history count = %v, want 5", got)
	}
	distRecs := distances["records"].([]any)
	gotZ := map[float64]int{}
	for _, r := range distRecs {
		rm := r.(map[string]any)
		if rm["type"] != "distance" {
			t.Fatalf("type-filtered record has type %v", rm["type"])
		}
		resp := rm["response"].(map[string]any)
		gotZ[f64(resp["redshift"])]++
	}
	for _, z := range []float64{0.02, 0.03, 0.04, 0.05, 0.06} {
		if gotZ[z] != 1 {
			t.Fatalf("distance record for z=%v found %d times, want exactly 1", z, gotZ[z])
		}
	}

	// Newest-first: the last batch line (z=0.06) leads the distance list.
	newest := distRecs[0].(map[string]any)["response"].(map[string]any)
	if math.Abs(f64(newest["redshift"])-0.06) > 1e-12 {
		t.Fatalf("newest distance record redshift = %v, want 0.06 (batch order preserved)", newest["redshift"])
	}
	// Oldest distance is the single submission (z=0.02).
	oldest := distRecs[4].(map[string]any)["response"].(map[string]any)
	if math.Abs(f64(oldest["redshift"])-0.02) > 1e-12 {
		t.Fatalf("oldest distance record redshift = %v, want 0.02", oldest["redshift"])
	}

	_, redshifts := do(t, s, "GET", "/api/v1/history?type=redshift", nil)
	if got := f64(redshifts["count"]); got != 1 {
		t.Fatalf("redshift history count = %v, want 1", got)
	}
}

// TestBatchHistory_FailedLinesPersistedWithError ensures mixed batches
// persist failures too: every line is one "distance" record, and failed
// lines carry the error message in their response body.
func TestBatchHistory_FailedLinesPersistedWithError(t *testing.T) {
	s := newTestServer()
	_, body := do(t, s, "POST", "/api/v1/batch", map[string]any{
		"items": []map[string]any{
			{"id": "ok-line", "redshift": 0.03, "hubble_constant": 70},
			{"id": "blueshift-line", "rest_wavelength": 600, "observed_wavelength": 500, "hubble_constant": 70},
			{"id": "bad-hubble", "redshift": 0.02, "hubble_constant": -1},
			{"id": "missing-hubble", "redshift": 0.02},
		},
	})
	if f64(body["count"]) != 4 {
		t.Fatalf("batch returned %v results, want 4", body["count"])
	}

	_, hist := do(t, s, "GET", "/api/v1/history?type=distance&limit=100", nil)
	if got := f64(hist["count"]); got != 4 {
		t.Fatalf("distance history count = %v, want 4 (every line, failures included)", got)
	}
	records := hist["records"].([]any)
	failures := map[string]string{} // id-less mapping by error substring
	successes := 0
	for _, r := range records {
		rm := r.(map[string]any)
		if rm["type"] != "distance" {
			t.Fatalf("type = %v, want distance", rm["type"])
		}
		if rm["id"] == nil || rm["created_at"] == nil {
			t.Fatalf("record must carry id and created_at: %v", rm)
		}
		resp := rm["response"].(map[string]any)
		if errMsg, isErr := resp["error"].(string); isErr {
			failures[errMsg] = errMsg
		} else {
			if math.Abs(f64(resp["redshift"])-0.03) > 1e-12 {
				t.Fatalf("unexpected successful record: %v", resp)
			}
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("got %d successful records, want 1", successes)
	}
	if len(failures) != 3 {
		t.Fatalf("got %d distinct failure records, want 3: %v", len(failures), failures)
	}

	// Error text must identify why each line failed, matching the
	// single-endpoint persistence format {"error": "..."}.
	joined := ""
	for _, m := range failures {
		joined += m + "\n"
	}
	if !strings.Contains(joined, "blueshift") {
		t.Fatalf("blueshift failure not persisted: %q", joined)
	}
	if !strings.Contains(joined, "Hubble constant must be positive") {
		t.Fatalf("non-positive H0 failure not persisted: %q", joined)
	}
	if !strings.Contains(joined, "hubble_constant") {
		t.Fatalf("missing-H0 failure not persisted: %q", joined)
	}
}

// TestBatchHistory_ConcurrentBatchesAndSingles fires many batches and
// single requests concurrently and asserts the number of persisted
// records equals exactly the number of computations — no loss, no
// duplication, no cross-talk.
func TestBatchHistory_ConcurrentBatchesAndSingles(t *testing.T) {
	s := newTestServer()
	const (
		batches    = 16
		linesBatch = 5
		singleDist = 32
		singleRed  = 16
	)
	var wg sync.WaitGroup
	errs := make(chan error, batches+singleDist+singleRed)

	for b := 0; b < batches; b++ {
		wg.Add(1)
		go func(b int) {
			defer wg.Done()
			items := make([]map[string]any, 0, linesBatch)
			for j := 0; j < linesBatch; j++ {
				// Unique z per (batch, line) so cross-talk is detectable.
				z := 0.001 + float64(b)*0.01 + float64(j)*0.0001
				items = append(items, map[string]any{
					"id":              fmt.Sprintf("b%d-%d", b, j),
					"redshift":        z,
					"hubble_constant": 70,
				})
			}
			if _, _, err := doReq(s, "POST", "/api/v1/batch", map[string]any{"items": items}); err != nil {
				errs <- fmt.Errorf("batch %d: %w", b, err)
			}
		}(b)
	}
	for i := 0; i < singleDist; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			z := 0.5 + float64(i)*0.001
			rec, _, err := doReq(s, "POST", "/api/v1/distance", map[string]any{
				"redshift": z, "hubble_constant": 75,
			})
			if err != nil {
				errs <- err
				return
			}
			if rec.Code != http.StatusOK {
				errs <- fmt.Errorf("single distance %d: status %d", i, rec.Code)
			}
		}(i)
	}
	for i := 0; i < singleRed; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			z := 0.1 + float64(i)*0.001
			rec, _, err := doReq(s, "POST", "/api/v1/redshift", map[string]any{"redshift": z})
			if err != nil {
				errs <- err
				return
			}
			if rec.Code != http.StatusOK {
				errs <- fmt.Errorf("single redshift %d: status %d", i, rec.Code)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	wantDist := batches*linesBatch + singleDist
	wantTotal := wantDist + singleRed

	_, all := do(t, s, "GET", "/api/v1/history?limit=1000", nil)
	if got := f64(all["count"]); float64(wantTotal) != got {
		t.Fatalf("total history = %v, want exactly %d computations", got, wantTotal)
	}
	_, dist := do(t, s, "GET", "/api/v1/history?type=distance&limit=1000", nil)
	if got := f64(dist["count"]); float64(wantDist) != got {
		t.Fatalf("distance history = %v, want %d", got, wantDist)
	}
	_, red := do(t, s, "GET", "/api/v1/history?type=redshift&limit=1000", nil)
	if got := f64(red["count"]); float64(singleRed) != got {
		t.Fatalf("redshift history = %v, want %d", got, singleRed)
	}

	// IDs must be unique: no record was saved twice.
	seenIDs := map[float64]bool{}
	recs := all["records"].([]any)
	if len(recs) != wantTotal {
		t.Fatalf("got %d serialized records, want %d", len(recs), wantTotal)
	}
	for _, r := range recs {
		id := f64(r.(map[string]any)["id"])
		if seenIDs[id] {
			t.Fatalf("duplicate persisted record id %v", id)
		}
		seenIDs[id] = true
	}
}

func TestDemoEndpoint(t *testing.T) {
	s := newTestServer()
	rec, body := do(t, s, "GET", "/api/v1/demo", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %v", rec.Code, body)
	}
	res := body["result"].(map[string]any)
	if math.Abs(f64(res["distance_mpc"])-128.482) > 0.01 {
		t.Fatalf("demo distance = %v Mpc, want ~128.48 Mpc", res["distance_mpc"])
	}
	if math.Abs(f64(res["velocity_kms"])-8993.77374) > 0.01 {
		t.Fatalf("demo velocity = %v km/s, want ~8993.77 km/s", res["velocity_kms"])
	}
}

func TestStatusEndpoint(t *testing.T) {
	s := newTestServer()
	rec, body := do(t, s, "GET", "/status", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if body["status"] != "ok" || body["database"] != "ok" {
		t.Fatalf("unexpected status body: %v", body)
	}
	units := body["units"].(map[string]any)
	if units["distance"] != "Mpc" || units["velocity"] != "km/s" || units["hubble_constant"] != "km/s/Mpc" {
		t.Fatalf("unit conventions must be exposed: %v", units)
	}
}

// Concurrent requests with distinct inputs must each get their own
// correct result, and every request must land in history exactly once.
func TestConcurrentRequestsNoCrossTalk(t *testing.T) {
	s := newTestServer()
	const n = 64
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			z := 0.01 + float64(i)*0.001
			h0 := 60.0 + float64(i)
			rec, body, err := doReq(s, "POST", "/api/v1/distance", map[string]any{
				"redshift": z, "hubble_constant": h0,
			})
			if err != nil {
				errs <- fmt.Errorf("request %d: %v", i, err)
				return
			}
			if rec.Code != http.StatusOK {
				errs <- fmt.Errorf("request %d: status %d", i, rec.Code)
				return
			}
			wantV := 299792.458 * z
			wantD := wantV / h0
			if math.Abs(f64(body["velocity_kms"])-wantV) > 1e-6 {
				errs <- fmt.Errorf("request %d: velocity %v, want %v", i, body["velocity_kms"], wantV)
				return
			}
			if math.Abs(f64(body["distance_mpc"])-wantD) > 1e-6 {
				errs <- fmt.Errorf("request %d: distance %v, want %v", i, body["distance_mpc"], wantD)
				return
			}
			if math.Abs(f64(body["hubble_constant"])-h0) > 1e-12 {
				errs <- fmt.Errorf("request %d: hubble_constant echoed %v, want %v", i, body["hubble_constant"], h0)
				return
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	_, hist := do(t, s, "GET", "/api/v1/history?type=distance&limit=200", nil)
	if f64(hist["count"]) != n {
		t.Fatalf("history holds %v records, want exactly %d", hist["count"], n)
	}
	seen := map[float64]bool{}
	for _, r := range hist["records"].([]any) {
		resp := r.(map[string]any)["response"].(map[string]any)
		z := f64(resp["redshift"])
		if seen[z] {
			t.Fatalf("duplicate history entry for z=%v", z)
		}
		seen[z] = true
	}
	if len(seen) != n {
		t.Fatalf("history holds %d distinct redshifts, want %d", len(seen), n)
	}
}
