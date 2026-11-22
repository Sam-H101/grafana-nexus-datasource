package nexus_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sam-H101/grafana-nexus-datasource/pkg/nexus"
	"github.com/Sam-H101/grafana-nexus-datasource/pkg/nexus/nexustest"
)

var ctx = context.Background()

func client(t *testing.T, f *nexustest.Fake, cfg nexus.Config) *nexus.Client {
	t.Helper()
	cfg.BaseURL = f.URL
	c, err := nexus.NewClient(cfg, f.Client())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNewClientNeedsABaseURL(t *testing.T) {
	for _, u := range []string{"", "nexus", "://x"} {
		if _, err := nexus.NewClient(nexus.Config{BaseURL: u}, nil); err == nil {
			t.Errorf("%q was accepted", u)
		}
	}
}

func TestStatusReadsTheServerHeader(t *testing.T) {
	f := nexustest.New(t)
	c := client(t, f, nexus.Config{})
	s, err := c.Status(ctx)
	if err != nil || s.Version != "3.98.3-01" || s.Edition != "COMMUNITY" {
		t.Fatalf("status = %+v, %v", s, err)
	}
	if f.Calls("v1/status")[0].Auth != "" {
		t.Error("an anonymous client sent credentials")
	}

	// Behind a proxy that replaces the header, the version is unknown.
	f.ServerHeader = "nginx"
	if s, err := c.Status(ctx); err != nil || s.Version != "" || s.Edition != "" {
		t.Errorf("hidden header: %+v, %v", s, err)
	}
	// An older Nexus has no edition in the header.
	f.ServerHeader = "Nexus/3.70.4-02 (OSS)"
	if s, _ := c.Status(ctx); s.Version != "3.70.4-02" || s.Edition != "OSS" {
		t.Errorf("OSS header: %+v", s)
	}

	// Something that is not Nexus.
	f.Handlers["v1/status"] = func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }
	if _, err := c.Status(ctx); !errors.Is(err, nexus.ErrNotNexus) {
		t.Errorf("404: %v", err)
	}
}

