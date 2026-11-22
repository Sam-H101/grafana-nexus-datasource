package plugin

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Sam-H101/grafana-nexus-datasource/pkg/models"
	"github.com/Sam-H101/grafana-nexus-datasource/pkg/nexus"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
)

const (
	// maxConcurrentQueries bounds how many queries of one request run at once.
	maxConcurrentQueries = 5
	// maxParallelRepositories bounds the requests one query makes at once.
	maxParallelRepositories = 4
)

// QueryData runs the queries of one request concurrently (bounded) and
// returns one response per RefID.
func (d *Datasource) QueryData(ctx context.Context, req *backend.QueryDataRequest) (*backend.QueryDataResponse, error) {
	resp := backend.NewQueryDataResponse()
	if d.settingsErr != nil {
		p := d.explain(d.settingsErr)
		for _, q := range req.Queries {
			resp.Responses[q.RefID] = p.response()
		}
		return resp, nil
	}

	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, maxConcurrentQueries)
	)
	for _, q := range req.Queries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r := d.query(ctx, q)
			mu.Lock()
			resp.Responses[q.RefID] = r
			mu.Unlock()
		}()
	}
	wg.Wait()
	return resp, nil
}

func (d *Datasource) query(ctx context.Context, q backend.DataQuery) backend.DataResponse {
	qm, err := models.ParseQuery(q.JSON)
	if err != nil {
		return d.explain(err).response()
	}
	ctx, cancel := context.WithTimeout(ctx, d.settings.Timeout)
	defer cancel()

	var frame *data.Frame
	switch qm.QueryType {
	case models.QueryRepositories:
		frame, err = d.repositories(ctx, qm)
		err = needs(err, "repositories", privBrowse)
	case models.QueryFormats:
		frame, err = d.formats(ctx)
		err = needs(err, "format totals", privMetrics)
	case models.QueryBlobStores:
		frame, err = d.blobStores(ctx, qm)
		err = needs(err, "blob stores", privBlobStores)
	case models.QueryComponents:
		frame, err = d.components(ctx, qm)
		err = needs(err, "search results", privSearch)
	case models.QueryAssets:
		frame, err = d.assets(ctx, qm)
		err = needs(err, "search results", privSearch)
	case models.QueryTasks:
		frame, err = d.tasks(ctx)
		err = needs(err, "tasks", privTasks)
	case models.QueryHealthChecks:
		frame, err = d.healthChecks(ctx, qm)
		err = needs(err, "health checks", privMetrics)
	case models.QueryUsage:
		frame, err = d.usage(ctx, qm)
		err = needs(err, "usage", privMetrics)
	case models.QueryMonthlyUsage:
		frame, err = d.monthlyUsage(ctx)
		err = needs(err, "usage", privMetrics)
	case models.QueryMetrics:
		frame, err = d.metrics(ctx, qm)
		err = needs(err, "metrics", privMetrics)
	}
	if err != nil {
		p := d.explain(err)
		backend.Logger.Warn("Nexus query failed", "refId", q.RefID, "queryType", string(qm.QueryType), "status", int(p.status), "error", logSafe(p.message))
		return p.response()
	}
	frame.RefID = q.RefID
	return backend.DataResponse{Frames: data.Frames{frame}}
}

