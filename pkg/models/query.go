package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// QueryType selects what a query returns.
type QueryType string

const (
	// QueryRepositories returns the repositories and their configuration.
	QueryRepositories QueryType = "repositories"
	// QueryFormats returns totals per package format: repositories,
	// components, assets and bytes transferred.
	QueryFormats QueryType = "formats"
	// QueryBlobStores returns the blob stores with their size and free space.
	QueryBlobStores QueryType = "blobStores"
	// QueryComponents searches components (packages, artifacts).
	QueryComponents QueryType = "components"
	// QueryAssets searches assets (files), with upload and download times.
	QueryAssets QueryType = "assets"
	// QueryTasks returns the scheduled tasks and how their last run went.
	QueryTasks QueryType = "tasks"
	// QueryHealthChecks returns the system status checks.
	QueryHealthChecks QueryType = "healthChecks"
	// QueryUsage returns the request or component count over time.
	QueryUsage QueryType = "usage"
	// QueryMonthlyUsage returns requests, components, storage and egress
	// for each of the last twelve months.
	QueryMonthlyUsage QueryType = "monthlyUsage"
	// QueryMetrics returns current values of Nexus' internal metrics.
	QueryMetrics QueryType = "metrics"
)

// Limits on what one query may ask for. They bound the number of requests a
// single panel can cause.
const (
	MaxRepositories = 200
	MaxFormats      = 20
	MaxMetrics      = 50
	DefaultLimit    = 100
	MaxLimit        = 10000
	maxTextLength   = 300
)

// QueryModel is a validated query. Fields that do not apply to the query
// type are ignored.
type QueryModel struct {
	QueryType QueryType
	// Repositories restricts repositories, components and assets. Empty
	// means every repository.
	Repositories []string
	// Formats restricts repositories, components and assets (maven2, npm...).
	// Empty means every format. The query carries them in one "format" text,
	// comma-separated, which is how a dashboard variable set to All or to
	// several values arrives.
	Formats []string
	// RepositoryType restricts the repository list: hosted, proxy or group.
	RepositoryType string
	// Keyword, Group, Name and Version are the search terms for components
	// and assets. Group, Name and Version accept * as a wildcard.
	Keyword string
	Group   string
	Name    string
	Version string
	// Sort orders search results by Nexus: "", name, group, version or
	// repository.
	Sort       string
	Descending bool
	Limit      int
	// UsageMetric is requests or components; UsagePeriod is daily or monthly.
	UsageMetric string
	UsagePeriod string
	// Metrics are the internal metric names to return. Empty lists them all.
	Metrics []string
	// UnhealthyOnly keeps only failing health checks.
	UnhealthyOnly bool
	// BlobStoreValue turns the blob store table into one number per blob
	// store, for alert rules and stat panels: free, size or blobs. Empty
	// means the table.
	BlobStoreValue string
}

type rawQuery struct {
	QueryType      string   `json:"queryType"`
	Repositories   []string `json:"repositories"`
	Format         string   `json:"format"`
	RepositoryType string   `json:"repositoryType"`
	Keyword        string   `json:"keyword"`
	Group          string   `json:"group"`
	Name           string   `json:"name"`
	Version        string   `json:"version"`
	Sort           string   `json:"sort"`
	Descending     bool     `json:"descending"`
	Limit          int      `json:"limit"`
	UsageMetric    string   `json:"usageMetric"`
	UsagePeriod    string   `json:"usagePeriod"`
	Metrics        []string `json:"metrics"`
	UnhealthyOnly  bool     `json:"unhealthyOnly"`
	BlobStoreValue string   `json:"blobStoreValue"`
}

// ErrInvalidQuery wraps every query validation failure.
var ErrInvalidQuery = errors.New("invalid query")

func badQuery(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidQuery, fmt.Sprintf(format, a...))
}

