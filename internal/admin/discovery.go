package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/secretbox"
)

// UpstreamModel 是从上游 GET /models 接口拉到的一条模型摘要，只保留"选哪些
// 模型导入"需要用到的字段——各家返回的字段差异很大（SiliconFlow/DeepSeek/
// OpenAI 都遵循 {"data":[{"id":...}]} 这个外层形状，但 owned_by 之类的字段
// 不是所有厂商都填），缺失就留空，不强求。
type UpstreamModel struct {
	ID      string `json:"id"`
	OwnedBy string `json:"owned_by"`
}

var (
	ErrProviderAccountNotFound = errors.New("admin: provider account not found")
	ErrNoActiveProviderKey     = errors.New("admin: provider account has no active key")
	ErrUpstreamUnavailable     = errors.New("admin: upstream models endpoint unavailable")
)

// discoveryHTTPClient 是调用上游 /models 接口用的默认客户端——列模型接口通常
// 很快，但公网请求还是需要一个上限，不能无限等。
var discoveryHTTPClient = &http.Client{Timeout: 15 * time.Second}

// ListUpstreamModels 调用某个 provider_account 的 base_url + "/models"
// （OpenAI 兼容协议的标准列模型接口），用它名下一条 active 的 Key 认证，
// 返回上游汇报的模型列表。这是"选一个像 SiliconFlow 这样的供应商、录入
// API Key 之后，真的问一遍它支持哪些模型"这个操作的后端实现——只对 openai
// 协议的 provider 有意义（SiliconFlow/DeepSeek/火山方舟/百炼等都实现了这个
// 接口；anthropic/gemini 协议没有标准等价物，不是这里的缺陷）。
func (s *Service) ListUpstreamModels(ctx context.Context, providerAccountID int64) ([]UpstreamModel, error) {
	var baseURL, protocol string
	if err := s.pool.QueryRow(ctx,
		`SELECT pa.base_url, p.protocol FROM provider_accounts pa JOIN providers p ON p.id = pa.provider_id WHERE pa.id = $1`,
		providerAccountID,
	).Scan(&baseURL, &protocol); err != nil {
		if isNoRows(err) {
			return nil, ErrProviderAccountNotFound
		}
		return nil, fmt.Errorf("admin: load provider_account: %w", err)
	}
	if protocol != "openai" {
		return nil, fmt.Errorf("admin: %s 协议的上游没有标准 GET /models 接口，模型发现只支持 openai 协议", protocol)
	}

	var ciphertext, dek []byte
	if err := s.pool.QueryRow(ctx,
		`SELECT secret_ciphertext, secret_dek_wrapped FROM provider_keys
		 WHERE provider_account_id = $1 AND status = 'active'
		 ORDER BY weight DESC, created_at LIMIT 1`,
		providerAccountID,
	).Scan(&ciphertext, &dek); err != nil {
		if isNoRows(err) {
			return nil, ErrNoActiveProviderKey
		}
		return nil, fmt.Errorf("admin: load provider_key: %w", err)
	}
	secret, err := s.box.Open(&secretbox.Sealed{Ciphertext: ciphertext, WrappedDEK: dek})
	if err != nil {
		return nil, fmt.Errorf("admin: decrypt provider_key: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("admin: build upstream request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+secret)

	resp, err := discoveryHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("admin: call upstream /models: %w: %w", ErrUpstreamUnavailable, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("admin: read upstream /models response: %w: %w", ErrUpstreamUnavailable, err)
	}
	if resp.StatusCode != http.StatusOK {
		snippet := body
		if len(snippet) > 500 {
			snippet = snippet[:500]
		}
		return nil, fmt.Errorf("admin: upstream /models returned status %d (%s): %w", resp.StatusCode, snippet, ErrUpstreamUnavailable)
	}

	var parsed struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("admin: decode upstream /models response: %w: %w", ErrUpstreamUnavailable, err)
	}

	out := make([]UpstreamModel, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		out = append(out, UpstreamModel{ID: m.ID, OwnedBy: m.OwnedBy})
	}
	return out, nil
}