// forEach runs fn for every key, a few at a time, and returns the results in
// the order of keys. The first error cancels the rest.
func forEach[K, T any](ctx context.Context, keys []K, fn func(ctx context.Context, key K) (T, error)) ([]T, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		out      = make([]T, len(keys))
		wg       sync.WaitGroup
		sem      = make(chan struct{}, maxParallelRepositories)
		once     sync.Once
		firstErr error
	)
	for i, key := range keys {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			v, err := fn(ctx, key)
			if err != nil {
				once.Do(func() {
					firstErr = err
					cancel()
				})
				return
			}
			out[i] = v
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

func (d *Datasource) repositories(ctx context.Context, qm *models.QueryModel) (*data.Frame, error) {
	all, err := d.repositoryList(ctx)
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	for _, name := range qm.Repositories {
		wanted[name] = true
	}
	var (
		name, format, kind, blobStore, remote, writePolicy, versionPolicy, members, cleanup, link []string
		online                                                                                    []bool
	)
	for _, r := range all {
		if len(wanted) > 0 && !wanted[r.Name] {
			continue
		}
		if len(qm.Formats) > 0 && !contains(qm.Formats, r.Format) {
			continue
		}
		if qm.RepositoryType != "" && r.Type != qm.RepositoryType {
			continue
		}
		name = append(name, r.Name)
		format = append(format, r.Format)
		kind = append(kind, r.Type)
		online = append(online, r.Online)
		blobStore = append(blobStore, r.BlobStore)
		remote = append(remote, r.RemoteURL)
		writePolicy = append(writePolicy, r.WritePolicy)
		versionPolicy = append(versionPolicy, r.VersionPolicy)
		members = append(members, strings.Join(r.Members, ", "))
		cleanup = append(cleanup, strings.Join(r.Cleanup, ", "))
		link = append(link, r.URL)
	}
	frame := data.NewFrame("repositories",
		named(data.NewField("name", nil, orEmpty(name)), "Repository"),
		named(data.NewField("format", nil, orEmpty(format)), "Format"),
		named(data.NewField("type", nil, orEmpty(kind)), "Type"),
		onlineField(online),
		named(data.NewField("blob_store", nil, orEmpty(blobStore)), "Blob store"),
		named(data.NewField("remote_url", nil, orEmpty(remote)), "Proxies"),
		named(data.NewField("members", nil, orEmpty(members)), "Group members"),
		named(data.NewField("write_policy", nil, orEmpty(writePolicy)), "Write policy"),
		named(data.NewField("version_policy", nil, orEmpty(versionPolicy)), "Version policy"),
		named(data.NewField("cleanup_policies", nil, orEmpty(cleanup)), "Cleanup policies"),
		named(data.NewField("url", nil, orEmpty(link)), "URL"),
	)
	switch {
	case len(all) == 0:
		addNotice(frame, "This login cannot see any repository. Give one of the user's roles the privilege "+privBrowse+".")
	case len(name) == 0:
		addNotice(frame, "No repository matches the filter.")
	case !hasDetails(all):
		addNotice(frame, "Blob store, policies and group members are blank because Nexus only gives them to a user with the privilege "+privRepositoryAdmin+".")
	}
	return frame, nil
}

// hasDetails reports whether Nexus gave the settings of any repository.
func hasDetails(repos []nexus.Repository) bool {
	for _, r := range repos {
		if r.BlobStore != "" {
			return true
		}
	}
	return false
}

func onlineField(online []bool) *data.Field {
	if online == nil {
		online = []bool{}
	}
	f := named(data.NewField("online", nil, online), "Online")
	f.Config.Mappings = data.ValueMappings{data.ValueMapper{
		"true":  {Text: "Online", Color: "green"},
		"false": {Text: "Offline", Color: "red"},
	}}
	return f
}

func (d *Datasource) formats(ctx context.Context) (*data.Frame, error) {
	m, err := d.client.Metrics(ctx)
	if err != nil {
		return nil, err
	}
	stats, found := m.Formats()
	n := len(stats)
	var (
		format                                          = make([]string, n)
		repos, components, assets, downloaded, uploaded = make([]int64, n), make([]int64, n), make([]int64, n), make([]int64, n), make([]int64, n)
	)
	for i, s := range stats {
		format[i], repos[i], components[i], assets[i] = s.Format, s.Repositories, s.Components, s.Assets
		downloaded[i], uploaded[i] = s.BytesDownloaded, s.BytesUploaded
	}
	frame := data.NewFrame("formats",
		named(data.NewField("format", nil, format), "Format"),
		named(data.NewField("repositories", nil, repos), "Repositories"),
		named(data.NewField("components", nil, components), "Components"),
		named(data.NewField("assets", nil, assets), "Assets"),
		unit(named(data.NewField("bytes_downloaded", nil, downloaded), "Downloaded"), "bytes"),
		unit(named(data.NewField("bytes_uploaded", nil, uploaded), "Uploaded"), "bytes"),
	)
	if !found {
		addNotice(frame, "This Nexus version does not report totals per format.")
	}
	return frame, nil
}

// numbers builds the frame that alert rules, stat panels and gauges take:
// one row at the current time and one number field per item. Each field
// carries a label, so that an alert rule gets one instance per item.
func (d *Datasource) numbers(name, label, unitName string, items []string, values []float64) *data.Frame {
	frame := data.NewFrame(name, data.NewField("time", nil, []time.Time{d.now().UTC()}))
	for i, item := range items {
		f := data.NewField(name, data.Labels{label: item}, []float64{values[i]})
		f.Config = &data.FieldConfig{DisplayNameFromDS: item, Unit: unitName}
		frame.Fields = append(frame.Fields, f)
	}
	frame.Meta = &data.FrameMeta{Type: data.FrameTypeTimeSeriesWide, TypeVersion: data.FrameTypeVersion{0, 1}}
	return frame
}

func (d *Datasource) blobStores(ctx context.Context, qm *models.QueryModel) (*data.Frame, error) {
	stores, err := d.client.BlobStores(ctx)
	if err != nil {
		return nil, err
	}
	if qm.BlobStoreValue != "" {
		names, values := make([]string, len(stores)), make([]float64, len(stores))
		unitName := "bytes"
		for i, s := range stores {
			names[i] = s.Name
			switch qm.BlobStoreValue {
			case "free":
				values[i] = float64(s.AvailableSpaceInBytes)
			case "size":
				values[i] = float64(s.TotalSizeInBytes)
			default:
				values[i], unitName = float64(s.BlobCount), ""
			}
		}
		return d.numbers(qm.BlobStoreValue, "blob_store", unitName, names, values), nil
	}
	n := len(stores)
	var (
		name, kind, quotaType = make([]string, n), make([]string, n), make([]string, n)
		available             = make([]bool, n)
		blobs, size, free     = make([]int64, n), make([]int64, n), make([]int64, n)
		quota                 = make([]*int64, n)
	)
	for i, s := range stores {
		name[i], kind[i], available[i] = s.Name, s.Type, !s.Unavailable
		blobs[i], size[i], free[i] = s.BlobCount, s.TotalSizeInBytes, s.AvailableSpaceInBytes
		if s.SoftQuota != nil {
			limit := s.SoftQuota.Limit
			quotaType[i], quota[i] = s.SoftQuota.Type, &limit
		}
	}
	availableField := named(data.NewField("available", nil, available), "Available")
	availableField.Config.Mappings = data.ValueMappings{data.ValueMapper{
		"true":  {Text: "Available", Color: "green"},
		"false": {Text: "Unavailable", Color: "red"},
	}}
	frame := data.NewFrame("blob_stores",
		named(data.NewField("name", nil, name), "Blob store"),
		named(data.NewField("type", nil, kind), "Type"),
		availableField,
		named(data.NewField("blobs", nil, blobs), "Blobs"),
		unit(named(data.NewField("size", nil, size), "Size"), "bytes"),
		unit(named(data.NewField("free", nil, free), "Free space"), "bytes"),
		unit(named(data.NewField("quota", nil, quota), "Quota"), "bytes"),
		named(data.NewField("quota_type", nil, quotaType), "Quota type"),
	)
	return frame, nil
}

// target is one search: Nexus takes at most one repository and one format.
type target struct{ repository, format string }

// searchTargets is the list of searches one query makes. Nexus searches one
// repository and one format at a time, so several of either become several
// searches:
//
//	nothing selected            one search across everything
//	formats only                one search per format
//	repositories (and formats)  one search per repository (of those formats)
//
// Selecting every repository, as a dashboard variable set to All does, is
// the same as selecting none.
func (d *Datasource) searchTargets(ctx context.Context, qm *models.QueryModel) []target {
	repositories := qm.Repositories
	var known map[string]string
	if len(repositories) > 0 {
		// Best effort: without the list every selected repository is searched.
		if all, err := d.repositoryList(ctx); err == nil {
			known = make(map[string]string, len(all))
			for _, r := range all {
				known[r.Name] = r.Format
			}
			if coversAll(repositories, known) {
				repositories = nil
			}
		}
	}
	var targets []target
	switch {
	case len(repositories) == 0 && len(qm.Formats) == 0:
		targets = []target{{}}
	case len(repositories) == 0:
		for _, f := range qm.Formats {
			targets = append(targets, target{format: f})
		}
	default:
		for _, r := range repositories {
			// A repository of another format has nothing to offer. One
			// that is not in the list is searched, so Nexus can say so.
			if format, ok := known[r]; ok && len(qm.Formats) > 0 && !contains(qm.Formats, format) {
				continue
			}
			targets = append(targets, target{repository: r})
		}
	}
	return targets
}

// coversAll reports whether every known repository is selected.
func coversAll(selected []string, known map[string]string) bool {
	if len(known) == 0 || len(selected) < len(known) {
		return false
	}
	for name := range known {
		if !contains(selected, name) {
			return false
		}
	}
	return true
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func searchFilter(qm *models.QueryModel, t target) nexus.SearchFilter {
	return nexus.SearchFilter{
		Repository: t.repository,
		Format:     t.format,
		Keyword:    qm.Keyword,
		Group:      qm.Group,
		Name:       qm.Name,
		Version:    qm.Version,
		Sort:       qm.Sort,
		Descending: qm.Descending,
	}
}

type page[T any] struct {
	items []T
	more  bool
}

// gather runs the search for every target and joins the results, up to the
// limit.
func gather[T any](ctx context.Context, d *Datasource, qm *models.QueryModel, run func(ctx context.Context, f nexus.SearchFilter, limit int) ([]T, bool, error)) ([]T, bool, error) {
	pages, err := forEach(ctx, d.searchTargets(ctx, qm), func(ctx context.Context, t target) (page[T], error) {
		items, more, err := run(ctx, searchFilter(qm, t), qm.Limit)
		return page[T]{items, more}, err
	})
	if err != nil {
		return nil, false, err
	}
	var (
		all  []T
		more bool
	)
	for _, p := range pages {
		all = append(all, p.items...)
		more = more || p.more
	}
	if len(all) > qm.Limit {
		all, more = all[:qm.Limit], true
	}
	return all, more, nil
}

func limitNotice(frame *data.Frame, what string, n int, more bool) {
	switch {
	case more:
		addNotice(frame, fmt.Sprintf("Showing the first %d %s. Narrow the search or raise the limit (up to %d) to see others.", n, what, models.MaxLimit))
	case n == 0:
		addNotice(frame, "No matching "+what+".")
	}
}

func (d *Datasource) components(ctx context.Context, qm *models.QueryModel) (*data.Frame, error) {
	items, more, err := gather(ctx, d, qm, d.client.Components)
	if err != nil {
		return nil, err
	}
	n := len(items)
	var (
		repository, format, group, name, version = make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n)
		assets, size                             = make([]int64, n), make([]int64, n)
		updated, downloaded                      = make([]*time.Time, n), make([]*time.Time, n)
	)
	for i, c := range items {
		repository[i], format[i], group[i], name[i], version[i] = c.Repository, c.Format, c.Group, c.Name, c.Version
		assets[i] = int64(len(c.Assets))
		for _, a := range c.Assets {
			size[i] += a.FileSize
			updated[i] = latest(updated[i], &a.LastModified)
			downloaded[i] = latest(downloaded[i], a.LastDownloaded)
		}
	}
	frame := data.NewFrame("components",
		named(data.NewField("repository", nil, repository), "Repository"),
		named(data.NewField("format", nil, format), "Format"),
		named(data.NewField("group", nil, group), "Group"),
		named(data.NewField("name", nil, name), "Name"),
		named(data.NewField("version", nil, version), "Version"),
		named(data.NewField("assets", nil, assets), "Assets"),
		unit(named(data.NewField("size", nil, size), "Size"), "bytes"),
		named(data.NewField("last_updated", nil, updated), "Last updated"),
		named(data.NewField("last_downloaded", nil, downloaded), "Last downloaded"),
	)
	limitNotice(frame, "components", n, more)
	return frame, nil
}

func (d *Datasource) assets(ctx context.Context, qm *models.QueryModel) (*data.Frame, error) {
	items, more, err := gather(ctx, d, qm, d.client.Assets)
	if err != nil {
		return nil, err
	}
	n := len(items)
	var (
		repository, format, path, contentType, uploader, sha1, link = make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n)
		size                                                        = make([]int64, n)
		uploaded, modified, downloaded                              = make([]*time.Time, n), make([]*time.Time, n), make([]*time.Time, n)
	)
	for i, a := range items {
		repository[i], format[i], path[i], contentType[i] = a.Repository, a.Format, a.Path, a.ContentType
		sha1[i], link[i], size[i] = a.Checksum.SHA1, a.DownloadURL, a.FileSize
		if a.Uploader != nil {
			uploader[i] = *a.Uploader
		}
		modified[i] = latest(nil, &a.LastModified)
		// Nexus before 3.77 has no separate upload time.
		if uploaded[i] = latest(nil, &a.BlobCreated); uploaded[i] == nil {
			uploaded[i] = modified[i]
		}
		downloaded[i] = latest(nil, a.LastDownloaded)
	}
	// The upload time comes first: it is the time of the row for annotations
	// and for panels that need one.
	frame := data.NewFrame("assets",
		named(data.NewField("uploaded", nil, uploaded), "Uploaded"),
		named(data.NewField("repository", nil, repository), "Repository"),
		named(data.NewField("path", nil, path), "Path"),
		unit(named(data.NewField("size", nil, size), "Size"), "bytes"),
		named(data.NewField("format", nil, format), "Format"),
		named(data.NewField("content_type", nil, contentType), "Content type"),
		named(data.NewField("uploaded_by", nil, uploader), "Uploaded by"),
		named(data.NewField("last_modified", nil, modified), "Last modified"),
		named(data.NewField("last_downloaded", nil, downloaded), "Last downloaded"),
		named(data.NewField("sha1", nil, sha1), "SHA-1"),
		named(data.NewField("url", nil, link), "URL"),
	)
	limitNotice(frame, "assets", n, more)
	return frame, nil
}

