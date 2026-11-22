// Package nexus is a client for the REST API of Sonatype Nexus Repository 3.
// It only reads. docs/nexus-api-notes.md records the observed behaviour the
// client relies on.
package nexus

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	// maxResponseBytes caps one response body.
	maxResponseBytes = 32 << 20
	userAgent        = "grafana-nexus-datasource"
	// maxPages bounds how many pages one listing may follow.
	maxPages = 250
)

var (
	// ErrUnauthorized is a 401: the credentials are missing or wrong.
	ErrUnauthorized = errors.New("nexus: unauthorized")
	// ErrForbidden is a 403: the user lacks a privilege.
	ErrForbidden = errors.New("nexus: forbidden")
	// ErrNotNexus means the server answered, but not like the Nexus API:
	// usually a wrong URL, a missing context path, or a login page.
	ErrNotNexus = errors.New("nexus: unexpected response")
	// ErrResponseTooLarge means a response exceeded maxResponseBytes.
	ErrResponseTooLarge = errors.New("nexus: response too large")
	// ErrNoUsage means this Nexus does not report usage history: the
	// endpoints arrived with Community Edition (3.77).
	ErrNoUsage = errors.New("nexus: usage history is not available")
)

// APIError is an error reported by the API.
type APIError struct {
	Status  int
	Message string
	// bare is set when Nexus gave a status but no message of its own.
	bare bool
}

func (e *APIError) Error() string { return e.Message }

// NotFound reports whether the API said the thing asked for does not exist.
func (e *APIError) NotFound() bool { return e.Status == http.StatusNotFound }

// Config is what the client needs to reach one Nexus.
type Config struct {
	// BaseURL has no trailing slash and may include a context path.
	BaseURL  string
	Username string
	Password string
}

// Client talks to one Nexus. It is safe for concurrent use.
type Client struct {
	cfg  Config
	http *http.Client
	auth string
}

// NewClient returns a client that sends its requests through hc.
func NewClient(cfg Config, hc *http.Client) (*Client, error) {
	if u, err := url.Parse(cfg.BaseURL); err != nil || cfg.BaseURL == "" || u.Host == "" {
		return nil, errors.New("invalid base URL")
	}
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	c := &Client{cfg: cfg, http: hc}
	if cfg.Username != "" {
		c.auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(cfg.Username+":"+cfg.Password))
	}
	return c, nil
}

// HasCredentials reports whether requests are authenticated.
func (c *Client) HasCredentials() bool { return c.auth != "" }

// get calls one endpoint under the base URL. It decodes a JSON answer into
// out (unless out is nil) and returns the response headers.
func (c *Client) get(ctx context.Context, path string, params url.Values, out any) (http.Header, error) {
	u := c.cfg.BaseURL + "/" + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	if c.auth != "" {
		req.Header.Set("Authorization", c.auth)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		// Drain a little so the connection can be reused, then close.
		_, _ = io.CopyN(io.Discard, resp.Body, 4096)
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxResponseBytes {
		return nil, ErrResponseTooLarge
	}

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return nil, ErrUnauthorized
	case http.StatusForbidden:
		return nil, ErrForbidden
	}
	if resp.StatusCode >= 400 {
		msg, bare := errorMessage(resp.StatusCode, body)
		return nil, &APIError{Status: resp.StatusCode, Message: msg, bare: bare}
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return nil, fmt.Errorf("%w: the answer to %s is not JSON", ErrNotNexus, path)
		}
	}
	return resp.Header, nil
}

// errorMessage reads the three shapes Nexus reports errors in:
//
//	[{"id":"sort","message":"Invalid sort field"}]
//	{"error":"metric parameter is required"}  or  {"message":"..."}
//	{"status-message":"NOT_FOUND","status-code":404}
//
// The second result is true when the body carried no message.
func errorMessage(status int, body []byte) (string, bool) {
	var list []struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &list) == nil && len(list) > 0 {
		msgs := make([]string, 0, len(list))
		for _, e := range list {
			if e.Message != "" {
				msgs = append(msgs, e.Message)
			}
		}
		if len(msgs) > 0 {
			return cleanMessage(strings.Join(msgs, "; ")), false
		}
	}
	var obj struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &obj) == nil {
		if obj.Error != "" {
			return cleanMessage(obj.Error), false
		}
		if obj.Message != "" {
			return cleanMessage(obj.Message), false
		}
	}
	if status == http.StatusNotFound {
		return "not found", true
	}
	return fmt.Sprintf("Nexus answered HTTP %d", status), true
}

