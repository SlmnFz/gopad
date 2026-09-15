package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/local/gopad/internal/crdt"
)

func TestHistoryRoutesClampAndReplayWithoutSnapshot(t *testing.T) {
	database := openServerTestStore(t)
	document, err := database.CreateDocument(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a := crdt.CharID{SiteID: "history-api", Counter: 1}
	b := crdt.CharID{SiteID: "history-api", Counter: 2}
	if err := database.AppendOperations(context.Background(), document.ID, []crdt.Operation{
		{Type: crdt.Insert, ID: a, Value: 'A'},
		{Type: crdt.Insert, ID: b, Value: 'B', LeftID: &a},
	}); err != nil {
		t.Fatal(err)
	}
	handler := New(database)

	var bounds struct {
		MinSeq     int64  `json:"minSeq"`
		MaxSeq     int64  `json:"maxSeq"`
		CurrentSeq int64  `json:"currentSeq"`
		CreatedAt  string `json:"createdAt"`
	}
	recorder := requestHistory(t, handler, "/api/documents/"+document.Slug+"/history/range")
	if recorder.Code != http.StatusOK {
		t.Fatalf("range status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if err := json.NewDecoder(recorder.Body).Decode(&bounds); err != nil {
		t.Fatal(err)
	}
	if bounds.MinSeq != 0 || bounds.MaxSeq != 2 || bounds.CurrentSeq != 2 || bounds.CreatedAt == "" {
		t.Fatalf("unexpected bounds: %#v", bounds)
	}

	for _, test := range []struct {
		name     string
		query    string
		wantSeq  int64
		wantText string
	}{
		{name: "below minimum", query: "?seq=-10", wantSeq: 0, wantText: ""},
		{name: "middle", query: "?seq=1", wantSeq: 1, wantText: "A"},
		{name: "above maximum", query: "?seq=100", wantSeq: 2, wantText: "AB"},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := requestHistory(t, handler, "/api/documents/"+document.Slug+"/history"+test.query)
			if recorder.Code != http.StatusOK {
				t.Fatalf("point status = %d, body=%s", recorder.Code, recorder.Body.String())
			}
			var point struct {
				Sequence int64  `json:"sequence"`
				Text     string `json:"text"`
			}
			if err := json.NewDecoder(recorder.Body).Decode(&point); err != nil {
				t.Fatal(err)
			}
			if point.Sequence != test.wantSeq || point.Text != test.wantText {
				t.Fatalf("point = %#v, want sequence=%d text=%q", point, test.wantSeq, test.wantText)
			}
		})
	}

	bad := requestHistory(t, handler, "/api/documents/"+document.Slug+"/history?seq=not-a-number")
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid sequence status = %d, want %d", bad.Code, http.StatusBadRequest)
	}
}

func requestHistory(t *testing.T, handler http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return recorder
}