func TestCredentials(t *testing.T) {
	f := nexustest.New(t)
	f.Username, f.Password = "grafana", "s3cret: with colon"

	c := client(t, f, nexus.Config{Username: "grafana", Password: "s3cret: with colon"})
	if !c.HasCredentials() {
		t.Error("HasCredentials is false")
	}
	if _, err := c.Repositories(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.Calls("v1/repositories")[0].Auth; !strings.HasPrefix(got, "Basic ") {
		t.Errorf("Authorization = %q", got)
	}

	wrong := client(t, f, nexus.Config{Username: "grafana", Password: "nope"})
	if _, err := wrong.Repositories(ctx); !errors.Is(err, nexus.ErrUnauthorized) {
		t.Errorf("wrong password: %v", err)
	}
	// Nexus refuses bad credentials even on the open status check.
	if _, err := wrong.Status(ctx); !errors.Is(err, nexus.ErrUnauthorized) {
		t.Errorf("wrong password on status: %v", err)
	}
	anonymous := client(t, f, nexus.Config{})
	if _, err := anonymous.Repositories(ctx); !errors.Is(err, nexus.ErrUnauthorized) {
		t.Errorf("anonymous: %v", err)
	}
	if _, err := anonymous.Status(ctx); err != nil {
		t.Errorf("anonymous status: %v", err)
	}
}

func TestRepositoriesMergeTheListAndTheSettings(t *testing.T) {
	f := nexustest.New(t)
	c := client(t, f, nexus.Config{})
	repos, err := c.Repositories(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]nexus.Repository{}
	names := make([]string, len(repos))
	for i, r := range repos {
		byName[r.Name], names[i] = r, r.Name
	}
	if len(repos) != 10 || names[0] != "docs-archive" || names[9] != "raw-hosted" {
		t.Fatalf("names = %v", names)
	}
	if r := byName["maven-central"]; r.Format != "maven2" || r.Type != "proxy" || r.RemoteURL != "https://repo1.maven.org/maven2/" || r.BlobStore != "default" || r.VersionPolicy != "RELEASE" || !r.Online {
		t.Errorf("maven-central = %+v", r)
	}
	if r := byName["maven-public"]; r.Type != "group" || strings.Join(r.Members, ",") != "maven-releases,maven-snapshots,maven-central" {
		t.Errorf("maven-public = %+v", r)
	}
	// The settings endpoint leaves the format out for some repositories; it
	// comes from the list.
	if r := byName["nuget-hosted"]; r.Format != "nuget" || r.WritePolicy != "ALLOW" {
		t.Errorf("nuget-hosted = %+v", r)
	}
	if r := byName["docs-archive"]; r.Online || r.BlobStore != "artifacts" || r.WritePolicy != "DENY" {
		t.Errorf("docs-archive = %+v", r)
	}

	// Without access to the settings the list still works.
	f.Forbidden["v1/repositorySettings"] = true
	repos, err = c.Repositories(ctx)
	if err != nil || len(repos) != 10 || repos[0].BlobStore != "" {
		t.Errorf("without settings: %d, %v", len(repos), err)
	}
	f.Forbidden["v1/repositories"] = true
	if _, err := c.Repositories(ctx); !errors.Is(err, nexus.ErrForbidden) {
		t.Errorf("forbidden: %v", err)
	}
}

func TestBlobStores(t *testing.T) {
	c := client(t, nexustest.New(t), nexus.Config{})
	stores, err := c.BlobStores(ctx)
	if err != nil || len(stores) != 2 {
		t.Fatalf("%d stores, %v", len(stores), err)
	}
	a, d := stores[0], stores[1]
	if a.Name != "artifacts" || a.SoftQuota == nil || a.SoftQuota.Type != "spaceUsedQuota" || a.SoftQuota.Limit != 10737418240 {
		t.Errorf("artifacts = %+v", a)
	}
	if d.Name != "default" || d.Type != "File" || d.Unavailable || d.BlobCount != 49 || d.TotalSizeInBytes != 902286 || d.SoftQuota != nil {
		t.Errorf("default = %+v", d)
	}
}

func TestSearchSendsOnlyWhatIsSet(t *testing.T) {
	f := nexustest.New(t)
	c := client(t, f, nexus.Config{})
	if _, _, err := c.Components(ctx, nexus.SearchFilter{}, 100); err != nil {
		t.Fatal(err)
	}
	if q := f.Calls("v1/search")[0].Query; len(q) != 0 {
		t.Errorf("empty filter sent %v", q)
	}
	f.Reset()
	filter := nexus.SearchFilter{Repository: "maven-releases", Format: "maven2", Keyword: "bill", Group: "com.example", Name: "bill*", Version: "1.*", Sort: "version", Descending: true}
	if _, _, err := c.Assets(ctx, filter, 100); err != nil {
		t.Fatal(err)
	}
	q := f.Calls("v1/search/assets")[0].Query
	for k, want := range map[string]string{"repository": "maven-releases", "format": "maven2", "q": "bill", "group": "com.example", "name": "bill*", "version": "1.*", "sort": "version", "direction": "desc"} {
		if q.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
	// Without a sort there is no direction either.
	f.Reset()
	_, _, _ = c.Components(ctx, nexus.SearchFilter{Descending: true}, 1)
	if f.Calls("v1/search")[0].Query.Has("direction") {
		t.Error("a direction was sent without a sort")
	}
}

func TestSearchFollowsPagesUpToTheLimit(t *testing.T) {
	f := nexustest.New(t)
	f.PageSize = 3
	c := client(t, f, nexus.Config{})

	all, more, err := c.Components(ctx, nexus.SearchFilter{}, 100)
	if err != nil || len(all) != 8 || more {
		t.Fatalf("all: %d, more=%v, %v", len(all), more, err)
	}
	if n := len(f.Calls("v1/search")); n != 3 {
		t.Errorf("%d requests, want 3", n)
	}
	if c0 := all[3]; c0.Repository != "maven-releases" || c0.Group != "com.example" || c0.Name != "billing" || c0.Version != "1.0.0" || len(c0.Assets) != 2 || c0.Assets[1].FileSize != 5000 {
		t.Errorf("component = %+v", c0)
	}

	f.Reset()
	some, more, err := c.Components(ctx, nexus.SearchFilter{}, 4)
	if err != nil || len(some) != 4 || !more {
		t.Errorf("limit 4: %d, more=%v, %v", len(some), more, err)
	}
	if n := len(f.Calls("v1/search")); n != 2 {
		t.Errorf("limit 4 made %d requests, want 2", n)
	}
	// Exactly a page: Nexus says there is more.
	if _, more, _ := c.Components(ctx, nexus.SearchFilter{}, 3); !more {
		t.Error("limit 3 of 8 reports no more")
	}
}

func TestAssets(t *testing.T) {
	c := client(t, nexustest.New(t), nexus.Config{})
	assets, more, err := c.Assets(ctx, nexus.SearchFilter{Repository: "raw-hosted"}, 10)
	if err != nil || len(assets) != 3 || more {
		t.Fatalf("%d assets, more=%v, %v", len(assets), more, err)
	}
	a := assets[0]
	if a.Path != "/tools/cli/1.1.0/cli-1.1.0.bin" || a.FileSize != 5000 || a.Uploader == nil || *a.Uploader != "admin" || a.ContentType != "application/octet-stream" || a.LastDownloaded == nil || assets[1].LastDownloaded != nil {
		t.Errorf("asset = %+v", a)
	}
	if !strings.HasPrefix(a.DownloadURL, "https://nexus.example.com/repository/raw-hosted/") || len(a.Checksum.SHA1) != 40 {
		t.Errorf("asset url and checksum = %+v", a)
	}
	if created, ok := nexus.ParseTime(a.BlobCreated); !ok || created.Year() != 2026 {
		t.Errorf("blobCreated %q", a.BlobCreated)
	}
}

func TestSearchErrors(t *testing.T) {
	f := nexustest.New(t)
	c := client(t, f, nexus.Config{})
	var apiErr *nexus.APIError

	// Nexus answers a search in an unknown repository with a bare 400.
	_, _, err := c.Components(ctx, nexus.SearchFilter{Repository: "nope"}, 10)
	if !errors.As(err, &apiErr) || !apiErr.NotFound() || !strings.Contains(apiErr.Message, `"nope" does not exist`) {
		t.Errorf("unknown repository: %v", err)
	}
	// Its own messages are passed on.
	_, _, err = c.Components(ctx, nexus.SearchFilter{Repository: "raw-hosted", Sort: "bogus"}, 10)
	if !errors.As(err, &apiErr) || apiErr.Status != 400 || !strings.Contains(apiErr.Message, "Invalid sort field") {
		t.Errorf("bad sort: %v", err)
	}
	f.Forbidden["v1/search"] = true
	if _, _, err := c.Components(ctx, nexus.SearchFilter{}, 10); !errors.Is(err, nexus.ErrForbidden) {
		t.Errorf("forbidden: %v", err)
	}
}

func TestTasks(t *testing.T) {
	c := client(t, nexustest.New(t), nexus.Config{})
	tasks, err := c.Tasks(ctx)
	if err != nil || len(tasks) != 9 {
		t.Fatalf("%d tasks, %v", len(tasks), err)
	}
	if tasks[0].Name != "Cleanup service" || tasks[0].CurrentState != "WAITING" || tasks[0].LastRunResult != nil || tasks[0].NextRun == nil {
		t.Errorf("first task = %+v", tasks[0])
	}
	ran := 0
	for _, task := range tasks {
		if task.LastRunResult != nil && *task.LastRunResult == "OK" && task.LastRun != nil {
			ran++
		}
	}
	if ran != 5 {
		t.Errorf("%d tasks have run, want 5", ran)
	}
}

func TestHealthChecks(t *testing.T) {
	c := client(t, nexustest.New(t), nexus.Config{})
	checks, err := c.HealthChecks(ctx)
	if err != nil || len(checks) < 15 {
		t.Fatalf("%d checks, %v", len(checks), err)
	}
	var failing []string
	for i, check := range checks {
		if i > 0 && checks[i-1].Name > check.Name {
			t.Fatalf("not sorted at %q", check.Name)
		}
		if !check.Healthy {
			failing = append(failing, check.Name)
		}
	}
	if strings.Join(failing, "|") != "Baseline Telemetry|Default Secret Encryption Key" {
		t.Errorf("failing = %v", failing)
	}
}

func TestUsageHistory(t *testing.T) {
	f := nexustest.New(t)
	c := client(t, f, nexus.Config{})

	// A new server repeats a date; the last value for it wins.
	daily, err := c.UsageHistory(ctx, "requests", "daily")
	if err != nil || len(daily) != 7 {
		t.Fatalf("daily: %d points, %v", len(daily), err)
	}
	if !daily[0].Time.Equal(time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)) || daily[1].Value != 1250 || daily[6].Value != 95 {
		t.Errorf("daily = %+v", daily)
	}
	monthly, err := c.UsageHistory(ctx, "components", "monthly")
	if err != nil || len(monthly) != 3 || !monthly[2].Time.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) || monthly[2].Value != 512 {
		t.Errorf("monthly = %+v, %v", monthly, err)
	}
	q := f.Calls("v1/usage-history")[1].Query
	if q.Get("metric") != "components" || q.Get("period") != "monthly" {
		t.Errorf("query = %v", q)
	}

	months, err := c.MonthlyUsage(ctx)
	if err != nil || len(months) != 2 || months[0].MetricDate != "2026-10-01T00:00:00Z" || months[1].RequestCount != 7380 || months[1].PeakStorage != 8672860 {
		t.Errorf("months = %+v, %v", months, err)
	}

	// Before Community Edition the endpoints do not exist.
	f.Missing["v1/usage-history"], f.Missing["v1/monthly-metrics"] = true, true
	if _, err := c.UsageHistory(ctx, "requests", "daily"); !errors.Is(err, nexus.ErrNoUsage) {
		t.Errorf("missing usage history: %v", err)
	}
	if _, err := c.MonthlyUsage(ctx); !errors.Is(err, nexus.ErrNoUsage) {
		t.Errorf("missing monthly metrics: %v", err)
	}
}

