package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIndexAndAPI(t *testing.T) {
	handler := New()

	indexRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	indexResponse := httptest.NewRecorder()
	handler.ServeHTTP(indexResponse, indexRequest)
	if indexResponse.Code != http.StatusOK || !strings.Contains(indexResponse.Body.String(), "Go Map Lab") {
		t.Fatalf("index response: status=%d body=%q", indexResponse.Code, indexResponse.Body.String())
	}

	body, _ := json.Marshal(request{Mode: "swiss", Kind: "insert", Key: 1, Value: 10})
	apiRequest := httptest.NewRequest(http.MethodPost, "/api/operation", bytes.NewReader(body))
	apiRequest.Header.Set("Content-Type", "application/json")
	apiResponse := httptest.NewRecorder()
	handler.ServeHTTP(apiResponse, apiRequest)
	if apiResponse.Code != http.StatusOK {
		t.Fatalf("API status=%d body=%q", apiResponse.Code, apiResponse.Body.String())
	}
	if !strings.Contains(apiResponse.Body.String(), `"mode":"swiss"`) {
		t.Fatalf("unexpected API body %q", apiResponse.Body.String())
	}
}
