package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/nutmos/expensetrackr/pkg/common_type"
)

func TestPathUID(t *testing.T) {
	const canonical = "a1b2c3d4-e5f6-4789-a012-3456789abcde"
	for _, tc := range []struct {
		name   string
		param  string
		want   common_type.UID
		wantOK bool
	}{
		{"canonical", canonical, canonical, true},
		{"uppercase", "A1B2C3D4-E5F6-4789-A012-3456789ABCDE", canonical, true},
		{"surrounding whitespace", "  " + canonical + "\t", canonical, true},
		{"empty", "", "", false},
		{"not a uuid", "abc", "", false},
		{"dash-less", "a1b2c3d4e5f64789a0123456789abcde", "", false},
		{"braced", "{" + canonical + "}", "", false},
		{"urn form", "urn:uuid:" + canonical, "", false},
		{"non-hex digit", "a1b2c3d4-e5f6-4789-a012-3456789abcdg", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Params = gin.Params{{Key: "uid", Value: tc.param}}
			got, ok := pathUID(c)
			if got != tc.want || ok != tc.wantOK {
				t.Fatalf("pathUID(%q) = %q, %v; want %q, %v", tc.param, got, ok, tc.want, tc.wantOK)
			}
			if !ok && rec.Code != http.StatusBadRequest {
				t.Errorf("status %d, want 400", rec.Code)
			}
		})
	}
}

func TestUIDNormalisedAtBoundary(t *testing.T) {
	h := newTestServer(t)
	path, created := createBal(t, h, `{"name":"Cash","type":"payment_account","currency":"THB","balance":"1"}`)
	uid := created["uid"].(string)
	for _, tc := range []struct {
		name string
		path string
		want int
	}{
		{"canonical", path, http.StatusOK},
		{"uppercase", "/api/balances/" + strings.ToUpper(uid), http.StatusOK},
		{"malformed", "/api/balances/not-a-uuid", http.StatusBadRequest},
		{"unknown", "/api/balances/00000000-0000-4000-8000-000000000001", http.StatusNotFound},
		{"category filter uppercase", "/api/transactions?category_uid=" + strings.ToUpper(uid), http.StatusOK},
		{"category filter malformed", "/api/transactions?category_uid=nope", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if rec, _ := do(t, h, http.MethodGet, tc.path, ""); rec.Code != tc.want {
				t.Errorf("GET %s: %d, want %d", tc.path, rec.Code, tc.want)
			}
		})
	}
}
