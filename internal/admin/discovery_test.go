package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// modelsFixture 是手工构造的、遵循 OpenAI GET /models 响应形状的 JSON——
// 本包的测试从不对真实外部服务发请求（见 internal/pricesync 包的同款约束），
// 这里用 httptest.Server 充当一个"假 SiliconFlow"。
const modelsFixture = `{
  "object": "list",
  "data": [
    {"id": "deepseek-ai/DeepSeek-V3", "object": "model", "owned_by": "deepseek-ai"},
    {"id": "Qwen/Qwen2.5-72B-Instruct", "object": "model", "owned_by": "qwen"}
  ]
}`

// seedOpenAIProviderAccount 建一个 protocol=openai 的 provider + provider_account，
// base_url 指向调用方传入的假上游地址。
func seedOpenAIProviderAccount(t *testing.T, s *Service, baseURL string) int64 {
	t.Helper()
	p, err := s.CreateProvider(context.Background(), CreateProviderInput{
		Code: uniqueCode(t), Name: "test-provider", Protocol: "openai",
	})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	pa, err := s.CreateProviderAccount(context.Background(), CreateProviderAccountInput{
		ProviderID: p.ID, Name: "test-account", BaseURL: baseURL,
	})
	if err != nil {
		t.Fatalf("CreateProviderAccount: %v", err)
	}
	return pa.ID
}

func TestListUpstreamModels_HappyPath(t *testing.T) {
	pool := testPool(t)
	s := newService(t, pool)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer fake-secret" {
			t.Errorf("Authorization header = %q, want Bearer fake-secret", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(modelsFixture))
	}))
	defer srv.Close()

	paID := seedOpenAIProviderAccount(t, s, srv.URL)
	if _, err := s.AddProviderKey(context.Background(), AddProviderKeyInput{ProviderAccountID: paID, Secret: "fake-secret", Weight: 100}); err != nil {
		t.Fatalf("AddProviderKey: %v", err)
	}

	got, err := s.ListUpstreamModels(context.Background(), paID)
	if err != nil {
		t.Fatalf("ListUpstreamModels: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d models, want 2: %+v", len(got), got)
	}
	if got[0].ID != "deepseek-ai/DeepSeek-V3" || got[0].OwnedBy != "deepseek-ai" {
		t.Errorf("got[0] = %+v, want id=deepseek-ai/DeepSeek-V3 owned_by=deepseek-ai", got[0])
	}
	if got[1].ID != "Qwen/Qwen2.5-72B-Instruct" {
		t.Errorf("got[1] = %+v, want id=Qwen/Qwen2.5-72B-Instruct", got[1])
	}
}

func TestListUpstreamModels_ProviderAccountNotFound(t *testing.T) {
	pool := testPool(t)
	s := newService(t, pool)

	_, err := s.ListUpstreamModels(context.Background(), -1)
	if !errors.Is(err, ErrProviderAccountNotFound) {
		t.Errorf("err = %v, want ErrProviderAccountNotFound", err)
	}
}

func TestListUpstreamModels_NonOpenAIProtocol_Rejected(t *testing.T) {
	pool := testPool(t)
	s := newService(t, pool)

	p, err := s.CreateProvider(context.Background(), CreateProviderInput{Code: uniqueCode(t), Name: "anthropic-test", Protocol: "anthropic"})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	pa, err := s.CreateProviderAccount(context.Background(), CreateProviderAccountInput{ProviderID: p.ID, Name: "acc", BaseURL: "https://example.invalid"})
	if err != nil {
		t.Fatalf("CreateProviderAccount: %v", err)
	}

	_, err = s.ListUpstreamModels(context.Background(), pa.ID)
	if err == nil {
		t.Fatal("expected an error for a non-openai protocol provider account")
	}
	if errors.Is(err, ErrProviderAccountNotFound) || errors.Is(err, ErrNoActiveProviderKey) || errors.Is(err, ErrUpstreamUnavailable) {
		t.Errorf("err = %v, want a plain protocol-not-supported error, not one of the sentinel errors", err)
	}
}