var (
	// Nexus repository names: letters, digits, underscore, hyphen and dot.
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.\-]{1,200}$`)
	formatPattern     = regexp.MustCompile(`^[a-z0-9_\-]{1,40}$`)
	// A dashboard variable that Grafana did not replace ($repo, ${repo},
	// [[repo]]): the variable does not exist or has no value.
	variablePattern = regexp.MustCompile(`\$\{?[A-Za-z_]\w*|\[\[\w+`)
)

var (
	repositoryTypes = set("hosted", "proxy", "group")
	sorts           = set("name", "group", "version", "repository")
	usageMetrics    = set("requests", "components")
	usagePeriods    = set("daily", "monthly")
	blobStoreValues = set("free", "size", "blobs")
	queryTypes      = set(
		string(QueryRepositories), string(QueryFormats), string(QueryBlobStores), string(QueryComponents),
		string(QueryAssets), string(QueryTasks), string(QueryHealthChecks), string(QueryUsage),
		string(QueryMonthlyUsage), string(QueryMetrics),
	)
)

func set(values ...string) map[string]bool {
	m := make(map[string]bool, len(values))
	for _, v := range values {
		m[v] = true
	}
	return m
}

// ParseQuery validates the JSON of one query.
func ParseQuery(raw json.RawMessage) (*QueryModel, error) {
	var rq rawQuery
	if err := json.Unmarshal(raw, &rq); err != nil {
		return nil, badQuery("the query is not valid JSON")
	}
	if rq.QueryType == "" {
		rq.QueryType = string(QueryRepositories)
	}
	if !queryTypes[rq.QueryType] {
		return nil, badQuery("unknown query type %q", truncate(rq.QueryType, 40))
	}
	qm := &QueryModel{
		QueryType:     QueryType(rq.QueryType),
		Descending:    rq.Descending,
		UnhealthyOnly: rq.UnhealthyOnly,
	}

	var err error
	if qm.Repositories, err = cleanList(rq.Repositories, "repositories", MaxRepositories); err != nil {
		return nil, err
	}
	for _, r := range qm.Repositories {
		if variablePattern.MatchString(r) {
			return nil, badQuery("the repository %q is a dashboard variable without a value", truncate(r, 60))
		}
		if !repositoryPattern.MatchString(r) {
			return nil, badQuery("%q is not a repository name; names have letters, digits, dots, hyphens and underscores", truncate(r, 60))
		}
	}

	if variablePattern.MatchString(rq.Format) {
		return nil, badQuery("the format %q is a dashboard variable without a value", truncate(rq.Format, 60))
	}
	if qm.Formats, err = cleanList([]string{strings.ToLower(rq.Format)}, "formats", MaxFormats); err != nil {
		return nil, err
	}
	for _, f := range qm.Formats {
		if !formatPattern.MatchString(f) {
			return nil, badQuery("%q is not a format; use a name such as maven2, npm or docker", truncate(f, 60))
		}
	}

	qm.RepositoryType = strings.ToLower(strings.TrimSpace(rq.RepositoryType))
	if qm.RepositoryType != "" && !repositoryTypes[qm.RepositoryType] {
		return nil, badQuery("the repository type must be hosted, proxy or group")
	}

	for _, t := range []struct {
		what string
		in   string
		out  *string
	}{
		{"keyword", rq.Keyword, &qm.Keyword},
		{"group", rq.Group, &qm.Group},
		{"name", rq.Name, &qm.Name},
		{"version", rq.Version, &qm.Version},
	} {
		v := strings.TrimSpace(t.in)
		if len(v) > maxTextLength {
			return nil, badQuery("the %s is longer than %d characters", t.what, maxTextLength)
		}
		if variablePattern.MatchString(v) {
			return nil, badQuery("the %s %q is a dashboard variable without a value", t.what, truncate(v, 60))
		}
		*t.out = v
	}

	qm.Sort = strings.ToLower(strings.TrimSpace(rq.Sort))
	if qm.Sort != "" && !sorts[qm.Sort] {
		return nil, badQuery("results can be sorted by name, group, version or repository")
	}

	switch {
	case rq.Limit == 0:
		qm.Limit = DefaultLimit
	case rq.Limit < 0 || rq.Limit > MaxLimit:
		return nil, badQuery("the limit must be between 1 and %d", MaxLimit)
	default:
		qm.Limit = rq.Limit
	}

	qm.UsageMetric = strings.ToLower(strings.TrimSpace(rq.UsageMetric))
	if qm.UsageMetric == "" {
		qm.UsageMetric = "requests"
	}
	qm.UsagePeriod = strings.ToLower(strings.TrimSpace(rq.UsagePeriod))
	if qm.UsagePeriod == "" {
		qm.UsagePeriod = "daily"
	}
	if qm.QueryType == QueryUsage {
		if !usageMetrics[qm.UsageMetric] {
			return nil, badQuery("usage is reported for requests or components")
		}
		if !usagePeriods[qm.UsagePeriod] {
			return nil, badQuery("usage is reported daily or monthly")
		}
	}

	qm.BlobStoreValue = strings.TrimSpace(rq.BlobStoreValue)
	if qm.BlobStoreValue == "table" {
		qm.BlobStoreValue = ""
	}
	if qm.QueryType == QueryBlobStores && qm.BlobStoreValue != "" && !blobStoreValues[qm.BlobStoreValue] {
		return nil, badQuery("blob stores can be shown as a table, or as their free space, size or blob count")
	}

	if qm.Metrics, err = cleanList(rq.Metrics, "metrics", MaxMetrics); err != nil {
		return nil, err
	}
	for _, m := range qm.Metrics {
		if len(m) > maxTextLength {
			return nil, badQuery("a metric name is longer than %d characters", maxTextLength)
		}
		if variablePattern.MatchString(m) {
			return nil, badQuery("the metric %q is a dashboard variable without a value", truncate(m, 60))
		}
	}
	return qm, nil
}

// cleanList trims, drops blanks and duplicates, and enforces a maximum. One
// entry may hold several comma-separated values, which is how Grafana expands
// a multi-value variable.
func cleanList(in []string, what string, limit int) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, entry := range in {
		for _, v := range strings.Split(entry, ",") {
			v = strings.TrimSpace(v)
			if v == "" || seen[v] {
				continue
			}
			seen[v] = true
			out = append(out, v)
		}
	}
	if len(out) > limit {
		return nil, badQuery("too many %s: %d selected, the limit is %d", what, len(out), limit)
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