func TestMetrics(t *testing.T) {
	f := nexustest.New(t)
	c := client(t, f, nexus.Config{Username: "u", Password: "p"})
	m, err := c.Metrics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The old address redirects, and the credentials go along.
	if calls := f.Calls("metrics/data"); len(calls) != 1 || calls[0].Auth == "" {
		t.Errorf("redirected calls = %+v", calls)
	}

	v := m.Values()
	for _, name := range []string{
		"jvm.memory.heap.used",
		"nexus.analytics.component_total_count",
		"org.sonatype.nexus.metrics.InstrumentedFilter.activeRequests",
		"org.sonatype.nexus.metrics.InstrumentedFilter.responseCodes.ok.count",
		"org.sonatype.nexus.metrics.InstrumentedFilter.responseCodes.ok.m1_rate",
		"org.sonatype.nexus.blobstore.file.FileBlobStore.create.timer.p95",
	} {
		if _, ok := v[name]; !ok {
			t.Errorf("no value for %s", name)
		}
	}
	// A boolean gauge is 0 or 1; text and structured gauges are left out.
	if got, ok := v["org.sonatype.nexus.internal.metrics.ReadOnlyMetricSet.enabled"]; !ok || got != 0 {
		t.Errorf("read-only gauge = %v, %v", got, ok)
	}
	for _, name := range []string{"jvm.vm.name", "nexus.analytics.edition", "nexus.analytics.repository_type_counts", "org.sonatype.nexus.metrics.InstrumentedFilter.responseCodes.ok.units"} {
		if _, ok := v[name]; ok {
			t.Errorf("%s should not be a number", name)
		}
	}

	// Nexus before 3.77 answers 401 to a user without the privilege: it is
	// reported as forbidden, unless the password is wrong everywhere.
	f.Username, f.Password = "u", "p"
	f.Handlers["metrics/data"] = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }
	if _, err := c.Metrics(ctx); !errors.Is(err, nexus.ErrForbidden) {
		t.Errorf("401 with a working login: %v", err)
	}
	if _, err := client(t, f, nexus.Config{Username: "u", Password: "wrong"}).Metrics(ctx); !errors.Is(err, nexus.ErrUnauthorized) {
		t.Errorf("401 with a wrong password: %v", err)
	}

	formats, found := m.Formats()
	if !found {
		t.Fatal("no format gauges")
	}
	byFormat := map[string]nexus.FormatStats{}
	for _, s := range formats {
		byFormat[s.Format] = s
	}
	if s := byFormat["maven2"]; s.Repositories != 4 || s.Components < 2 || s.BytesDownloaded != 27018 {
		t.Errorf("maven2 = %+v", s)
	}
	if s := byFormat["raw"]; s.Repositories < 1 || s.Components != 3 || s.BytesUploaded == 0 {
		t.Errorf("raw = %+v", s)
	}
	// Formats that are listed with zero bytes and have no repository are
	// left out.
	if _, ok := byFormat["apt"]; ok {
		t.Error("apt is listed although unused")
	}

	if _, found := (&nexus.MetricsData{}).Formats(); found {
		t.Error("an empty registry reports format gauges")
	}
}

