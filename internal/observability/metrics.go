package observability

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics 集中持有网关的核心指标句柄。标签只到 endpoint/model/channel/provider 级，
// 不带 user_id/api_key_id，避免高基数问题（见技术方案 §7.14）。
type Metrics struct {
	RequestsTotal   *prometheus.CounterVec
	RequestDuration *prometheus.HistogramVec
	TTFT            *prometheus.HistogramVec

	UpstreamRequestsTotal *prometheus.CounterVec
	BreakerState          *prometheus.GaugeVec

	ReserveRejectedTotal  *prometheus.CounterVec
	SettleDuration        prometheus.Histogram
	UnsettledReservations prometheus.Gauge

	ChannelMarginRatio prometheus.Gauge
}

func NewMetrics(reg prometheus.Registerer) *Metrics {
	f := promauto.With(reg)
	return &Metrics{
		RequestsTotal: f.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_requests_total",
			Help: "网关处理的请求总数",
		}, []string{"endpoint", "model", "status"}),

		RequestDuration: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gateway_request_duration_seconds",
			Help:    "端到端请求耗时",
			Buckets: prometheus.ExponentialBuckets(0.01, 2, 14),
		}, []string{"endpoint", "model"}),

		TTFT: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gateway_ttft_seconds",
			Help:    "首字节耗时",
			Buckets: prometheus.ExponentialBuckets(0.01, 2, 14),
		}, []string{"model", "channel"}),

		UpstreamRequestsTotal: f.NewCounterVec(prometheus.CounterOpts{
			Name: "upstream_requests_total",
			Help: "对上游渠道发起的请求总数",
		}, []string{"provider", "channel", "error_class"}),

		BreakerState: f.NewGaugeVec(prometheus.GaugeOpts{
			Name: "breaker_state",
			Help: "熔断器状态：0 closed / 1 half-open / 2 open",
		}, []string{"channel"}),

		ReserveRejectedTotal: f.NewCounterVec(prometheus.CounterOpts{
			Name: "billing_reserve_rejected_total",
			Help: "预扣失败次数",
		}, []string{"reason"}),

		SettleDuration: f.NewHistogram(prometheus.HistogramOpts{
			Name:    "billing_settle_duration_seconds",
			Help:    "结算耗时",
			Buckets: prometheus.DefBuckets,
		}),

		UnsettledReservations: f.NewGauge(prometheus.GaugeOpts{
			Name: "billing_unsettled_reservations",
			Help: "超时未结算的冻结记录数，正常应为 0",
		}),

		ChannelMarginRatio: f.NewGauge(prometheus.GaugeOpts{
			Name: "channel_margin_ratio",
			Help: "渠道毛利率（占位，按渠道维度上报见 worker）",
		}),
	}
}

// Handler 返回 /metrics 的 http.Handler。
func Handler() http.Handler {
	return promhttp.Handler()
}