// cleanMessage keeps an API message to one bounded line.
func cleanMessage(msg string) string {
	msg = strings.Join(strings.Fields(msg), " ")
	if len(msg) > 500 {
		msg = msg[:500] + "…"
	}
	return msg
}

func isNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.NotFound()
}

// Status says what answered the status check.
type Status struct {
	// Version is empty when the Server header is hidden by a proxy.
	Version string
	// Edition is COMMUNITY, PRO or OSS as Nexus reports it, or empty.
	Edition string
}

// Nexus identifies itself in the Server header: "Nexus/3.98.3-01 (COMMUNITY)".
var serverPattern = regexp.MustCompile(`^Nexus/(\S+)(?: \(([^)]+)\))?`)

// Status checks that Nexus is up and able to serve reads. The endpoint needs
// no authentication and has an empty body.
func (c *Client) Status(ctx context.Context) (*Status, error) {
	h, err := c.get(ctx, "service/rest/v1/status", nil, nil)
	if isNotFound(err) {
		return nil, fmt.Errorf("%w: no status endpoint", ErrNotNexus)
	}
	if err != nil {
		return nil, err
	}
	s := &Status{}
	if m := serverPattern.FindStringSubmatch(h.Get("Server")); m != nil {
		s.Version, s.Edition = m[1], m[2]
	}
	return s, nil
}

// Repository is one repository with the settings worth showing.
type Repository struct {
	Name          string
	Format        string
	Type          string
	URL           string
	Online        bool
	RemoteURL     string
	BlobStore     string
	WritePolicy   string
	VersionPolicy string
	Members       []string
	Cleanup       []string
}

type repositoryItem struct {
	Name       string `json:"name"`
	Format     string `json:"format"`
	Type       string `json:"type"`
	URL        string `json:"url"`
	Online     *bool  `json:"online"`
	Attributes struct {
		Proxy struct {
			RemoteURL string `json:"remoteUrl"`
		} `json:"proxy"`
	} `json:"attributes"`
}

type repositorySettings struct {
	Name    string `json:"name"`
	Online  *bool  `json:"online"`
	Storage *struct {
		BlobStoreName string `json:"blobStoreName"`
		WritePolicy   string `json:"writePolicy"`
	} `json:"storage"`
	Cleanup *struct {
		PolicyNames []string `json:"policyNames"`
	} `json:"cleanup"`
	Proxy *struct {
		RemoteURL string `json:"remoteUrl"`
	} `json:"proxy"`
	Group *struct {
		MemberNames []string `json:"memberNames"`
	} `json:"group"`
	Maven *struct {
		VersionPolicy string `json:"versionPolicy"`
	} `json:"maven"`
}

// Repositories lists the repositories the user can browse, sorted by name.
// The list endpoint has the format of every repository; the settings
// endpoint adds storage and policies. It answers with an empty list to a
// user without the nx-repository-admin-*-*-read privilege, which leaves
// those details blank.
func (c *Client) Repositories(ctx context.Context) ([]Repository, error) {
	var items []repositoryItem
	if _, err := c.get(ctx, "service/rest/v1/repositories", nil, &items); err != nil {
		if isNotFound(err) {
			return nil, fmt.Errorf("%w: no repositories endpoint", ErrNotNexus)
		}
		return nil, err
	}
	repos := make([]Repository, 0, len(items))
	index := make(map[string]int, len(items))
	for _, it := range items {
		index[it.Name] = len(repos)
		repos = append(repos, Repository{
			Name:      it.Name,
			Format:    it.Format,
			Type:      it.Type,
			URL:       it.URL,
			Online:    it.Online == nil || *it.Online,
			RemoteURL: it.Attributes.Proxy.RemoteURL,
		})
	}

	var settings []repositorySettings
	if _, err := c.get(ctx, "service/rest/v1/repositorySettings", nil, &settings); err == nil {
		for _, s := range settings {
			i, ok := index[s.Name]
			if !ok {
				continue
			}
			r := &repos[i]
			if s.Online != nil {
				r.Online = *s.Online
			}
			if s.Storage != nil {
				r.BlobStore, r.WritePolicy = s.Storage.BlobStoreName, s.Storage.WritePolicy
			}
			if s.Cleanup != nil {
				r.Cleanup = s.Cleanup.PolicyNames
			}
			if s.Proxy != nil && s.Proxy.RemoteURL != "" {
				r.RemoteURL = s.Proxy.RemoteURL
			}
			if s.Group != nil {
				r.Members = s.Group.MemberNames
			}
			if s.Maven != nil {
				r.VersionPolicy = s.Maven.VersionPolicy
			}
		}
	} else if ctx.Err() != nil {
		return nil, err
	}
	sort.SliceStable(repos, func(i, j int) bool { return repos[i].Name < repos[j].Name })
	return repos, nil
}