// latest parses s and returns the later of it and t. A missing or unreadable
// s leaves t as it is.
func latest(t *time.Time, s *string) *time.Time {
	if s == nil {
		return t
	}
	parsed, ok := nexus.ParseTime(*s)
	if !ok || (t != nil && !parsed.After(*t)) {
		return t
	}
	return &parsed
}

func (d *Datasource) tasks(ctx context.Context) (*data.Frame, error) {
	tasks, err := d.client.Tasks(ctx)
	if err != nil {
		return nil, err
	}
	n := len(tasks)
	var (
		name, kind, state, result, schedule, message, id = make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n)
		enabled                                          = make([]bool, n)
		lastRun, nextRun                                 = make([]*time.Time, n), make([]*time.Time, n)
	)
	for i, t := range tasks {
		name[i], kind[i], state[i], schedule[i], id[i] = t.Name, t.TypeName, t.CurrentState, t.Schedule, t.ID
		if kind[i] == "" {
			kind[i] = t.Type
		}
		if t.LastRunResult != nil {
			result[i] = *t.LastRunResult
		}
		if t.Message != nil {
			message[i] = *t.Message
		}
		enabled[i] = t.Enabled == nil || *t.Enabled
		lastRun[i], nextRun[i] = latest(nil, t.LastRun), latest(nil, t.NextRun)
	}
	return data.NewFrame("tasks",
		named(data.NewField("name", nil, name), "Task"),
		named(data.NewField("state", nil, state), "State"),
		named(data.NewField("last_result", nil, result), "Last result"),
		named(data.NewField("last_run", nil, lastRun), "Last run"),
		named(data.NewField("next_run", nil, nextRun), "Next run"),
		named(data.NewField("schedule", nil, schedule), "Schedule"),
		named(data.NewField("enabled", nil, enabled), "Enabled"),
		named(data.NewField("type", nil, kind), "Type"),
		named(data.NewField("message", nil, message), "Message"),
		named(data.NewField("id", nil, id), "ID"),
	), nil
}

