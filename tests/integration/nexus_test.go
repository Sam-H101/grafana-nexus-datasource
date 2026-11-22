//go:build integration

// Package integration runs the plugin against a real server: the dev stack's
// Nexus Repository with its seeded repositories, artifacts and users
// (devenv/nexus/seed.py).
//
//	NEXUS_URL       default http://nexus:8081
//	NEXUS_SEED_DIR  default /run/nexus: the passwords the seed wrote
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sam-H101/grafana-nexus-datasource/pkg/plugin"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func nexusURL() string { return env("NEXUS_URL", "http://nexus:8081") }

func password(t *testing.T, file string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(env("NEXUS_SEED_DIR", "/run/nexus"), file))
	if err != nil {
		t.Fatalf("no password: start the dev stack, or set NEXUS_SEED_DIR (%v)", err)
	}
	return strings.TrimSpace(string(b))
}

func datasource(t *testing.T, url, user, pass string) *plugin.Datasource {
	t.Helper()
	js, _ := json.Marshal(map[string]any{"username": user})
	secure := map[string]string{}
	if pass != "" {
		secure["password"] = pass
	}
	inst, err := plugin.NewDatasource(context.Background(), backend.DataSourceInstanceSettings{URL: url, JSONData: js, DecryptedSecureJSONData: secure})
	if err != nil {
		t.Fatal(err)
	}
	return inst.(*plugin.Datasource)
}

// reader has every privilege the plugin can use; limited may only browse and
// search.
func reader(t *testing.T) *plugin.Datasource {
	return datasource(t, nexusURL(), "grafana", password(t, "password"))
}

func limited(t *testing.T) *plugin.Datasource {
	return datasource(t, nexusURL(), "grafana-limited", password(t, "limited-password"))
}

