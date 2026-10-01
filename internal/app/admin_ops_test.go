// 运营后台收尾项的端到端测试：渠道健康（G9）、吊销 Key 清冷却、健康事件落库、
// base_url 修改后 24 小时的超级管理员限制、If-Match 强制、价格版本的 created_by。
package app_test

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/app"
	"github.com/WALLE-AI/uFreeTokens/internal/health"
)

func TestOps_ChannelHealthAndCooldownClearing(t *testing.T) {
	rdb := testRedis(t)
	ac, pool, done := newAdminTestServerWithDeps(t, true, func(d *app.AdminDeps) { d.Redis = rdb })
	defer done()
	f := newB2Fixture(t, ac)
	ctx := t.Context()

	var keyID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM provider_keys WHERE provider_account_id = $1`, f.accountID).Scan(&keyID); err != nil {
		t.Fatalf("find key: %v", err)
	}
	// 模拟网关：渠道熔断打开、唯一的 Key 进入冷却。
	raw, _ := json.Marshal(health.BreakerState{State: "open", Since: time.Now(), Gateway: "test"})
	rdb.Set(ctx, health.BreakerRedisKey(f.channelID), raw, time.Minute)
	rdb.Set(ctx, fmt.Sprintf("uft:cooldown:key:%d", keyID), 1, time.Minute)
	t.Cleanup(func() { rdb.Del(t.Context(), health.BreakerRedisKey(f.channelID)) })

	var rep admin.ChannelHealthReport
	ac.get("/channels/health", &rep)
	var mine *admin.ChannelHealth
	for i := range rep.Channels {
		if rep.Channels[i].ChannelID == f.channelID {
			mine = &rep.Channels[i]
		}
	}
	if !rep.RuntimeStateKnown || mine == nil {
		t.Fatalf("report = %+v, want runtime state and our channel", rep)
	}
	if mine.BreakerState != "open" || mine.KeysTotal != 1 || mine.KeysOnCooldown != 1 || mine.Status != "down" {
		t.Errorf("channel health = %+v, want open breaker, 1/1 keys cooling, status down", *mine)
	}
	if rep.Thresholds.P95Ms != 5000 {
		t.Errorf("thresholds = %+v, want server-provided defaults", rep.Thresholds)
	}

	// 吊销 Key 后冷却记录被清除。
	ac.post(fmt.Sprintf("/provider-keys/%d/revoke", keyID), nil, nil)
	if n, _ := rdb.Exists(ctx, fmt.Sprintf("uft:cooldown:key:%d", keyID)).Result(); n != 0 {
		t.Error("cooldown key still present after revoke")
	}
}

func TestOps_HealthEventsArePersisted(t *testing.T) {
	rdb := testRedis(t)
	pool := testPool(t)
	ctx := t.Context()
	marker := time.Now().UnixNano() % 1_000_000_000
	reg := health.NewRegistry(rdb, health.DefaultBreakerSettings())
	reg.CooldownKey(ctx, marker, 30*time.Second)
	time.Sleep(200 * time.Millisecond) // 事件是异步推送的
	t.Cleanup(func() { rdb.Del(t.Context(), fmt.Sprintf("uft:cooldown:key:%d", marker)) })

	if _, err := health.PersistEvents(ctx, pool, rdb); err != nil {
		t.Fatalf("PersistEvents: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM channel_health_events WHERE provider_key_id = $1 AND event = 'key_cooldown'`, marker).Scan(&n); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if n != 1 {
		t.Errorf("persisted cooldown events = %d, want 1", n)
	}
}

func TestOps_UpstreamModelsLockedAfterBaseURLChange(t *testing.T) {
	ac, pool, done := newAdminTestServerWithPool(t, true)
	defer done()
	f := newB2Fixture(t, ac)
	ac.loginAs(pool, "运营改地址", "operator")
	status, body := ac.do(http.MethodPatch, fmt.Sprintf("/provider-accounts/%d", f.accountID), map[string]any{"base_url": "https://b2-new.example"}, nil)
	if status != http.StatusOK {
		t.Fatalf("change base_url: %d %s", status, body)
	}
	// operator 有 provider_key:write，但地址刚改过：24 小时内只有超级管理员能用密钥请求上游。
	if status, _ := ac.do(http.MethodGet, fmt.Sprintf("/provider-accounts/%d/upstream-models", f.accountID), nil, nil); status != http.StatusForbidden {
		t.Errorf("upstream-models right after base_url change: status = %d, want 403", status)
	}
}