var (
	lineBreak = regexp.MustCompile(`(?i)\s*<br\s*/?>\s*`)
	htmlTag   = regexp.MustCompile(`<[^>]+>`)
)

// plainText turns the HTML fragments Nexus puts in health check messages
// into one line of text.
func plainText(s string) string {
	s = lineBreak.ReplaceAllString(s, "; ")
	s = html.UnescapeString(htmlTag.ReplaceAllString(s, ""))
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), ";"))
}

func (d *Datasource) healthChecks(ctx context.Context, qm *models.QueryModel) (*data.Frame, error) {
	checks, err := d.client.HealthChecks(ctx)
	if err != nil {
		return nil, err
	}
	// Failing checks first.
	sort.SliceStable(checks, func(i, j int) bool { return !checks[i].Healthy && checks[j].Healthy })
	var (
		name, message []string
		healthy       []bool
		checked       []*time.Time
	)
	for _, c := range checks {
		if qm.UnhealthyOnly && c.Healthy {
			continue
		}
		name = append(name, c.Name)
		healthy = append(healthy, c.Healthy)
		text := ""
		if c.Message != nil {
			text = plainText(*c.Message)
		}
		message = append(message, text)
		checked = append(checked, latest(nil, &c.Timestamp))
	}
	if healthy == nil {
		healthy, checked = []bool{}, []*time.Time{}
	}
	healthyField := named(data.NewField("healthy", nil, healthy), "Status")
	healthyField.Config.Mappings = data.ValueMappings{data.ValueMapper{
		"true":  {Text: "Healthy", Color: "green"},
		"false": {Text: "Failing", Color: "red"},
	}}
	frame := data.NewFrame("health_checks",
		named(data.NewField("check", nil, orEmpty(name)), "Check"),
		healthyField,
		named(data.NewField("message", nil, orEmpty(message)), "Message"),
		named(data.NewField("checked", nil, checked), "Checked"),
	)
	if qm.UnhealthyOnly && len(name) == 0 && len(checks) > 0 {
		addNotice(frame, fmt.Sprintf("All %d health checks pass.", len(checks)))
	}
	return frame, nil
}

