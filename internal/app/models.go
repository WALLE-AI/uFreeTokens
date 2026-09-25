package app

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/WALLE-AI/uFreeTokens/internal/auth"
	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
)

// openAIModel 是 GET /v1/models 的单条响应，遵循 OpenAI 兼容格式。
type openAIModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// listModelsHandler 直接查询 virtual_models（Phase1 尚无 Catalog 内存快照，
// 见技术方案 §7.3；后续应改为从快照读取，避免热路径查库）。
// 只返回该账户 tier 可见、状态为 active 的虚拟模型。
func listModelsHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.FromContext(r.Context())
		if !ok {
			httpx.WriteError(w, r, http.StatusUnauthorized, "invalid_api_key", "Invalid API key.")
			return
		}

		rows, err := pool.Query(r.Context(),
			`SELECT name FROM virtual_models WHERE status = 'active' AND $1 = ANY(visible_tiers) ORDER BY name`,
			principal.AccountTier,
		)
		if err != nil {
			httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Failed to list models.")
			return
		}
		defer rows.Close()

		models := make([]openAIModel, 0, 16)
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Failed to list models.")
				return
			}
			models = append(models, openAIModel{ID: name, Object: "model", OwnedBy: "ufreetokens"})
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"object": "list",
			"data":   models,
		})
	}
}
