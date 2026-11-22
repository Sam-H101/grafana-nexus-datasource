package nexus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// MetricsData is the answer of the metrics endpoint: the Dropwizard registry
// of the Nexus process.
type MetricsData struct {
	Gauges map[string]struct {
		Value json.RawMessage `json:"value"`
	} `json:"gauges"`
	Counters   map[string]map[string]json.RawMessage `json:"counters"`
	Histograms map[string]map[string]json.RawMessage `json:"histograms"`
	Meters     map[string]map[string]json.RawMessage `json:"meters"`
	Timers     map[string]map[string]json.RawMessage `json:"timers"`
}

// Metrics reads the metrics registry. The user needs the nx-metrics-all
// privilege. Nexus 3.7x moved the endpoint and redirects the old address,
// which the HTTP client follows.
func (c *Client) Metrics(ctx context.Context) (*MetricsData, error) {
	var m MetricsData
	if _, err := c.get(ctx, "service/metrics/data", nil, &m); err != nil {
		if isNotFound(err) {
			return nil, fmt.Errorf("%w: no metrics endpoint", ErrNotNexus)
		}
		// Nexus before 3.77 answers 401, not 403, to a user without the
		// privilege. The status check tells that from a wrong password.
		if errors.Is(err, ErrUnauthorized) && c.HasCredentials() {
			if _, statusErr := c.Status(ctx); statusErr == nil {
				return nil, ErrForbidden
			}
		}
		return nil, err
	}
	return &m, nil
}

// Which parts of a meter, timer or histogram become values. The rest
// (units, every percentile) would only crowd the list.
var (
	meterParts     = []string{"count", "m1_rate", "m5_rate", "m15_rate", "mean_rate"}
	timerParts     = []string{"count", "mean", "p95", "p99", "max", "m1_rate"}
	histogramParts = []string{"count", "mean", "p95", "max"}
)

// Values flattens the registry into numbers by name. A gauge keeps its name
// (true and false become 1 and 0, text and structured gauges are left out),
// a counter keeps its name, and each kept part of a meter, timer or
// histogram is named "<name>.<part>".
func (m *MetricsData) Values() map[string]float64 {
	out := make(map[string]float64, len(m.Gauges)+len(m.Counters)+len(m.Meters)*len(meterParts))
	for name, g := range m.Gauges {
		if v, ok := number(g.Value); ok {
			out[name] = v
		}
	}
	for name, c := range m.Counters {
		if v, ok := number(c["count"]); ok {
			out[name] = v
		}
	}
	for _, group := range []struct {
		metrics map[string]map[string]json.RawMessage
		parts   []string
	}{{m.Meters, meterParts}, {m.Timers, timerParts}, {m.Histograms, histogramParts}} {
		for name, metric := range group.metrics {
			for _, part := range group.parts {
				if v, ok := number(metric[part]); ok {
					out[name+"."+part] = v
				}
			}
		}
	}
	return out
}

func number(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	switch raw[0] {
	case 't':
		return 1, string(raw) == "true"
	case 'f':
		return 0, string(raw) == "false"
	case '"', '{', '[', 'n':
		return 0, false
	}
	var v float64
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0, false
	}
	return v, true
}

// FormatStats is what Nexus counts for one package format.
type FormatStats struct {
	Format          string
	Repositories    int64
	Components      int64
	Assets          int64
	BytesDownloaded int64
	BytesUploaded   int64
}

const analytics = "nexus.analytics."

// Formats gathers the per-format gauges, sorted by format. The second result
// is false when this Nexus has none of them (they came with the usage
// reporting of recent versions).
func (m *MetricsData) Formats() ([]FormatStats, bool) {
	byFormat := map[string]*FormatStats{}
	at := func(format string) *FormatStats {
		s, ok := byFormat[format]
		if !ok {
			s = &FormatStats{Format: format}
			byFormat[format] = s
		}
		return s
	}
	found := false

	type count struct {
		Format string `json:"format"`
		Count  int64  `json:"count"`
	}
	for gauge, add := range map[string]func(*FormatStats, int64){
		"repository_type_counts":                    func(s *FormatStats, n int64) { s.Repositories += n },
		"repository_component_count_by_format_type": func(s *FormatStats, n int64) { s.Components += n },
		"repository_asset_count_by_format_type":     func(s *FormatStats, n int64) { s.Assets += n },
	} {
		var counts []count
		if g, ok := m.Gauges[analytics+gauge]; ok && json.Unmarshal(g.Value, &counts) == nil {
			found = true
			for _, c := range counts {
				if c.Format != "" {
					add(at(c.Format), c.Count)
				}
			}
		}
	}

	// [{"maven2":{"bytes_downloaded":1,"bytes_uploaded":2}}, ...]
	var transferred []map[string]struct {
		Downloaded int64 `json:"bytes_downloaded"`
		Uploaded   int64 `json:"bytes_uploaded"`
	}
	if g, ok := m.Gauges[analytics+"bytes_transferred_by_format"]; ok && json.Unmarshal(g.Value, &transferred) == nil {
		found = true
		for _, entry := range transferred {
			for format, b := range entry {
				// Every known format is listed; keep those in use.
				if _, used := byFormat[format]; !used && b.Downloaded == 0 && b.Uploaded == 0 {
					continue
				}
				s := at(format)
				s.BytesDownloaded += b.Downloaded
				s.BytesUploaded += b.Uploaded
			}
		}
	}

	out := make([]FormatStats, 0, len(byFormat))
	for _, s := range byFormat {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Format < out[j].Format })
	return out, found
}