func (d *Datasource) usage(ctx context.Context, qm *models.QueryModel) (*data.Frame, error) {
	points, err := d.client.UsageHistory(ctx, qm.UsageMetric, qm.UsagePeriod)
	if err != nil {
		return nil, err
	}
	times, values := make([]time.Time, len(points)), make([]float64, len(points))
	for i, p := range points {
		times[i], values[i] = p.Time, p.Value
	}
	display := "Requests"
	if qm.UsageMetric == "components" {
		display = "Components"
	}
	value := named(data.NewField(qm.UsageMetric, nil, values), display)
	value.Config.SetDecimals(0)
	frame := data.NewFrame(qm.UsageMetric, data.NewField("time", nil, times), value)
	frame.Meta = &data.FrameMeta{Type: data.FrameTypeTimeSeriesWide, TypeVersion: data.FrameTypeVersion{0, 1}}
	if len(points) == 0 {
		addNotice(frame, "Nexus has not recorded any "+qm.UsagePeriod+" "+qm.UsageMetric+" figures yet.")
	}
	return frame, nil
}

func (d *Datasource) monthlyUsage(ctx context.Context) (*data.Frame, error) {
	months, err := d.client.MonthlyUsage(ctx)
	if err != nil {
		return nil, err
	}
	var (
		month                                 []time.Time
		requests, components, storage, egress []int64
	)
	for _, m := range months {
		t, ok := nexus.ParseTime(m.MetricDate)
		if !ok {
			continue
		}
		month = append(month, t)
		requests = append(requests, m.RequestCount)
		components = append(components, m.ComponentCount)
		storage = append(storage, m.PeakStorage)
		egress = append(egress, m.ResponseSize)
	}
	if month == nil {
		month, requests, components, storage, egress = []time.Time{}, []int64{}, []int64{}, []int64{}, []int64{}
	}
	return data.NewFrame("monthly_usage",
		named(data.NewField("month", nil, month), "Month"),
		named(data.NewField("requests", nil, requests), "Requests"),
		named(data.NewField("components", nil, components), "Components"),
		unit(named(data.NewField("peak_storage", nil, storage), "Peak storage"), "bytes"),
		unit(named(data.NewField("egress", nil, egress), "Data served"), "bytes"),
	), nil
}