func TestOps_IfMatchRequiredAndCreatedBy(t *testing.T) {
	ac, pool, done := newAdminTestServerWithDeps(t, true, func(d *app.AdminDeps) { d.RequireIfMatch = true })
	defer done()
	f := newB2Fixture(t, ac)
	path := fmt.Sprintf("/channels/%d", f.channelID)
	if status, _ := ac.do(http.MethodPatch, path, map[string]any{"weight": 10}, nil); status != http.StatusPreconditionRequired {
		t.Fatalf("PATCH without If-Match: status = %d, want 428", status)
	}
	req, _ := http.NewRequest(http.MethodGet, ac.baseURL+path, nil)
	req.Header.Set("Authorization", "Bearer "+ac.authToken())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if status, body := ac.do(http.MethodPatch, path, map[string]any{"weight": 10}, map[string]string{"If-Match": resp.Header.Get("ETag")}); status != http.StatusOK {
		t.Fatalf("PATCH with If-Match: %d %s", status, body)
	}

	// 价格版本记录发布人（此前 created_by 始终为 NULL）。
	me := ac.loginAs(pool, "定价员", "pricing")
	var out struct {
		PriceBookID int64 `json:"price_book_id"`
	}
	ac.post(fmt.Sprintf("/virtual-models/%d/sell-price", f.vmID), map[string]any{"components": []map[string]any{
		{"meter": "input", "unit": "per_1m_tokens", "unit_price": "11"},
	}}, &out)
	var createdBy *int64
	if err := pool.QueryRow(t.Context(), `SELECT created_by FROM price_books WHERE id = $1`, out.PriceBookID).Scan(&createdBy); err != nil {
		t.Fatalf("query book: %v", err)
	}
	if createdBy == nil || *createdBy != me.ID {
		t.Errorf("created_by = %v, want %d", createdBy, me.ID)
	}
}

// totpNow 按 RFC 6238 计算当前（偏移 offset 个时间片）的 6 位验证码。
func totpNow(t *testing.T, secretB32 string, offset int64) string {
	t.Helper()
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secretB32)
	if err != nil {
		t.Fatalf("decode secret: %v", err)
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(time.Now().Unix()/30+offset))
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(sum[off:off+4])&0x7fffffff)%1_000_000)
}

func TestOps_TOTPEnrollmentAndLogin(t *testing.T) {
	ac, pool, done := newAdminTestServerWithPool(t, false)
	defer done()
	me := ac.loginAs(pool, "两步验证", "support")

	var setup struct {
		Secret     string `json:"secret"`
		OTPAuthURL string `json:"otpauth_url"`
	}
	ac.post("/auth/totp/setup", nil, &setup)
	if setup.Secret == "" || !strings.HasPrefix(setup.OTPAuthURL, "otpauth://totp/") {
		t.Fatalf("setup = %+v", setup)
	}
	if status, _ := ac.do(http.MethodPost, "/auth/totp/enable", map[string]any{"code": "000000"}, nil); status != http.StatusBadRequest {
		t.Errorf("enable with wrong code: status = %d, want 400", status)
	}
	if status, body := ac.do(http.MethodPost, "/auth/totp/enable", map[string]any{"code": totpNow(t, setup.Secret, -1)}, nil); status != http.StatusNoContent {
		t.Fatalf("enable: %d %s", status, body)
	}

	login := func(code string) (int, string) {
		ac.token = ""
		body := map[string]any{"email": me.Email, "password": me.Password}
		if code != "" {
			body["totp_code"] = code
		}
		status, raw := ac.do(http.MethodPost, "/auth/login", body, nil)
		var e struct {
			Error struct{ Code string } `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		return status, e.Error.Code
	}
	if status, code := login(""); status != http.StatusUnauthorized || code != "totp_required" {
		t.Errorf("login without code: %d %s, want 401 totp_required", status, code)
	}
	current := totpNow(t, setup.Secret, 0)
	if status, _ := login(current); status != http.StatusOK {
		t.Errorf("login with code: status = %d, want 200", status)
	}
	// 同一个验证码不能重放。
	if status, code := login(current); status != http.StatusUnauthorized || code != "invalid_totp" {
		t.Errorf("replayed code: %d %s, want 401 invalid_totp", status, code)
	}
}
