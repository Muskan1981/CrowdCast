package stats

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// PromClient is a minimal typed client for the Prometheus HTTP API. It only
// implements the two calls the stats package needs.
type PromClient struct {
	base string
	http *http.Client
}

// Sample is one point of a range query result.
type Sample struct {
	T time.Time
	V float64
}

// NewPromClient builds a client for baseURL (for example http://prometheus:9090).
// timeout bounds every request, including the body read.
func NewPromClient(baseURL string, timeout time.Duration) *PromClient {
	return &PromClient{
		base: strings.TrimRight(baseURL, "/"),
		http: &http.Client{Timeout: timeout},
	}
}

// promEnvelope is the common wrapper around every Prometheus API response.
type promEnvelope struct {
	Status    string `json:"status"`
	Error     string `json:"error"`
	ErrorType string `json:"errorType"`
	Data      struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []any             `json:"value"`  // vector: [ts, "v"]
			Values [][]any           `json:"values"` // matrix: [[ts, "v"], ...]
		} `json:"result"`
	} `json:"data"`
}

// QueryInstant runs an instant query and returns the sum across all series.
// An empty result is 0, not an error: no traffic yet is a valid answer.
func (c *PromClient) QueryInstant(ctx context.Context, query string) (float64, error) {
	env, err := c.do(ctx, "/api/v1/query", url.Values{"query": {query}})
	if err != nil {
		return 0, err
	}
	var total float64
	for _, series := range env.Data.Result {
		if len(series.Value) != 2 {
			continue
		}
		v, err := parseSampleValue(series.Value[1])
		if err != nil {
			return 0, err
		}
		total += v
	}
	return total, nil
}

// QueryByLabel runs an instant query and returns one value per series, keyed by
// the given label (for example "instance" for the per-node traffic split).
// Series without that label are grouped under "".
func (c *PromClient) QueryByLabel(ctx context.Context, query, label string) (map[string]float64, error) {
	env, err := c.do(ctx, "/api/v1/query", url.Values{"query": {query}})
	if err != nil {
		return nil, err
	}
	out := make(map[string]float64, len(env.Data.Result))
	for _, series := range env.Data.Result {
		if len(series.Value) != 2 {
			continue
		}
		v, err := parseSampleValue(series.Value[1])
		if err != nil {
			return nil, err
		}
		out[series.Metric[label]] += v
	}
	return out, nil
}

// QueryRange runs a range query and returns one sample per step, summed across
// series, sorted by time. NaN values (no data in the window) are skipped.
func (c *PromClient) QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]Sample, error) {
	env, err := c.do(ctx, "/api/v1/query_range", url.Values{
		"query": {query},
		"start": {strconv.FormatFloat(float64(start.UnixMilli())/1000, 'f', 3, 64)},
		"end":   {strconv.FormatFloat(float64(end.UnixMilli())/1000, 'f', 3, 64)},
		"step":  {strconv.FormatFloat(step.Seconds(), 'f', 0, 64)},
	})
	if err != nil {
		return nil, err
	}

	sums := make(map[int64]float64)
	for _, series := range env.Data.Result {
		for _, point := range series.Values {
			if len(point) != 2 {
				continue
			}
			ts, err := parseTimestamp(point[0])
			if err != nil {
				return nil, err
			}
			v, err := parseSampleValue(point[1])
			if err != nil {
				return nil, err
			}
			sums[ts] += v
		}
	}

	out := make([]Sample, 0, len(sums))
	for ts, v := range sums {
		out = append(out, Sample{T: time.Unix(ts, 0).UTC(), V: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].T.Before(out[j].T) })
	return out, nil
}

// do performs a GET and decodes the envelope. Any non-200 status, transport
// error, timeout or status:"error" body is returned as an error, so callers
// can fall back to Redis.
func (c *PromClient) do(ctx context.Context, path string, params url.Values) (*promEnvelope, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path+"?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("prometheus: build request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prometheus: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("prometheus: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prometheus: unexpected status %d", resp.StatusCode)
	}

	var env promEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("prometheus: decode response: %w", err)
	}
	if env.Status != "success" {
		return nil, fmt.Errorf("prometheus: query error (%s): %s", env.ErrorType, env.Error)
	}
	return &env, nil
}

func parseTimestamp(raw any) (int64, error) {
	f, ok := raw.(float64)
	if !ok {
		return 0, fmt.Errorf("prometheus: timestamp is %T, want number", raw)
	}
	return int64(f), nil
}

// parseSampleValue handles Prometheus's string-encoded floats, including NaN
// and +Inf. NaN means "no data" and is treated as 0.
func parseSampleValue(raw any) (float64, error) {
	s, ok := raw.(string)
	if !ok {
		return 0, fmt.Errorf("prometheus: value is %T, want string", raw)
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("prometheus: parse value %q: %w", s, err)
	}
	if math.IsNaN(v) {
		return 0, nil
	}
	return v, nil
}
