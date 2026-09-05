// usage_test.go — table-driven test untuk parseGlmLimits (pure, tanpa network)
// dan smoke test route /v1/usage via httptest.
package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hafidzrizqullahprasetya/octane-zai/internal/db"
)

func TestParseGlmLimits(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantPlan  string
		wantQuota map[string]Quota
		wantErr   bool
	}{
		{
			name: "tokens limit unit 3 (session 5h)",
			body: `{"data":{"level":"GLM_CODING_PLAN","limits":[{"type":"TOKENS_LIMIT","percentage":42.5,"nextResetTime":1793900000000,"unit":3,"number":5}]}}`,
			wantPlan: "Glm_coding_plan",
			wantQuota: map[string]Quota{
				"Session (5h)": {
					Used:                42.5,
					Total:               100,
					Remaining:           57.5,
					RemainingPercentage: 57.5,
					ResetAt:             strPtr("2026-11-05T17:33:20Z"),
					Unlimited:           false,
				},
			},
		},
		{
			name: "tokens limit unit 6 (weekly 7d)",
			body: `{"data":{"level":"pro","limits":[{"type":"TOKENS_LIMIT","percentage":10,"nextResetTime":0,"unit":6}]}}`,
			wantPlan: "Pro",
			wantQuota: map[string]Quota{
				"Weekly (7d)": {
					Used: 10, Total: 100, Remaining: 90, RemainingPercentage: 90,
					ResetAt: nil, Unlimited: false,
				},
			},
		},
		{
			name: "tokens limit unit 0 (Tokens)",
			body: `{"data":{"level":"","limits":[{"type":"TOKENS_LIMIT","percentage":0,"nextResetTime":1793900000000,"unit":0}]}}`,
			wantPlan: "Unknown",
			wantQuota: map[string]Quota{
				"Tokens": {
					Used: 0, Total: 100, Remaining: 100, RemainingPercentage: 100,
					ResetAt: strPtr("2026-11-05T17:33:20Z"), Unlimited: false,
				},
			},
		},
		{
			name: "credit limit tanpa unit dikenal (Limit <number>)",
			body: `{"data":{"level":"max","limits":[{"type":"CREDIT_LIMIT","percentage":77,"nextResetTime":1793900000000,"unit":2,"number":300}]}}`,
			wantPlan: "Max",
			wantQuota: map[string]Quota{
				"Limit (300)": {
					Used: 77, Total: 100, Remaining: 23, RemainingPercentage: 23,
					ResetAt: strPtr("2026-11-05T17:33:20Z"), Unlimited: false,
				},
			},
		},
		{
			name: "multiple limits tanpa overwrite",
			body: `{"data":{"level":"glm","limits":[` +
				`{"type":"TOKENS_LIMIT","percentage":20,"nextResetTime":0,"unit":3,"number":5},` +
				`{"type":"TOKENS_LIMIT","percentage":30,"nextResetTime":0,"unit":6},` +
				`{"type":"CREDIT_LIMIT","percentage":40,"nextResetTime":0,"unit":0,"number":100}]}}`,
			wantPlan: "Glm",
			wantQuota: map[string]Quota{
				"Session (5h)": {Used: 20, Total: 100, Remaining: 80, RemainingPercentage: 80, ResetAt: nil, Unlimited: false},
				"Weekly (7d)":  {Used: 30, Total: 100, Remaining: 70, RemainingPercentage: 70, ResetAt: nil, Unlimited: false},
				"Limit (100)":  {Used: 40, Total: 100, Remaining: 60, RemainingPercentage: 60, ResetAt: nil, Unlimited: false},
			},
		},
		{
			name: "type tak dikenal di-skip",
			body: `{"data":{"level":"pro","limits":[{"type":"WEIRD_LIMIT","percentage":99,"unit":3,"number":5}]}}`,
			wantPlan: "Pro",
			wantQuota: map[string]Quota{},
		},
		{
			name:      "percentage negative clamp ke 0",
			body:      `{"data":{"level":"pro","limits":[{"type":"TOKENS_LIMIT","percentage":120,"unit":0}]}}`,
			wantPlan:  "Pro",
			wantQuota: map[string]Quota{"Tokens": {Used: 120, Total: 100, Remaining: 0, RemainingPercentage: 0, ResetAt: nil, Unlimited: false}},
		},
		{
			name:      "percentage string numerik diterima (cf. glm.js)",
			body:      `{"data":{"level":"pro","limits":[{"type":"TOKENS_LIMIT","percentage":"42.5","unit":0}]}}`,
			wantPlan:  "Pro",
			wantQuota: map[string]Quota{"Tokens": {Used: 42.5, Total: 100, Remaining: 57.5, RemainingPercentage: 57.5, ResetAt: nil, Unlimited: false}},
		},
		{
			name:      "percentage string tanpa spasi + invalid di-skip sendiri",
			body:      `{"data":{"level":"lite","limits":[{"type":"TOKENS_LIMIT","percentage":" 15 ","unit":0},{"type":"TOKENS_LIMIT","percentage":"bukan-angka","unit":3,"number":5}]}}`,
			wantPlan:  "Lite",
			wantQuota: map[string]Quota{"Tokens": {Used: 15, Total: 100, Remaining: 85, RemainingPercentage: 85, ResetAt: nil, Unlimited: false}},
		},
		{
			name:      "percentage null/objek di-skip, limit lain tetap",
			body:      `{"data":{"level":"max","limits":[{"type":"TOKENS_LIMIT","percentage":null,"unit":6},{"type":"CREDIT_LIMIT","percentage":55,"unit":0,"number":9}]}}`,
			wantPlan:  "Max",
			wantQuota: map[string]Quota{"Limit (9)": {Used: 55, Total: 100, Remaining: 45, RemainingPercentage: 45, ResetAt: nil, Unlimited: false}},
		},
		{
			name:    "respons rusak: bukan json",
			body:    `<html>oops</html>`,
			wantErr: true,
		},
		{
			name:    "respons rusak: tanpa data",
			body:    `{"code":200,"msg":"ok"}`,
			wantErr: true,
		},
		{
			name:    "respons rusak: data null",
			body:    `{"data":null}`,
			wantErr: true,
		},
		{
			name:      "limits kosong — plan tetap ke-parse",
			body:      `{"data":{"level":"lite","limits":[]}}`,
			wantPlan:  "Lite",
			wantQuota: map[string]Quota{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, quotas, err := parseGlmLimits([]byte(tt.body))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseGlmLimits() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseGlmLimits() unexpected error: %v", err)
			}
			if plan != tt.wantPlan {
				t.Errorf("plan = %q, want %q", plan, tt.wantPlan)
			}
			if len(quotas) != len(tt.wantQuota) {
				t.Fatalf("jumlah quota = %d (%v), want %d", len(quotas), quotas, len(tt.wantQuota))
			}
			for k, want := range tt.wantQuota {
				got, ok := quotas[k]
				if !ok {
					t.Fatalf("quota key %q tidak ada; got keys %v", k, keysOf(quotas))
				}
				if !quotaEqual(got, want) {
					t.Errorf("quota[%q] = %+v, want %+v", k, got, want)
				}
			}
		})
	}
}

