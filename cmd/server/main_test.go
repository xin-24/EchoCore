package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealth(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	res := httptest.NewRecorder()

	newHandler().ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", res.Code, http.StatusOK)
	}
	if got, want := res.Header().Get("Content-Type"), "application/json; charset=utf-8"; got != want {
		t.Fatalf("Content-Type = %q, want %q", got, want)
	}
	if got, want := res.Body.String(), "{\"status\":\"ok\"}\n"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestHealthRejectsOtherMethods(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	res := httptest.NewRecorder()

	newHandler().ServeHTTP(res, req)

	if res.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status code = %d, want %d", res.Code, http.StatusMethodNotAllowed)
	}
}