func TestListUpstreamModels_NoActiveKey(t *testing.T) {
	pool := testPool(t)
	s := newService(t, pool)

	paID := seedOpenAIProviderAccount(t, s, "https://example.invalid")

	_, err := s.ListUpstreamModels(context.Background(), paID)
	if !errors.Is(err, ErrNoActiveProviderKey) {
		t.Errorf("err = %v, want ErrNoActiveProviderKey", err)
	}
}

func TestListUpstreamModels_UpstreamNon200_WrapsUnavailable(t *testing.T) {
	pool := testPool(t)
	s := newService(t, pool)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid api key"}`))
	}))
	defer srv.Close()

	paID := seedOpenAIProviderAccount(t, s, srv.URL)
	if _, err := s.AddProviderKey(context.Background(), AddProviderKeyInput{ProviderAccountID: paID, Secret: "bad-secret", Weight: 100}); err != nil {
		t.Fatalf("AddProviderKey: %v", err)
	}

	_, err := s.ListUpstreamModels(context.Background(), paID)
	if !errors.Is(err, ErrUpstreamUnavailable) {
		t.Errorf("err = %v, want ErrUpstreamUnavailable", err)
	}
}

func TestListUpstreamModels_MalformedResponse_WrapsUnavailable(t *testing.T) {
	pool := testPool(t)
	s := newService(t, pool)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	paID := seedOpenAIProviderAccount(t, s, srv.URL)
	if _, err := s.AddProviderKey(context.Background(), AddProviderKeyInput{ProviderAccountID: paID, Secret: "fake-secret", Weight: 100}); err != nil {
		t.Fatalf("AddProviderKey: %v", err)
	}

	_, err := s.ListUpstreamModels(context.Background(), paID)
	if !errors.Is(err, ErrUpstreamUnavailable) {
		t.Errorf("err = %v, want ErrUpstreamUnavailable", err)
	}
}

// TestListUpstreamModels_Dialect：方言的 list_paths 逐个拉取并按 ID 去重，附加请求头，
// auth.validation=url 时先校验 Key（/models 不鉴权的供应商，无效 Key 不能"列出模型"）。
func TestListUpstreamModels_Dialect(t *testing.T) {
	pool := testPool(t)
	s := newService(t, pool)

	keyOK := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Title") != "t" {
			t.Errorf("%s: X-Title = %q", r.URL.Path, r.Header.Get("X-Title"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/key":
			if !keyOK {
				w.WriteHeader(http.StatusUnauthorized)
			}
			_, _ = w.Write([]byte(`{}`))
		case "/api/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"a"},{"id":"b"}]}`))
		case "/api/v1/embeddings/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"b"},{"id":"e"}]}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	p, err := s.CreateProvider(context.Background(), CreateProviderInput{Code: uniqueCode(t), Name: "test-provider", Protocol: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	pa, err := s.CreateProviderAccount(context.Background(), CreateProviderAccountInput{
		ProviderID: p.ID, Name: "test-account", BaseURL: srv.URL + "/api/v1",
		Dialect: []byte(`{"transport":{"extra_headers":{"X-Title":"t"}},"auth":{"validation":{"method":"url","url":"{origin}/api/v1/key"}},"catalog":{"list_paths":["/models","/embeddings/models"]}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddProviderKey(context.Background(), AddProviderKeyInput{ProviderAccountID: pa.ID, Secret: "k", Weight: 100}); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListUpstreamModels(context.Background(), pa.ID)
	if err != nil || len(got) != 3 || got[0].ID != "a" || got[2].ID != "e" {
		t.Fatalf("got %+v, %v", got, err)
	}
	keyOK = false
	if _, err := s.ListUpstreamModels(context.Background(), pa.ID); !errors.Is(err, ErrUpstreamUnavailable) {
		t.Errorf("invalid key: err = %v, want ErrUpstreamUnavailable", err)
	}
}
