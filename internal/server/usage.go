// usage.go menyediakan endpoint /v1/usage: kuota GLM live per akun aktif
// (fetch paralel worker pool max 4, timeout 8s/akun) + parsing semantik
// limits Z.ai yang dipisah jadi pure function agar bisa di-test tanpa network.
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hafidzrizqullahprasetya/octane-zai/internal/db"
)

// Quota adalah satu baris kuota (kontrak UI Quota Tracker, lihat
// open-sse/services/usage/glm.js).
type Quota struct {
	Used                float64 `json:"used"`
	Total               float64 `json:"total"`
	Remaining           float64 `json:"remaining"`
	RemainingPercentage float64 `json:"remainingPercentage"`
	ResetAt             *string `json:"resetAt"`
	Unlimited           bool    `json:"unlimited"`
}

// usageAccountResult adalah output per akun untuk response /v1/usage.
type usageAccountResult struct {
	Name        string           `json:"name"`
	Plan        string           `json:"plan"`
	Quotas      map[string]Quota `json:"quotas"`
	Points      int              `json:"points"`
	FetchedLive bool             `json:"fetchedLive"`
	Error       string           `json:"error"`
}

// logUsageStream mengekstrak usage dari SSE stream body (event terakhir yang
// membawa "usage") dan mencatatnya ke tabel logs. Parser ringan: cari segmen
// data JSON terakhir yang punya field usage. Fail-open total.
func logUsageStream(acctID int64, model string, body []byte) {
	if len(body) == 0 {
		return
	}
	// Ambil kemunculan TERAKHIR dari blok usage di stream (event final).
	idx := bytes.LastIndex(body, []byte(`"usage":`))
	if idx < 0 {
		return
	}
	// Ambil sampai "total_tokens":<n> kalau ada, else 512 byte.
	end := bytes.Index(body[idx:], []byte(`}}`))
	if end < 0 || end > 768 {
		end = 512
	}
	// Bungkus jadi objek JSON valid: {"usage":{...}} — captured segment
	// sudah menutup usage obj, wrapper perlu kurung tutup sendiri.
	snippet := append(append([]byte(`{`), body[idx:idx+end+2]...), '}')
	var data struct {
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(snippet, &data); err != nil || data.Usage == nil {
		return
	}
	if data.Usage.TotalTokens <= 0 && data.Usage.PromptTokens <= 0 {
		return
	}
	if _, err := db.DB.Exec(`INSERT INTO logs (account_id, model, prompt_tokens, completion_tokens, total_tokens, cost, status, error)
	         VALUES (?, ?, ?, ?, ?, 0, 'success', '')`,
		acctID, model, data.Usage.PromptTokens, data.Usage.CompletionTokens, data.Usage.TotalTokens); err != nil {
		return
	}
}

// usage24h menjumlahkan total token yang dipakai akun dalam 24 jam terakhir
// dari tabel logs (sumber: server.handleChat; created_at UTC 'YYYY-MM-DD HH:MM:SS').
// Gagal query → 0 (fail-open, jangan ganggu response usage).
func usage24h(accountID int64) int64 {
	var sum int64
	err := db.DB.QueryRow(
		`SELECT COALESCE(SUM(total_tokens),0) FROM logs
		 WHERE account_id = ? AND status = 'success'
		   AND created_at >= datetime('now', '-24 hours')`,
		accountID,
	).Scan(&sum)
	if err != nil {
		return 0
	}
	return sum
}

// fetchAccountUsage live-fetch kuota satu akun aktif, selalu mengembalikan
// hasil (gagal → fetchedLive:false + error, points tetap).
//
// Urutan sumber (wallet-first):
//  1. Wallet points AutoClaw (agent-assetmgr wallet-instances) — kelas
//     kredensial yang sama dengan check-in, selalu jalan buat akun OAuth.
//  2. GLM Coding Plan (api.z.ai quota/limit) — hanya relevan bila akun
//     punya API-key coding plan; HTTP 200 code:401 dipakai sebagai sinyal
//     "bukan akun coding plan" dan BUKAN dianggap error keras.
func (s *Server) fetchAccountUsage(ctx context.Context, acct db.Account) usageAccountResult {
	res := usageAccountResult{
		Name:        acct.Name,
		Plan:        "",
		Quotas:      map[string]Quota{},
		Points:      acct.Points,
		FetchedLive: false,
	}
	if acct.AccessToken == "" {
		res.Error = "no access token"
		return res
	}

	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	// 1) Wallet points AutoClaw — sumber utama.
	if balance, err := s.cl.WalletBalance(cctx, acct.AccessToken); err == nil {
		res.Points = balance
		res.Plan = "AutoClaw"
		used24h := usage24h(acct.ID)
		denom := used24h + int64(balance)
		pct := 100.0
		if denom > 0 {
			pct = float64(balance) / float64(denom) * 100
		}
		res.Quotas = map[string]Quota{
			// Bar Quota Tracker: kiri = burn 24 jam (dari log sidecar),
			// kanan = burn + saldo sekarang, persen = proporsi saldo.
			// Points tidak punya window reset, jadi bar ini nyatain
			// "seberapa terpakai" akun dalam sehari terakhir.
			"Points": {
				Used:                float64(used24h),
				Total:               float64(denom),
				Remaining:           float64(balance),
				RemainingPercentage: pct,
				ResetAt:             nil,
				Unlimited:           false,
			},
		}
		res.FetchedLive = true
		return res
	}

	// 2) Fallback GLM Coding Plan (API-key class).
	body, err := s.cl.UsageQuota(cctx, acct.AccessToken)
	if err != nil {
		res.Error = err.Error()
		return res
	}

	plan, quotas, err := parseGlmLimits(body)
	if err != nil {
		// api.z.ai mengembalikan HTTP 200 + code:401 di body saat token
		// bukan coding-plan token — coba refresh token sekali lalu fetch
		// ulang (self-healing). Kalau tetap 401, ini memang bukan akun
		// coding plan: jadikan catatan, bukan error keras.
		bodyStr := string(body)
		if strings.Contains(bodyStr, "\"code\":401") || strings.Contains(bodyStr, "token expired") {
			if _, refErr := s.refreshToken(&acct); refErr == nil {
				_ = db.UpdateAccount(&acct)
				body2, err2 := s.cl.UsageQuota(cctx, acct.AccessToken)
				if err2 == nil {
					if plan2, quotas2, perr := parseGlmLimits(body2); perr == nil {
						res.Plan = plan2
						res.Quotas = quotas2
						res.FetchedLive = true
						return res
					}
				}
			}
			res.Error = "bukan akun GLM Coding Plan (quota/limit code:401); wallet AutoClaw juga gagal"
			return res
		}
		// Sertakan cuplikan body biar shape tak terduga dari api.z.ai
		// bisa didiagnosis dari error message sendirian.
		snippet := body
		if len(snippet) > 300 {
			snippet = snippet[:300]
		}
		res.Error = fmt.Errorf("%s — body: %s", err, snippet).Error()
		return res
	}
	res.Plan = plan
	res.Quotas = quotas
	res.FetchedLive = true
	return res
}

// handleUsage menangani GET /v1/usage.
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	accounts, _ := db.ListAccounts()

	results := make([]usageAccountResult, 0, len(accounts))
	sem := make(chan struct{}, 4) // worker pool max 4
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i := range accounts {
		acct := accounts[i]
		if !acct.Active || acct.AccessToken == "" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res := s.fetchAccountUsage(r.Context(), acct)
			mu.Lock()
			results = append(results, res)
			mu.Unlock()
		}()
	}
	wg.Wait()

	// Urutan stabil by name biar output UI konsisten antar-poll.
	sort.Slice(results, func(a, b int) bool { return results[a].Name < results[b].Name })

	writeJSON(w, 200, map[string]any{
		"accounts":    results,
		"generatedAt": time.Now().UTC().Format(time.RFC3339),
	})
}

