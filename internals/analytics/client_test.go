package analytics

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// Regression: an empty object id used to be formatted into "/%s/insights"
// producing graph.facebook.com/v23.0//insights — Meta answered
// "Object with ID 'insights' does not exist" (code 100 subcode 33).
// Empty ids must now fail locally without any network call.
func TestEmptyObjectIDFailsLocally(t *testing.T) {

	var hits int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
	}))
	defer ts.Close()

	old := GraphBaseURL()
	SetGraphBaseURL(ts.URL)
	defer SetGraphBaseURL(old)

	if _, err := FetchMetrics("tok", "", ScopeCampaign, "last_30d"); err == nil {
		t.Error("FetchMetrics with empty objectID must fail")
	}
	if _, err := FetchDaily("tok", "", ScopeAdSet, "last_30d"); err == nil {
		t.Error("FetchDaily with empty objectID must fail")
	}
	if _, err := FetchMetricsRange("tok", "", ScopeAd, "2026-01-01", "2026-01-07"); err == nil {
		t.Error("FetchMetricsRange with empty objectID must fail")
	}
	if _, err := ListObjects("tok", "", "campaigns"); err == nil {
		t.Error("ListObjects with empty accountID must fail")
	}

	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Errorf("mock server hit %d times; empty-id calls must not reach Meta", got)
	}
}