// metrics returns the selected internal metrics as one row at the current
// time, a number field each: the shape stat panels, gauges and alert rules
// take. Without a selection it lists every metric and its value.
func (d *Datasource) metrics(ctx context.Context, qm *models.QueryModel) (*data.Frame, error) {
	values, err := d.metricValues(ctx)
	if err != nil {
		return nil, err
	}
	if len(qm.Metrics) == 0 {
		names := make([]string, 0, len(values))
		for name := range values {
			names = append(names, name)
		}
		sort.Strings(names)
		nums := make([]float64, len(names))
		for i, name := range names {
			nums[i] = values[name]
		}
		return data.NewFrame("metrics",
			named(data.NewField("metric", nil, names), "Metric"),
			named(data.NewField("value", nil, nums), "Value"),
		), nil
	}

	var (
		known   []string
		nums    []float64
		unknown []string
	)
	for _, name := range qm.Metrics {
		v, ok := values[name]
		if !ok {
			unknown = append(unknown, name)
			continue
		}
		known, nums = append(known, name), append(nums, v)
	}
	frame := d.numbers("value", "metric", "", known, nums)
	if len(unknown) > 0 {
		addNotice(frame, "Nexus does not report: "+strings.Join(unknown, ", ")+".")
	}
	return frame, nil
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func named(f *data.Field, display string) *data.Field {
	if f.Config == nil {
		f.Config = &data.FieldConfig{}
	}
	f.Config.DisplayName = display
	return f
}

func unit(f *data.Field, u string) *data.Field {
	if f.Config == nil {
		f.Config = &data.FieldConfig{}
	}
	f.Config.Unit = u
	return f
}

func addNotice(frame *data.Frame, text string) {
	if text != "" {
		frame.AppendNotices(data.Notice{Severity: data.NoticeSeverityInfo, Text: text})
	}
}