// BlobStore is one blob store.
type BlobStore struct {
	Name                  string `json:"name"`
	Type                  string `json:"type"`
	Unavailable           bool   `json:"unavailable"`
	BlobCount             int64  `json:"blobCount"`
	TotalSizeInBytes      int64  `json:"totalSizeInBytes"`
	AvailableSpaceInBytes int64  `json:"availableSpaceInBytes"`
	SoftQuota             *struct {
		Type  string `json:"type"`
		Limit int64  `json:"limit"`
	} `json:"softQuota"`
}

// BlobStores lists the blob stores, sorted by name.
func (c *Client) BlobStores(ctx context.Context) ([]BlobStore, error) {
	var stores []BlobStore
	if _, err := c.get(ctx, "service/rest/v1/blobstores", nil, &stores); err != nil {
		return nil, err
	}
	sort.SliceStable(stores, func(i, j int) bool { return stores[i].Name < stores[j].Name })
	return stores, nil
}

// Asset is one file of a component.
type Asset struct {
	ID          string `json:"id"`
	Repository  string `json:"repository"`
	Format      string `json:"format"`
	Path        string `json:"path"`
	DownloadURL string `json:"downloadUrl"`
	ContentType string `json:"contentType"`
	FileSize    int64  `json:"fileSize"`
	// Uploader is only given to Nexus administrators.
	Uploader       *string `json:"uploader"`
	LastModified   string  `json:"lastModified"`
	LastDownloaded *string `json:"lastDownloaded"`
	BlobCreated    string  `json:"blobCreated"`
	Checksum       struct {
		SHA1   string `json:"sha1"`
		SHA256 string `json:"sha256"`
	} `json:"checksum"`
}

// Component is one package or artifact with its assets.
type Component struct {
	ID         string  `json:"id"`
	Repository string  `json:"repository"`
	Format     string  `json:"format"`
	Group      string  `json:"group"`
	Name       string  `json:"name"`
	Version    string  `json:"version"`
	Assets     []Asset `json:"assets"`
}

// SearchFilter narrows a component or asset search. Empty fields are not
// sent. Group, Name and Version accept * as a wildcard.
type SearchFilter struct {
	Repository string
	Format     string
	Keyword    string
	Group      string
	Name       string
	Version    string
	Sort       string
	Descending bool
}

func (f SearchFilter) params() url.Values {
	p := url.Values{}
	for k, v := range map[string]string{
		"repository": f.Repository,
		"format":     f.Format,
		"q":          f.Keyword,
		"group":      f.Group,
		"name":       f.Name,
		"version":    f.Version,
		"sort":       f.Sort,
	} {
		if v != "" {
			p.Set(k, v)
		}
	}
	if f.Sort != "" {
		direction := "asc"
		if f.Descending {
			direction = "desc"
		}
		p.Set("direction", direction)
	}
	return p
}

// search follows continuation tokens until limit items are collected. It
// reports whether Nexus had more.
func search[T any](ctx context.Context, c *Client, path string, f SearchFilter, limit int) ([]T, bool, error) {
	params := f.params()
	var out []T
	for page := 0; page < maxPages; page++ {
		var res struct {
			Items             []T     `json:"items"`
			ContinuationToken *string `json:"continuationToken"`
		}
		if _, err := c.get(ctx, path, params, &res); err != nil {
			// A search in a repository that does not exist, or that the
			// user may not see, is a bare 400 or 404 without a message.
			var apiErr *APIError
			if f.Repository != "" && errors.As(err, &apiErr) && apiErr.bare {
				return nil, false, &APIError{Status: http.StatusNotFound, Message: fmt.Sprintf("the repository %q does not exist, or this login may not search it", f.Repository)}
			}
			return nil, false, err
		}
		out = append(out, res.Items...)
		more := res.ContinuationToken != nil && *res.ContinuationToken != ""
		if len(out) >= limit {
			return out[:limit], more || len(out) > limit, nil
		}
		if !more || len(res.Items) == 0 {
			return out, false, nil
		}
		params.Set("continuationToken", *res.ContinuationToken)
	}
	return out, true, nil
}