func TestErrorShapes(t *testing.T) {
	f := nexustest.New(t)
	c := client(t, f, nexus.Config{})
	var apiErr *nexus.APIError

	_, err := c.UsageHistory(ctx, "requests", "hourly")
	if !errors.As(err, &apiErr) || apiErr.Message != "Invalid period. Use 'daily' or 'monthly'" {
		t.Errorf(`{"error"} shape: %v`, err)
	}
	f.Handlers["v1/blobstores"] = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("<html>boom</html>"))
	}
	if _, err := c.BlobStores(ctx); !errors.As(err, &apiErr) || apiErr.Message != "Nexus answered HTTP 500" {
		t.Errorf("html error: %v", err)
	}
	f.Handlers["v1/blobstores"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>login</html>"))
	}
	if _, err := c.BlobStores(ctx); !errors.Is(err, nexus.ErrNotNexus) {
		t.Errorf("html with 200: %v", err)
	}
}

func TestParseTime(t *testing.T) {
	for in, want := range map[string]time.Time{
		"2026-11-22T00:00:04.497+00:00": time.Date(2026, 11, 22, 0, 0, 4, 497000000, time.UTC),
		"2026-11-21T23:59:35.230Z":      time.Date(2026, 11, 21, 23, 59, 35, 230000000, time.UTC),
		"2026-11-01T02:00:00.000+02:00": time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		"2026-11-21":                    time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC),
		"2026-11":                       time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
	} {
		if got, ok := nexus.ParseTime(in); !ok || !got.Equal(want) {
			t.Errorf("%q: %v, %v", in, got, ok)
		}
	}
	if _, ok := nexus.ParseTime("yesterday"); ok {
		t.Error("nonsense was parsed")
	}
}