// ── Parsing semantik GLM (pure, tanpa network) ──────────────────────

// glmLimit satu entri dari json.data.limits[].
// Percentage any: Z.ai kadang mengirim number, kadang string numerik —
// ditangani toleran di pctAny (cf. Number() di open-sse glm.js).
type glmLimit struct {
	Type          string `json:"type"`
	Percentage    any    `json:"percentage"`
	NextResetTime int64  `json:"nextResetTime"`
	Unit          int    `json:"unit"`
	Number        int    `json:"number"`
}

// pctAny mengonversi percentage (float64 ATAU string numerik) ke float64;
// ok=false kalau tidak valid → limit di-skip, bukan gagal seluruh payload.
func pctAny(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	default:
		return 0, false
	}
}

// glmUsageData adalah json.data dari respons quota/limit.
type glmUsageData struct {
	Level  string     `json:"level"`
	Limits []glmLimit `json:"limits"`
}

// parseGlmLimits mem-parsing respons GET quota/limit Z.ai jadi
// (plan, quotas). Pure — tidak ada network, mudah di-test table-driven.
//
// Semantik (sejajar open-sse/services/usage/glm.js):
//   - limits[].type TOKENS_LIMIT / CREDIT_LIMIT (lainnya di-skip)
//   - percentage = used %, remaining = 100 - used
//   - nextResetTime = ms epoch → resetAt ISO (null kalau tidak ada)
//   - unit 3 → "Session (<number>h)", unit 6 → "Weekly (7d)",
//     TOKENS_LIMIT lain → "Tokens", CREDIT_LIMIT lain → "Limit (<number>)"
//   - data.level → plan Title case ("Unknown" kalau kosong)
func parseGlmLimits(data []byte) (string, map[string]Quota, error) {
	var root struct {
		Data *glmUsageData `json:"data"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		return "", nil, fmt.Errorf("payload bukan JSON: %v", err)
	}
	if root.Data == nil {
		return "", nil, fmt.Errorf("payload tanpa data")
	}

	quotas := map[string]Quota{}
	for _, lim := range root.Data.Limits {
		if lim.Type != "TOKENS_LIMIT" && lim.Type != "CREDIT_LIMIT" {
			continue
		}
		used, ok := pctAny(lim.Percentage)
		if !ok {
			// percentage tidak valid (bukan number / string numerik) →
			// skip limit ini saja, jangan gagalkan seluruh payload.
			continue
		}
		remaining := 100 - used
		if remaining < 0 {
			remaining = 0
		}
		var key string
		switch {
		case lim.Unit == 3:
			key = fmt.Sprintf("Session (%dh)", lim.Number)
		case lim.Unit == 6:
			key = "Weekly (7d)"
		case lim.Type == "TOKENS_LIMIT":
			key = "Tokens"
		default:
			key = fmt.Sprintf("Limit (%d)", lim.Number)
		}
		q := Quota{
			Used:                used,
			Total:               100,
			Remaining:           remaining,
			RemainingPercentage: remaining,
			Unlimited:           false,
		}
		if lim.NextResetTime > 0 {
			iso := time.UnixMilli(lim.NextResetTime).UTC().Format(time.RFC3339)
			q.ResetAt = &iso
		}
		quotas[key] = q
	}

	plan := "Unknown"
	if lv := strings.TrimSpace(root.Data.Level); lv != "" {
		plan = strings.ToUpper(lv[:1]) + strings.ToLower(lv[1:])
	}
	return plan, quotas, nil
}