func TestUsageRoute(t *testing.T) {
	// handleUsage membaca db.ListAccounts() — init DB sqlite ke temp dir
	// (lokal, tanpa network) supaya global db.DB tidak nil.
	if err := db.Init(t.TempDir()); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(db.Close)

	s := New(nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Accounts    []usageAccountResult `json:"accounts"`
		GeneratedAt string               `json:"generatedAt"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response bukan JSON: %v", err)
	}
	if out.GeneratedAt == "" {
		t.Errorf("generatedAt kosong")
	}
}

func TestUsageRouteWithAPIKey(t *testing.T) {
	if err := db.Init(t.TempDir()); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(db.Close)

	s := New(nil).WithAPIKey("secret-key")

	// Tanpa auth → 401
	req := httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("status tanpa auth = %d, want 401", rec.Code)
	}

	// Dengan auth → 200
	req2 := httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
	req2.Header.Set("Authorization", "Bearer secret-key")
	rec2 := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != 200 {
		t.Fatalf("status dengan auth = %d, want 200", rec2.Code)
	}
	if !strings.Contains(rec2.Body.String(), "generatedAt") {
		t.Errorf("body tidak mengandung generatedAt: %s", rec2.Body.String())
	}
}

func strPtr(s string) *string { return &s }

// quotaEqual membandingkan Quota termasuk isi pointer ResetAt.
// (Catatan: == pada struct dengan field pointer membandingkan alamat,
// jadi scalar dibandingkan eksplisit.)
func quotaEqual(a, b Quota) bool {
	if a.Used != b.Used || a.Total != b.Total ||
		a.Remaining != b.Remaining ||
		a.RemainingPercentage != b.RemainingPercentage ||
		a.Unlimited != b.Unlimited {
		return false
	}
	switch {
	case a.ResetAt == nil && b.ResetAt == nil:
		return true
	case a.ResetAt == nil || b.ResetAt == nil:
		return false
	default:
		return *a.ResetAt == *b.ResetAt
	}
}

func keysOf(m map[string]Quota) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