func query(t *testing.T, ds *plugin.Datasource, q map[string]any) backend.DataResponse {
	t.Helper()
	js, _ := json.Marshal(q)
	now := time.Now()
	resp, err := ds.QueryData(context.Background(), &backend.QueryDataRequest{Queries: []backend.DataQuery{{
		RefID: "A", JSON: js, TimeRange: backend.TimeRange{From: now.AddDate(0, 0, -7), To: now},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return resp.Responses["A"]
}

func frame(t *testing.T, ds *plugin.Datasource, q map[string]any) *data.Frame {
	t.Helper()
	r := query(t, ds, q)
	if r.Error != nil {
		t.Fatalf("%v: %v", q, r.Error)
	}
	if len(r.Frames) != 1 {
		t.Fatalf("%v: %d frames", q, len(r.Frames))
	}
	return r.Frames[0]
}

func column(t *testing.T, f *data.Frame, name string) []string {
	t.Helper()
	field, _ := f.FieldByName(name)
	if field == nil {
		t.Fatalf("no field %q", name)
	}
	out := make([]string, field.Len())
	for i := range out {
		if v, ok := field.ConcreteAt(i); ok {
			out[i] = fmt.Sprint(v)
		}
	}
	return out
}

// row returns the index of the first row whose column has the value.
func row(t *testing.T, f *data.Frame, name, value string) int {
	t.Helper()
	for i, v := range column(t, f, name) {
		if v == value {
			return i
		}
	}
	t.Fatalf("no row with %s = %q in %v", name, value, column(t, f, name))
	return -1
}

func notices(f *data.Frame) string {
	if f.Meta == nil {
		return ""
	}
	var out []string
	for _, n := range f.Meta.Notices {
		out = append(out, n.Text)
	}
	return strings.Join(out, " | ")
}

func health(t *testing.T, ds *plugin.Datasource) *backend.CheckHealthResult {
	t.Helper()
	res, err := ds.CheckHealth(context.Background(), &backend.CheckHealthRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestHealth(t *testing.T) {
	res := health(t, reader(t))
	if res.Status != backend.HealthStatusOk || !strings.HasPrefix(res.Message, "Connected to Nexus Repository 3.") || !strings.Contains(res.Message, "The user grafana can see") || strings.Contains(res.Message, "Not readable") {
		t.Errorf("reader: %v %q", res.Status, res.Message)
	}

	res = health(t, limited(t))
	if res.Status != backend.HealthStatusOk {
		t.Fatalf("limited: %v %q", res.Status, res.Message)
	}
	for _, want := range []string{"blob stores (nx-blobstores-read)", "tasks (nx-tasks-read)", "(nx-metrics-all)"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("limited: %q lacks %q", res.Message, want)
		}
	}
	if strings.Contains(res.Message, "nx-search-read") {
		t.Errorf("limited may search, but: %q", res.Message)
	}

	res = health(t, datasource(t, nexusURL(), "grafana", "not-the-password"))
	if res.Status != backend.HealthStatusError || !strings.Contains(res.Message, "rejected the user name or password") || strings.Contains(res.Message, "not-the-password") {
		t.Errorf("wrong password: %q", res.Message)
	}
	res = health(t, datasource(t, nexusURL(), "", ""))
	if res.Status != backend.HealthStatusError || !strings.Contains(res.Message, "requires a login") {
		t.Errorf("anonymous: %q", res.Message)
	}
	res = health(t, datasource(t, nexusURL()+"/service/rest/v1/", "grafana", password(t, "password")))
	if res.Status != backend.HealthStatusOk {
		t.Errorf("an API address as the URL: %q", res.Message)
	}
	res = health(t, datasource(t, nexusURL()+"/nexus", "grafana", password(t, "password")))
	if res.Status != backend.HealthStatusError || !strings.Contains(res.Message, "did not answer like Nexus") {
		t.Errorf("wrong path: %q", res.Message)
	}
	res = health(t, datasource(t, "http://nexus.invalid:8081", "", ""))
	if res.Status != backend.HealthStatusError || !strings.Contains(res.Message, "Cannot reach Nexus") {
		t.Errorf("unknown host: %q", res.Message)
	}
}

func TestRepositories(t *testing.T) {
	ds := reader(t)
	f := frame(t, ds, map[string]any{"queryType": "repositories"})
	i := row(t, f, "name", "raw-hosted")
	if column(t, f, "format")[i] != "raw" || column(t, f, "type")[i] != "hosted" || column(t, f, "online")[i] != "true" || column(t, f, "blob_store")[i] != "artifacts" {
		t.Errorf("raw-hosted: %v", f.RowCopy(i))
	}
	i = row(t, f, "name", "docs-archive")
	if column(t, f, "online")[i] != "false" || column(t, f, "write_policy")[i] != "DENY" {
		t.Errorf("docs-archive: %v", f.RowCopy(i))
	}
	i = row(t, f, "name", "maven-central")
	if column(t, f, "type")[i] != "proxy" || !strings.HasPrefix(column(t, f, "remote_url")[i], "https://repo1.maven.org") {
		t.Errorf("maven-central: %v", f.RowCopy(i))
	}
	i = row(t, f, "name", "maven-public")
	if !strings.Contains(column(t, f, "members")[i], "maven-releases") {
		t.Errorf("maven-public: %v", f.RowCopy(i))
	}

	f = frame(t, ds, map[string]any{"queryType": "repositories", "format": "raw", "repositoryType": "hosted"})
	if got := strings.Join(column(t, f, "name"), ","); got != "docs-archive,raw-hosted" {
		t.Errorf("raw hosted = %s", got)
	}
	// The limited user sees the same list.
	f = frame(t, limited(t), map[string]any{"queryType": "repositories", "repositories": []string{"npm-proxy"}})
	if f.Rows() != 1 || column(t, f, "format")[0] != "npm" || column(t, f, "blob_store")[0] != "" || !strings.Contains(notices(f), "nx-repository-admin-*-*-read") {
		t.Errorf("limited: %d rows, %q", f.Rows(), notices(f))
	}
}

func TestBlobStores(t *testing.T) {
	f := frame(t, reader(t), map[string]any{"queryType": "blobStores"})
	if got := strings.Join(column(t, f, "name"), ","); got != "artifacts,default" {
		t.Fatalf("names = %s", got)
	}
	if column(t, f, "available")[0] != "true" || column(t, f, "quota")[0] != "10737418240" || column(t, f, "quota_type")[0] != "spaceUsedQuota" || column(t, f, "quota")[1] != "" {
		t.Errorf("quota: %v / %v", f.RowCopy(0), f.RowCopy(1))
	}
	// The raw artifacts live in the "artifacts" store, the Maven ones in
	// "default".
	for i, name := range column(t, f, "name") {
		if column(t, f, "blobs")[i] == "0" || column(t, f, "size")[i] == "0" || column(t, f, "free")[i] == "0" {
			t.Errorf("%s is empty: %v", name, f.RowCopy(i))
		}
	}
}

func TestBlobStoreNumbers(t *testing.T) {
	f := frame(t, reader(t), map[string]any{"queryType": "blobStores", "blobStoreValue": "free"})
	if f.Rows() != 1 || len(f.Fields) != 3 || f.Fields[1].Labels["blob_store"] != "artifacts" || f.Fields[2].Labels["blob_store"] != "default" {
		t.Fatalf("free space: %v", f.Fields)
	}
	if free, _ := f.Fields[1].ConcreteAt(0); free.(float64) < 1e6 {
		t.Errorf("free = %v", free)
	}
}

func TestComponents(t *testing.T) {
	ds := reader(t)
	f := frame(t, ds, map[string]any{"queryType": "components", "repositories": []string{"maven-releases"}, "name": "billing", "sort": "version"})
	if got := strings.Join(column(t, f, "version"), ","); got != "1.0.0,1.1.0,1.2.0,2.0.0" {
		t.Fatalf("billing versions = %s", got)
	}
	if column(t, f, "group")[0] != "com.example.shop" || column(t, f, "format")[0] != "maven2" || column(t, f, "assets")[0] == "0" || column(t, f, "last_updated")[0] == "" {
		t.Errorf("billing 1.0.0: %v", f.RowCopy(0))
	}
	// Only 2.0.0 was downloaded by the seed.
	if column(t, f, "last_downloaded")[0] != "" || column(t, f, "last_downloaded")[3] == "" {
		t.Errorf("last downloaded = %v", column(t, f, "last_downloaded"))
	}

	f = frame(t, ds, map[string]any{"queryType": "components", "group": "com.example.*", "sort": "name", "descending": true})
	names := column(t, f, "name")
	if f.Rows() != 9 || names[0] != "checkout" || names[8] != "auth-client" {
		t.Errorf("com.example.*: %d rows, %v", f.Rows(), names)
	}
	f = frame(t, ds, map[string]any{"queryType": "components", "format": "raw"})
	if f.Rows() != 5 {
		t.Errorf("raw components: %d", f.Rows())
	}
	f = frame(t, ds, map[string]any{"queryType": "components", "keyword": "auth-client", "limit": 2})
	if f.Rows() != 2 || !strings.Contains(notices(f), "Showing the first 2 components") {
		t.Errorf("limit: %d rows, %q", f.Rows(), notices(f))
	}
	// Two repositories: one search each.
	f = frame(t, ds, map[string]any{"queryType": "components", "repositories": []string{"raw-hosted", "maven-releases"}, "limit": 1000})
	if f.Rows() != 14 || column(t, f, "repository")[0] != "raw-hosted" {
		t.Errorf("two repositories: %d rows", f.Rows())
	}
	f = frame(t, ds, map[string]any{"queryType": "components", "name": "no-such-component"})
	if f.Rows() != 0 || notices(f) != "No matching components." {
		t.Errorf("no match: %d rows, %q", f.Rows(), notices(f))
	}

	r := query(t, ds, map[string]any{"queryType": "components", "repositories": []string{"no-such-repository"}})
	if r.Error == nil || !strings.Contains(r.Error.Error(), `"no-such-repository" does not exist`) {
		t.Errorf("unknown repository: %v", r.Error)
	}
	// The limited user may search too.
	if f := frame(t, limited(t), map[string]any{"queryType": "components", "name": "billing"}); f.Rows() != 4 {
		t.Errorf("limited: %d rows", f.Rows())
	}
}

func TestAssets(t *testing.T) {
	ds := reader(t)
	f := frame(t, ds, map[string]any{"queryType": "assets", "repositories": []string{"raw-hosted"}, "limit": 1000})
	if f.Rows() != 5 || f.Fields[0].Name != "uploaded" {
		t.Fatalf("%d raw assets", f.Rows())
	}
	i := row(t, f, "path", "/tools/cli/1.3.0/cli-1.3.0.bin")
	// Nexus names the uploader to administrators only.
	if column(t, f, "uploaded_by")[i] != "" || column(t, f, "size")[i] == "0" || column(t, f, "last_downloaded")[i] == "" || len(column(t, f, "sha1")[i]) != 40 {
		t.Errorf("cli 1.3.0: %v", f.RowCopy(i))
	}
	if !strings.HasSuffix(column(t, f, "url")[i], "/repository/raw-hosted/tools/cli/1.3.0/cli-1.3.0.bin") {
		t.Errorf("url = %s", column(t, f, "url")[i])
	}
	uploaded, _ := f.Fields[0].ConcreteAt(i)
	if age := time.Since(uploaded.(time.Time)); age < 0 || age > 365*24*time.Hour {
		t.Errorf("uploaded %v ago", age)
	}
	if j := row(t, f, "path", "/tools/cli/1.1.0/cli-1.1.0.bin"); column(t, f, "last_downloaded")[j] != "" {
		t.Error("cli 1.1.0 was never downloaded but has a download time")
	}

	// A Maven version has a pom and a jar.
	f = frame(t, ds, map[string]any{"queryType": "assets", "name": "billing", "version": "2.0.0"})
	if f.Rows() != 2 {
		t.Errorf("billing 2.0.0: %d assets", f.Rows())
	}
}

func TestTasks(t *testing.T) {
	f := frame(t, reader(t), map[string]any{"queryType": "tasks"})
	if f.Rows() == 0 {
		t.Fatal("no tasks")
	}
	i := row(t, f, "name", "Cleanup service")
	if column(t, f, "state")[i] == "" || column(t, f, "next_run")[i] == "" || column(t, f, "enabled")[i] != "true" {
		t.Errorf("cleanup service: %v", f.RowCopy(i))
	}
}

func TestHealthChecks(t *testing.T) {
	ds := reader(t)
	f := frame(t, ds, map[string]any{"queryType": "healthChecks"})
	if f.Rows() < 10 {
		t.Fatalf("%d checks", f.Rows())
	}
	healthy := column(t, f, "healthy")
	failing := 0
	for i, h := range healthy {
		if h == "false" {
			failing++
		}
		if i > 0 && healthy[i-1] == "true" && h == "false" {
			t.Error("a failing check comes after a passing one")
		}
		if msg := column(t, f, "message")[i]; strings.ContainsAny(msg, "<>") {
			t.Errorf("HTML left in %q", msg)
		}
	}
	if i := row(t, f, "check", "Blob Stores Ready"); healthy[i] != "true" || column(t, f, "checked")[i] == "" {
		t.Errorf("Blob Stores Ready: %v", f.RowCopy(i))
	}
	only := frame(t, ds, map[string]any{"queryType": "healthChecks", "unhealthyOnly": true})
	if only.Rows() != failing {
		t.Errorf("failing only: %d rows, want %d", only.Rows(), failing)
	}
}

func TestUsage(t *testing.T) {
	ds := reader(t)
	for _, metric := range []string{"requests", "components"} {
		for _, period := range []string{"daily", "monthly"} {
			f := frame(t, ds, map[string]any{"queryType": "usage", "usageMetric": metric, "usagePeriod": period})
			if len(f.Fields) != 2 || f.Fields[0].Type() != data.FieldTypeTime || f.Fields[1].Name != metric {
				t.Errorf("%s %s: fields %v", metric, period, f.Fields)
			}
			// No date twice, oldest first.
			var last time.Time
			for i := 0; i < f.Rows(); i++ {
				at, _ := f.Fields[0].ConcreteAt(i)
				if !at.(time.Time).After(last) {
					t.Errorf("%s %s: %v does not follow %v", metric, period, at, last)
				}
				last = at.(time.Time)
			}
		}
	}
	f := frame(t, ds, map[string]any{"queryType": "monthlyUsage"})
	if f.Rows() < 1 || f.Fields[0].Name != "month" {
		t.Fatalf("monthly usage: %d rows", f.Rows())
	}
	for _, name := range []string{"requests", "components", "peak_storage", "egress"} {
		column(t, f, name)
	}
}

func TestFormatsAndMetrics(t *testing.T) {
	ds := reader(t)
	f := frame(t, ds, map[string]any{"queryType": "formats"})
	if notices(f) != "" {
		t.Fatalf("formats: %q", notices(f))
	}
	i := row(t, f, "format", "maven2")
	if column(t, f, "repositories")[i] != "4" {
		t.Errorf("maven2: %v", f.RowCopy(i))
	}
	row(t, f, "format", "raw")

	f = frame(t, ds, map[string]any{"queryType": "metrics", "metrics": []string{"jvm.memory.heap.used", "jvm.memory.heap.max", "nexus.analytics.repository_total_count", "no.such.metric"}})
	if f.Rows() != 1 || len(f.Fields) != 4 || f.Fields[1].Labels["metric"] != "jvm.memory.heap.used" || !strings.Contains(notices(f), "no.such.metric") {
		t.Fatalf("metrics: %d fields, %q", len(f.Fields), notices(f))
	}
	if used, _ := f.Fields[1].ConcreteAt(0); used.(float64) < 1e6 {
		t.Errorf("heap used = %v", used)
	}
	if repos, _ := f.Fields[3].ConcreteAt(0); repos.(float64) < 10 {
		t.Errorf("repository count = %v", repos)
	}
	all := frame(t, ds, map[string]any{"queryType": "metrics"})
	if all.Rows() < 100 {
		t.Errorf("only %d metrics listed", all.Rows())
	}
}

func TestMissingPrivileges(t *testing.T) {
	ds := limited(t)
	for queryType, privilege := range map[string]string{
		"blobStores":   "nx-blobstores-read",
		"tasks":        "nx-tasks-read",
		"healthChecks": "nx-metrics-all",
		"usage":        "nx-metrics-all",
		"monthlyUsage": "nx-metrics-all",
		"formats":      "nx-metrics-all",
		"metrics":      "nx-metrics-all",
	} {
		r := query(t, ds, map[string]any{"queryType": queryType})
		if r.Error == nil || r.Status != backend.StatusForbidden || !strings.Contains(r.Error.Error(), "Give one of its roles the privilege "+privilege) {
			t.Errorf("%s: %v (%v)", queryType, r.Error, r.Status)
		}
	}
}
