package memory

import (
	"fmt"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMemorywire_RememberAndRecall(t *testing.T) {
	store, cleanup := setupTestFileStore(t)
	defer cleanup()
	handler := NewMemorywireHandler(store)

	// Remember a fact.
	rememberBody := `{"operations":[{"op":"remember","content":"Alice prefers dark mode in all applications.","scope":"user","user_id":"alice"}]}`
	req := httptest.NewRequest(http.MethodPost, "/memorywire", bytes.NewReader([]byte(rememberBody)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("remember: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp MWResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].Status != "ok" {
		t.Fatalf("remember failed: %+v", resp.Results)
	}
	memID := resp.Results[0].ID

	// Recall the fact.
	recallBody := `{"operations":[{"op":"recall","query":"dark mode","limit":5}]}`
	req = httptest.NewRequest(http.MethodPost, "/memorywire", bytes.NewReader([]byte(recallBody)))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("recall: expected 200, got %d", rec.Code)
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode recall response: %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].Status != "ok" {
		t.Fatalf("recall failed: %+v", resp.Results)
	}
	found := false
	for _, m := range resp.Results[0].Memories {
		if m.ID == memID {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("recalled memories should contain the remembered fact")
	}
}


func TestMemorywire_Forget(t *testing.T) {
	store, cleanup := setupTestFileStore(t)
	defer cleanup()
	handler := NewMemorywireHandler(store)

	// Remember a fact.
	rememberBody := `{"operations":[{"op":"remember","id":"forget-me","content":"Forget test content.","scope":"user"}]}`
	req := httptest.NewRequest(http.MethodPost, "/memorywire", bytes.NewReader([]byte(rememberBody)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var resp MWResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Results[0].Status != "ok" {
		t.Fatalf("remember failed: %+v", resp.Results[0])
	}

	// Forget should succeed (soft-invalidate).
	forgetBody := `{"operations":[{"op":"forget","id":"forget-me"}]}`
	req = httptest.NewRequest(http.MethodPost, "/memorywire", bytes.NewReader([]byte(forgetBody)))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Results[0].Status != "ok" {
		t.Fatalf("first forget failed: %+v", resp.Results[0])
	}

	// Double forget should fail (already invalidated).
	req = httptest.NewRequest(http.MethodPost, "/memorywire", bytes.NewReader([]byte(forgetBody)))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Results[0].Status != "error" {
		t.Fatalf("expected error on double forget, got status=%s", resp.Results[0].Status)
	}
}
func TestMemorywire_Merge(t *testing.T) {
	store, cleanup := setupTestFileStore(t)
	defer cleanup()
	handler := NewMemorywireHandler(store)

	// Remember original.
	body := `{"operations":[{"op":"remember","id":"merge-me","content":"Original content.","scope":"user"}]}`
	req := httptest.NewRequest(http.MethodPost, "/memorywire", bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Merge updated content.
	mergeBody := `{"operations":[{"op":"merge","id":"merge-me","content":"Updated content with more details.","version":1}]}`
	req = httptest.NewRequest(http.MethodPost, "/memorywire", bytes.NewReader([]byte(mergeBody)))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var resp MWResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Results[0].Status != "ok" {
		t.Fatalf("merge failed: %+v", resp.Results[0])
	}

	// Verify updated content.
	recallBody := `{"operations":[{"op":"recall","query":"Updated content","limit":5}]}`
	req = httptest.NewRequest(http.MethodPost, "/memorywire", bytes.NewReader([]byte(recallBody)))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Results[0].Memories) == 0 {
		t.Fatal("expected updated content to be findable")
	}
}

func TestMemorywire_Expire(t *testing.T) {
	store, cleanup := setupTestFileStore(t)
	defer cleanup()
	handler := NewMemorywireHandler(store)

	// Remember.
	body := `{"operations":[{"op":"remember","id":"expire-me","content":"Will expire soon.","scope":"user"}]}`
	req := httptest.NewRequest(http.MethodPost, "/memorywire", bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Expire it (soft invalidation).
	expireBody := fmt.Sprintf(`{"operations":[{"op":"expire","id":"expire-me","valid_to":"%s"}]}`,
		time.Now().UTC().Format(time.RFC3339))
	req = httptest.NewRequest(http.MethodPost, "/memorywire", bytes.NewReader([]byte(expireBody)))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var resp MWResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Results[0].Status != "ok" {
		t.Fatalf("expire failed: %+v", resp.Results[0])
	}

	// Verify it's gone from default search.
	recallBody := `{"operations":[{"op":"recall","query":"Will expire"}]}`
	req = httptest.NewRequest(http.MethodPost, "/memorywire", bytes.NewReader([]byte(recallBody)))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Results[0].Memories) > 0 {
		t.Fatal("expected expired fact to be gone from default search")
	}
}

func TestMemorywire_UnknownOperation(t *testing.T) {
	store, cleanup := setupTestFileStore(t)
	defer cleanup()
	handler := NewMemorywireHandler(store)

	body := `{"operations":[{"op":"invent","id":"test"}]}`
	req := httptest.NewRequest(http.MethodPost, "/memorywire", bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp MWResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Results[0].Status != "error" {
		t.Fatal("expected error for unknown operation")
	}
}

func TestMemorywire_BatchOperations(t *testing.T) {
	store, cleanup := setupTestFileStore(t)
	defer cleanup()
	handler := NewMemorywireHandler(store)

	// Batch: remember + recall.
	body := `{"operations":[
		{"op":"remember","id":"batch-1","content":"First batch fact.","scope":"user"},
		{"op":"remember","id":"batch-2","content":"Second batch fact.","scope":"user"},
		{"op":"recall","query":"batch fact","limit":5}
	]}`
	req := httptest.NewRequest(http.MethodPost, "/memorywire", bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("batch: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp MWResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(resp.Results))
	}
	if resp.Results[0].Status != "ok" || resp.Results[1].Status != "ok" {
		t.Fatal("batch remember failed")
	}
	if len(resp.Results[2].Memories) < 2 {
		t.Fatalf("expected >=2 recall results, got %d", len(resp.Results[2].Memories))
	}
}