// Components searches components.
func (c *Client) Components(ctx context.Context, f SearchFilter, limit int) ([]Component, bool, error) {
	return search[Component](ctx, c, "service/rest/v1/search", f, limit)
}

// Assets searches assets.
func (c *Client) Assets(ctx context.Context, f SearchFilter, limit int) ([]Asset, bool, error) {
	return search[Asset](ctx, c, "service/rest/v1/search/assets", f, limit)
}

// Task is one scheduled task.
type Task struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Type          string  `json:"type"`
	TypeName      string  `json:"typeName"`
	Message       *string `json:"message"`
	CurrentState  string  `json:"currentState"`
	LastRunResult *string `json:"lastRunResult"`
	NextRun       *string `json:"nextRun"`
	LastRun       *string `json:"lastRun"`
	Schedule      string  `json:"schedule"`
	Enabled       *bool   `json:"enabled"`
}

// Tasks lists the scheduled tasks, sorted by name.
func (c *Client) Tasks(ctx context.Context) ([]Task, error) {
	tasks, _, err := search[Task](ctx, c, "service/rest/v1/tasks", SearchFilter{}, maxPages*1000)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].Name < tasks[j].Name })
	return tasks, nil
}

// HealthCheck is one system status check.
type HealthCheck struct {
	Name      string  `json:"-"`
	Healthy   bool    `json:"healthy"`
	Message   *string `json:"message"`
	Timestamp string  `json:"timestamp"`
}

// HealthChecks returns the system status checks, sorted by name.
func (c *Client) HealthChecks(ctx context.Context) ([]HealthCheck, error) {
	var raw map[string]HealthCheck
	if _, err := c.get(ctx, "service/rest/v1/status/check", nil, &raw); err != nil {
		return nil, err
	}
	checks := make([]HealthCheck, 0, len(raw))
	for name, check := range raw {
		check.Name = name
		checks = append(checks, check)
	}
	sort.Slice(checks, func(i, j int) bool { return checks[i].Name < checks[j].Name })
	return checks, nil
}

// UsagePoint is the usage of one day or month.
type UsagePoint struct {
	Time  time.Time
	Value float64
}

// UsageHistory returns the request or component count per day or per month,
// oldest first. metric is "requests" or "components"; period is "daily" or
// "monthly".
func (c *Client) UsageHistory(ctx context.Context, metric, period string) ([]UsagePoint, error) {
	var res struct {
		Data []struct {
			Date  string  `json:"date"`
			Value float64 `json:"value"`
		} `json:"data"`
	}
	params := url.Values{"metric": {metric}, "period": {period}}
	if _, err := c.get(ctx, "service/rest/v1/usage-history", params, &res); err != nil {
		if isNotFound(err) {
			return nil, ErrNoUsage
		}
		return nil, err
	}
	// A new server repeats one date for days it has no record of; the last
	// value for a date is the current one.
	byTime := map[time.Time]float64{}
	for _, d := range res.Data {
		if t, ok := ParseTime(d.Date); ok {
			byTime[t] = d.Value
		}
	}
	points := make([]UsagePoint, 0, len(byTime))
	for t, v := range byTime {
		points = append(points, UsagePoint{Time: t, Value: v})
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Time.Before(points[j].Time) })
	return points, nil
}

// Month is the usage of one calendar month.
type Month struct {
	MetricDate     string `json:"metricDate"`
	RequestCount   int64  `json:"requestCount"`
	ComponentCount int64  `json:"componentCount"`
	PeakStorage    int64  `json:"peakStorage"`
	ResponseSize   int64  `json:"responseSize"`
}

// MonthlyUsage returns up to the last twelve months, oldest first.
func (c *Client) MonthlyUsage(ctx context.Context) ([]Month, error) {
	var months []Month
	if _, err := c.get(ctx, "service/rest/v1/monthly-metrics", nil, &months); err != nil {
		if isNotFound(err) {
			return nil, ErrNoUsage
		}
		return nil, err
	}
	sort.SliceStable(months, func(i, j int) bool { return months[i].MetricDate < months[j].MetricDate })
	return months, nil
}

var timeLayouts = []string{
	"2006-01-02T15:04:05.000-07:00",
	time.RFC3339Nano,
	"2006-01-02",
	"2006-01",
}

// ParseTime reads the datetime and date forms the API uses.
func ParseTime(s string) (time.Time, bool) {
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}
