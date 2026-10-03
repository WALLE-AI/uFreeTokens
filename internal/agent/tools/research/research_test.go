package research

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/WALLE-AI/uFreeTokens/internal/agent/kernel"
	"github.com/WALLE-AI/uFreeTokens/internal/datasync"
)

type fakeFetcher struct {
	body  string
	calls int
}

func (f *fakeFetcher) Get(_ context.Context, _ string, _ datasync.GetOptions) (*datasync.Response, error) {
	f.calls++
	if f.body == "" {
		return nil, errors.New("datasync: status 404")
	}
	return &datasync.Response{Body: []byte(f.body)}, nil
}

func TestAllowedAndRegistrableDomain(t *testing.T) {
	if !Allowed("platform.deepseek.com", []string{"deepseek.com"}) || Allowed("deepseek.com.evil.io", []string{"deepseek.com"}) {
		t.Error("Allowed suffix matching wrong")
	}
	cases := map[string]string{"api.deepseek.com": "deepseek.com", "open.bigmodel.cn": "bigmodel.cn", "x.aliyun.com.cn": "aliyun.com.cn", "localhost": "localhost"}
	for in, want := range cases {
		if got := RegistrableDomain(in); got != want {
			t.Errorf("RegistrableDomain(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestFetchPage(t *testing.T) {
	f := &fakeFetcher{body: "<html><body><p>限时免费活动</p><p>无关内容</p></body></html>"}
	tool := &FetchPage{Env: f, AllowDomains: func(context.Context) ([]string, error) { return []string{"example.com"}, nil }}
	env := &kernel.Env{Pages: kernel.NewPageCache()}

	res, err := tool.Call(context.Background(), env, json.RawMessage(`{"url":"https://evil.io/x"}`))
	if err != nil || !res.IsError {
		t.Fatalf("non-allowlisted domain: %+v %v", res, err)
	}
	res, _ = tool.Call(context.Background(), env, json.RawMessage(`{"url":"https://www.example.com/p"}`))
	if res.IsError || !strings.Contains(res.Content.(map[string]any)["text"].(string), "限时免费活动") {
		t.Fatalf("fetch = %+v", res)
	}
	if _, ok := env.Pages.Get("https://www.example.com/p"); !ok {
		t.Error("page not recorded for evidence checks")
	}
	// 同一运行再次抓取：命中缓存且不再送正文。
	res, _ = tool.Call(context.Background(), env, json.RawMessage(`{"url":"https://www.example.com/p"}`))
	if f.calls != 1 || res.Content.(map[string]any)["unchanged"] != true {
		t.Errorf("second fetch: calls=%d res=%+v", f.calls, res)
	}
}
