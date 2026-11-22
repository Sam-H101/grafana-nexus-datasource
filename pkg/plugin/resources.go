package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/Sam-H101/grafana-nexus-datasource/pkg/nexus"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/resource/httpadapter"
)

const (
	repositoriesTTL = time.Minute
	// metricsTTL is short: it only spares Nexus when several panels of one
	// dashboard ask for metrics in the same refresh.
	metricsTTL      = 5 * time.Second
	resourceTimeout = 20 * time.Second
)

// catalogue caches what several queries of one refresh, and the query
// editor, ask for repeatedly.
type catalogue struct {
	mu             sync.Mutex
	repositories   []nexus.Repository
	repositoriesAt time.Time
	metrics        map[string]float64
	metricsAt      time.Time
}

func (d *Datasource) repositoryList(ctx context.Context) ([]nexus.Repository, error) {
	d.cache.mu.Lock()
	if d.cache.repositories != nil && d.now().Sub(d.cache.repositoriesAt) < repositoriesTTL {
		repos := d.cache.repositories
		d.cache.mu.Unlock()
		return repos, nil
	}
	d.cache.mu.Unlock()

	repos, err := d.client.Repositories(ctx)
	if err != nil {
		return nil, err
	}
	d.cache.mu.Lock()
	d.cache.repositories, d.cache.repositoriesAt = repos, d.now()
	d.cache.mu.Unlock()
	return repos, nil
}

func (d *Datasource) metricValues(ctx context.Context) (map[string]float64, error) {
	d.cache.mu.Lock()
	if d.cache.metrics != nil && d.now().Sub(d.cache.metricsAt) < metricsTTL {
		values := d.cache.metrics
		d.cache.mu.Unlock()
		return values, nil
	}
	d.cache.mu.Unlock()

	m, err := d.client.Metrics(ctx)
	if err != nil {
		return nil, err
	}
	values := m.Values()
	d.cache.mu.Lock()
	d.cache.metrics, d.cache.metricsAt = values, d.now()
	d.cache.mu.Unlock()
	return values, nil
}

// CallResource serves the lists the query editor needs:
//
//	GET /repositories  [{"name","format","type"}]
//	GET /metrics       ["metric name", ...]
func (d *Datasource) CallResource(ctx context.Context, req *backend.CallResourceRequest, sender backend.CallResourceResponseSender) error {
	d.resOnce.Do(func() {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /repositories", d.handleRepositories)
		mux.HandleFunc("GET /metrics", d.handleMetrics)
		d.resHandler = httpadapter.New(mux)
	})
	return d.resHandler.CallResource(ctx, req, sender)
}

type repositoryItem struct {
	Name   string `json:"name"`
	Format string `json:"format"`
	Type   string `json:"type"`
}

func (d *Datasource) handleRepositories(w http.ResponseWriter, r *http.Request) {
	ctx, cancel, ok := d.resourceContext(w, r)
	if !ok {
		return
	}
	defer cancel()
	repos, err := d.repositoryList(ctx)
	if err != nil {
		d.resourceError(w, needs(err, "repositories", privBrowse))
		return
	}
	items := make([]repositoryItem, len(repos))
	for i, repo := range repos {
		items[i] = repositoryItem{Name: repo.Name, Format: repo.Format, Type: repo.Type}
	}
	writeJSON(w, http.StatusOK, items)
}

func (d *Datasource) handleMetrics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel, ok := d.resourceContext(w, r)
	if !ok {
		return
	}
	defer cancel()
	values, err := d.metricValues(ctx)
	if err != nil {
		d.resourceError(w, needs(err, "metrics", privMetrics))
		return
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	writeJSON(w, http.StatusOK, names)
}

func (d *Datasource) resourceContext(w http.ResponseWriter, r *http.Request) (context.Context, context.CancelFunc, bool) {
	if d.settingsErr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": d.explain(d.settingsErr).message})
		return nil, nil, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), resourceTimeout)
	return ctx, cancel, true
}

func (d *Datasource) resourceError(w http.ResponseWriter, err error) {
	p := d.explain(err)
	backend.Logger.Warn("Nexus metadata request failed", "error", logSafe(p.message))
	status := http.StatusBadGateway
	if p.status == backend.StatusBadRequest || p.status == backend.StatusUnauthorized || p.status == backend.StatusForbidden {
		status = int(p.status)
	}
	writeJSON(w, status, map[string]string{"message": p.message})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
