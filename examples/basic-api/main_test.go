package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBasicAPI(t *testing.T) {
	app := newApp()
	for _, tt := range []struct {
		method, path, body string
		code               int
		want               string
	}{
		{"GET", "/", "", 200, `{"framework":"Golar","status":"ok"}`},
		{"GET", "/health", "", 200, "ok"},
		{"GET", "/api/users/42", "", 200, `{"id":"42"}`},
		{"POST", "/api/users", `{"name":"Ali"}`, 201, `{"name":"Ali"}`},
		{"POST", "/api/users", `{`, 400, "Invalid JSON\n"},
		{"POST", "/api/users", `{}`, 422, "Name is required\n"},
		{"GET", "/missing", "", 404, "404 page not found\n"},
		{"DELETE", "/api/users", "", 405, "Method Not Allowed\n"},
	} {
		t.Run(tt.method+tt.path+tt.body, func(t *testing.T) {
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body)))
			if rec.Code != tt.code {
				t.Fatalf("status = %d, want %d", rec.Code, tt.code)
			}
			if strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
				var got, want any
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(tt.want), &want); err != nil {
					t.Fatal(err)
				}
				gotBytes, _ := json.Marshal(got)
				wantBytes, _ := json.Marshal(want)
				if string(gotBytes) != string(wantBytes) {
					t.Errorf("body = %s, want %s", gotBytes, wantBytes)
				}
			} else if rec.Body.String() != tt.want {
				t.Errorf("body = %q, want %q", rec.Body.String(), tt.want)
			}
			if tt.code == 405 && rec.Header().Get("Allow") != "POST" {
				t.Error(rec.Header())
			}
		})
	}
}
