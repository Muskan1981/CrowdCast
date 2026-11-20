package infra

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"time"
)

// ServerRow matches ServerRow in docs/openapi.yaml.
type ServerRow struct {
	Name           string  `json:"name"`
	Zone           string  `json:"zone"`
	Weight         int     `json:"weight"`
	Requests       string  `json:"requests"`
	RequestsPerSec float64 `json:"requests_per_sec"`
	SharePct       float64 `json:"share_pct"`
	CPU            int     `json:"cpu"`
	Memory         int     `json:"memory"`
	Status         string  `json:"status"`
	Color          string  `json:"color"`
}

// FleetMetrics are the totals across the fleet.
type FleetMetrics struct {
	TotalRequests     int64   `json:"total_requests"`
	AvgLatencyMs      float64 `json:"avg_latency_ms"`
	ErrorRatePct      float64 `json:"error_rate_pct"`
	ActiveConnections int64   `json:"active_connections"`
}

// MonitoringService is one row of the "Monitoring services" panel.
type MonitoringService struct {
	Name     string `json:"name"`
	Subtitle string `json:"subtitle"`
	Status   string `json:"status"`
	Good     bool   `json:"good"`
}

// FleetView is the response body of GET /infra/nodes (InfraNodesResponse).
type FleetView struct {
	FleetStatus        string              `json:"fleet_status"`
	Degraded           bool                `json:"degraded"`
	Metrics            FleetMetrics        `json:"metrics"`
	ServerRows         []ServerRow         `json:"server_rows"`
	MonitoringServices []MonitoringService `json:"monitoring_services"`
}

// BuildFleet merges the expected node list with live heartbeats. A node with no
// live record is shown as Offline with zeros rather than being dropped.
func BuildFleet(expected []Expected, live map[string]NodeRecord) (rows []ServerRow, totals FleetMetrics, status string, degraded bool) {
	serving := 0
	allHealthy := true
	rows = make([]ServerRow, 0, len(expected))

	var fleetRPS float64
	for _, e := range expected {
		rec, ok := live[e.Name]
		row := ServerRow{Name: e.Name, Zone: e.Zone, Weight: e.Weight, Status: "Offline", Color: e.Color}
		if ok {
			row.Zone = firstNonEmpty(rec.Zone, e.Zone)
			row.CPU = int(math.Round(rec.CPU))
			row.Memory = int(math.Round(rec.Memory))
			row.Status = statusLabel(rec.Health)
			totals.ActiveConnections += rec.Connections
			// Only a node that is serving reports a rate. A failed or draining node
			// still counts the requests it refuses (503s), so its rate is zeroed.
			if rec.Health == HealthHealthy || rec.Health == HealthDegraded {
				serving++
				row.RequestsPerSec = rec.RequestsPerSec
				fleetRPS += rec.RequestsPerSec
				totals.TotalRequests += int64(math.Round(rec.RequestsPerSec))
			}
		}
		row.Requests = commas(int64(math.Round(row.RequestsPerSec)))
		if row.Status != "Healthy" {
			allHealthy = false
		}
		rows = append(rows, row)
	}

	// Each serving node's share of the live fleet rate. With no traffic at all,
	// idleShare decides what the table shows.
	for i := range rows {
		if fleetRPS > 0 {
			rows[i].SharePct = rows[i].RequestsPerSec / fleetRPS * 100
		} else {
			rows[i].SharePct = idleShare(expected[i], rows[i].Status)
		}
	}

	switch {
	case allHealthy:
		status = "Operational"
	case serving == 0:
		status = "Outage"
	default:
		status = "Degraded"
	}
	return rows, totals, status, status != "Operational"
}

// idleShare is the traffic share shown for a node when the fleet is serving no
// requests at all. A Healthy node shows its configured weight, so the table still
// shows the intended split. Any other node shows 0, so an outage never looks
// like traffic is flowing.
func idleShare(e Expected, status string) float64 {
	if status == "Healthy" {
		return float64(e.Weight)
	}
	return 0
}

func statusLabel(health string) string {
	switch health {
	case HealthHealthy:
		return "Healthy"
	case HealthDegraded:
		return "Degraded"
	case HealthDraining:
		return "Draining"
	case HealthFailed:
		return "Failed"
	default:
		return "Offline"
	}
}

// Querier is the slice of the Prometheus client the fleet view needs.
type Querier interface {
	QueryInstant(ctx context.Context, query string) (float64, error)
}

const (
	qAvgLatency = `1000 * sum(rate(http_request_duration_seconds_sum{route!="unmatched",route!="/metrics"}[1m])) / sum(rate(http_request_duration_seconds_count{route!="unmatched",route!="/metrics"}[1m]))`
	qErrorRate  = `100 * sum(rate(http_requests_total{status=~"5..",route!="unmatched"}[1m])) / sum(rate(http_requests_total{route!="unmatched"}[1m]))`
)

// FleetMetricsFromProm fills latency and error rate from Prometheus. If the
// query fails the values stay zero; the rest of the page still renders.
func FleetMetricsFromProm(ctx context.Context, q Querier, totals FleetMetrics) FleetMetrics {
	if q == nil {
		return totals
	}
	if v, err := q.QueryInstant(ctx, qAvgLatency); err == nil {
		totals.AvgLatencyMs = v
	}
	if v, err := q.QueryInstant(ctx, qErrorRate); err == nil {
		totals.ErrorRatePct = v
	}
	return totals
}

// MonitorCheck is a reachability probe for one monitoring service.
type MonitorCheck struct {
	Name     string
	Subtitle string
	URL      string
}

// CheckMonitors probes each service with a short timeout. A failed probe is
// reported as Down, not as an error for the whole response.
func CheckMonitors(ctx context.Context, checks []MonitorCheck, timeout time.Duration) []MonitoringService {
	out := make([]MonitoringService, 0, len(checks))
	client := &http.Client{Timeout: timeout}
	for _, c := range checks {
		status, good := "Down", false
		if c.URL != "" {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
			if err == nil {
				if resp, err := client.Do(req); err == nil {
					_ = resp.Body.Close()
					if resp.StatusCode < 400 {
						status, good = "Up", true
					}
				}
			}
		}
		out = append(out, MonitoringService{Name: c.Name, Subtitle: c.Subtitle, Status: status, Good: good})
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// commas renders 6420 as "6,420".
func commas(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := fmt.Sprintf("%d", n)
	out := make([]byte, 0, len(s)+len(s)/3)
	for i := 0; i < len(s); i++ {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}
