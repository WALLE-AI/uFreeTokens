package app

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/WALLE-AI/uFreeTokens/internal/adapter"
	"github.com/WALLE-AI/uFreeTokens/internal/dialect"
	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
)

// 供应商方言的后台接口（多供应商接口统一技术实施方案 §3.3）。

type metaDialectPresetsResponse struct {
	Data []dialectPreset `json:"data"`
}

type metaCodecsResponse struct {
	Codecs     []string `json:"codecs"`
	Transforms []string `json:"transforms"`
}

type setDialectRequest struct {
	Dialect json.RawMessage `json:"dialect"`
}

type dialectPreset struct {
	Name    string          `json:"name"`
	Notes   string          `json:"notes"`
	Dialect json.RawMessage `json:"dialect"`
}

// metaDialectPresets 是 GET /meta/dialect-presets：内置方言预设（名称、说明、原始配置）。
func (h *adminHandlers) metaDialectPresets(w http.ResponseWriter, r *http.Request) {
	out := []dialectPreset{}
	for _, name := range dialect.PresetNames() {
		raw, _ := dialect.PresetJSON(name)
		d, _ := dialect.Load(name, nil)
		p := dialectPreset{Name: name, Dialect: raw}
		if d != nil {
			p.Notes = d.Notes
		}
		out = append(out, p)
	}
	httpx.WriteJSON(w, http.StatusOK, metaDialectPresetsResponse{Data: out})
}

// metaCodecs 是 GET /meta/codecs：可以在方言里引用的 codec 与具名变换。
func (h *adminHandlers) metaCodecs(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, metaCodecsResponse{Codecs: adapter.CodecNames(), Transforms: dialect.KnownTransforms})
}

func (h *adminHandlers) getAccountDialect(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "providerAccountID", "provider account")
	if !ok {
		return
	}
	d, err := h.svc.GetAccountDialect(r.Context(), id)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

// setAccountDialect 是 PUT /provider-accounts/{id}/dialect，请求体 {"dialect": {...} | null}。
// 网关在下一次目录快照刷新（默认 10 秒）后生效。
func (h *adminHandlers) setAccountDialect(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "providerAccountID", "provider account")
	if !ok {
		return
	}
	var body setDialectRequest
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	_, err := audited(h, r, func(ctx context.Context) (struct{}, auditEntry, error) {
		before, after, err := h.svc.SetAccountDialect(ctx, id, body.Dialect)
		if err != nil {
			return struct{}{}, auditEntry{}, err
		}
		return struct{}{}, auditEntry{"provider_account.dialect", "provider_account", idStr(id), before, after}, nil
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	d, err := h.svc.GetAccountDialect(r.Context(), id)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}
