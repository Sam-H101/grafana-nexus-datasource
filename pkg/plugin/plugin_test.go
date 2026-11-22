package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sam-H101/grafana-nexus-datasource/pkg/nexus/nexustest"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
)

var ctx = context.Background()

func newDatasource(t *testing.T, url, jsonData string, secure map[string]string) *Datasource {
	t.Helper()
	if jsonData == "" {
		jsonData = `{}`
	}
	inst, err := NewDatasource(ctx, backend.DataSourceInstanceSettings{URL: url, JSONData: []byte(jsonData), DecryptedSecureJSONData: secure})
	if err != nil {
		t.Fatal(err)
	}
	return inst.(*Datasource)
}

func fakeDatasource(t *testing.T) (*Datasource, *nexustest.Fake) {
	t.Helper()
	f := nexustest.New(t)
	f.Username, f.Password = "grafana", "pw"
	return newDatasource(t, f.URL, `{"username":"grafana"}`, map[string]string{"password": "pw"}), f
}

func run(t *testing.T, d *Datasource, query string) backend.DataResponse {
	t.Helper()
	resp, err := d.QueryData(ctx, &backend.QueryDataRequest{Queries: []backend.DataQuery{{RefID: "A", JSON: json.RawMessage(query)}}})
	if err != nil {
		t.Fatal(err)
	}
	return resp.Responses["A"]
}

func frameOf(t *testing.T, d *Datasource, query string) *data.Frame {
	t.Helper()
	r := run(t, d, query)
	if r.Error != nil {
		t.Fatalf("%s: %v", query, r.Error)
	}
	if len(r.Frames) != 1 || r.Frames[0].RefID != "A" {
		t.Fatalf("%s: %d frames", query, len(r.Frames))
	}
	return r.Frames[0]
}

func column(t *testing.T, f *data.Frame, name string) *data.Field {
	t.Helper()
	field, _ := f.FieldByName(name)
	if field == nil {
		names := make([]string, len(f.Fields))
		for i, fl := range f.Fields {
			names[i] = fl.Name
		}
		t.Fatalf("no field %q in %v", name, names)
	}
	return field
}

func texts(t *testing.T, f *data.Frame, name string) []string {
	t.Helper()
	field := column(t, f, name)
	out := make([]string, field.Len())
	for i := range out {
		v, _ := field.ConcreteAt(i)
		out[i] = fmt.Sprint(v)
	}
	return out
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

func TestRepositories(t *testing.T) {
	d, f := fakeDatasource(t)
	frame := frameOf(t, d, `{"queryType":"repositories"}`)
	if frame.Rows() != 10 {
		t.Fatalf("%d rows", frame.Rows())
	}
	if got := texts(t, frame, "name")[:3]; strings.Join(got, ",") != "docs-archive,maven-central,maven-public" {
		t.Errorf("names = %v", got)
	}
	if got := texts(t, frame, "online")[0]; got != "false" {
		t.Errorf("docs-archive online = %s", got)
	}
	if got := texts(t, frame, "members")[2]; got != "maven-releases, maven-snapshots, maven-central" {
		t.Errorf("members = %q", got)
	}

	frame = frameOf(t, d, `{"queryType":"repositories","format":"maven2","repositoryType":"hosted"}`)
	if got := strings.Join(texts(t, frame, "name"), ","); got != "maven-releases,maven-snapshots" {
		t.Errorf("maven hosted = %s", got)
	}
	frame = frameOf(t, d, `{"queryType":"repositories","repositories":["raw-hosted","npm-proxy"]}`)
	if got := strings.Join(texts(t, frame, "name"), ","); got != "npm-proxy,raw-hosted" {
		t.Errorf("selected = %s", got)
	}
	frame = frameOf(t, d, `{"queryType":"repositories","format":"docker"}`)
	if frame.Rows() != 0 || !strings.Contains(notices(frame), "No repository matches") {
		t.Errorf("docker: %d rows, %q", frame.Rows(), notices(frame))
	}
	if notices(frameOf(t, d, `{"queryType":"repositories"}`)) != "" {
		t.Error("a notice although the settings were given")
	}
	// The list is cached: five queries, one request.
	if n := len(f.Calls("v1/repositories")); n != 1 {
		t.Errorf("%d repository requests, want 1", n)
	}
}

// Nexus answers the settings with an empty list to a user without the
// repository administration privilege.
func TestRepositoriesWithoutSettings(t *testing.T) {
	d, f := fakeDatasource(t)
	f.Handlers["v1/repositorySettings"] = func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("[]")) }
	frame := frameOf(t, d, `{"queryType":"repositories"}`)
	if frame.Rows() != 10 || texts(t, frame, "blob_store")[0] != "" || !strings.Contains(notices(frame), "nx-repository-admin-*-*-read") {
		t.Errorf("%d rows, %q", frame.Rows(), notices(frame))
	}
	// The format, type and proxied URL come from the list itself.
	if texts(t, frame, "format")[1] != "maven2" || texts(t, frame, "remote_url")[1] != "https://repo1.maven.org/maven2/" {
		t.Errorf("maven-central = %v %v", texts(t, frame, "format")[1], texts(t, frame, "remote_url")[1])
	}
}

func TestBlobStoresAndFormats(t *testing.T) {
	d, _ := fakeDatasource(t)
	frame := frameOf(t, d, `{"queryType":"blobStores"}`)
	if strings.Join(texts(t, frame, "name"), ",") != "artifacts,default" {
		t.Errorf("names = %v", texts(t, frame, "name"))
	}
	if got := texts(t, frame, "size")[1]; got != "902286" {
		t.Errorf("size = %s", got)
	}
	if column(t, frame, "size").Config.Unit != "bytes" || column(t, frame, "free").Config.Unit != "bytes" {
		t.Error("sizes have no unit")
	}
	quota := column(t, frame, "quota")
	if v, ok := quota.ConcreteAt(0); !ok || v.(int64) != 10737418240 {
		t.Errorf("quota = %v", v)
	}
	if _, ok := quota.ConcreteAt(1); ok {
		t.Error("the default store has a quota")
	}

	// As one number per blob store, for alert rules: a field each, told
	// apart by a label.
	frame = frameOf(t, d, `{"queryType":"blobStores","blobStoreValue":"free"}`)
	if frame.Rows() != 1 || len(frame.Fields) != 3 || frame.Fields[1].Labels["blob_store"] != "artifacts" || frame.Fields[2].Config.DisplayNameFromDS != "default" || frame.Fields[1].Config.Unit != "bytes" {
		t.Fatalf("free space: %d fields", len(frame.Fields))
	}
	if v, _ := frame.Fields[2].ConcreteAt(0); v.(float64) != 855687114752 {
		t.Errorf("free = %v", v)
	}
	frame = frameOf(t, d, `{"queryType":"blobStores","blobStoreValue":"blobs"}`)
	if v, _ := frame.Fields[2].ConcreteAt(0); v.(float64) != 49 || frame.Fields[2].Config.Unit != "" {
		t.Errorf("blobs = %v", v)
	}
	if frameOf(t, d, `{"queryType":"blobStores","blobStoreValue":"table"}`).Rows() != 2 {
		t.Error("the table was not returned")
	}

	frame = frameOf(t, d, `{"queryType":"formats"}`)
	formats := texts(t, frame, "format")
	if len(formats) < 3 || notices(frame) != "" {
		t.Fatalf("formats = %v, %q", formats, notices(frame))
	}
	for i, format := range formats {
		if format == "raw" && texts(t, frame, "components")[i] != "3" {
			t.Errorf("raw components = %s", texts(t, frame, "components")[i])
		}
	}
}

func TestComponents(t *testing.T) {
	d, f := fakeDatasource(t)
	frame := frameOf(t, d, `{"queryType":"components","keyword":"x","sort":"name"}`)
	if frame.Rows() != 8 || notices(frame) != "" {
		t.Fatalf("%d rows, %q", frame.Rows(), notices(frame))
	}
	i := 3 // com.example:billing:1.0.0, a pom and a jar
	if texts(t, frame, "name")[i] != "billing" || texts(t, frame, "version")[i] != "1.0.0" || texts(t, frame, "assets")[i] != "2" || texts(t, frame, "size")[i] != "5140" {
		t.Errorf("row %d = %v %v %v %v", i, texts(t, frame, "name")[i], texts(t, frame, "version")[i], texts(t, frame, "assets")[i], texts(t, frame, "size")[i])
	}
	// The latest asset time of the component.
	if v, ok := column(t, frame, "last_updated").ConcreteAt(i); !ok || v.(time.Time).Format("15:04:05.000") != "00:00:04.750" {
		t.Errorf("last_updated = %v", v)
	}
	if _, ok := column(t, frame, "last_downloaded").ConcreteAt(i); ok {
		t.Error("never downloaded, but has a time")
	}
	if q := f.Calls("v1/search")[0].Query; q.Get("q") != "x" || q.Get("sort") != "name" || q.Get("direction") != "asc" || q.Has("repository") {
		t.Errorf("query = %v", q)
	}

	// One search per selected repository, joined in the order selected.
	f.Reset()
	frame = frameOf(t, d, `{"queryType":"components","repositories":["raw-hosted","maven-central"]}`)
	if got := strings.Join(texts(t, frame, "repository"), ","); got != "raw-hosted,raw-hosted,raw-hosted,maven-central" {
		t.Errorf("repositories = %s", got)
	}
	if n := len(f.Calls("v1/search")); n != 2 {
		t.Errorf("%d searches, want 2", n)
	}

	// Several formats: one search each. With repositories too, those of
	// another format are left out, and selecting every repository is the
	// same as selecting none.
	f.Reset()
	frameOf(t, d, `{"queryType":"components","format":"raw,maven2"}`)
	if calls := f.Calls("v1/search"); len(calls) != 2 || calls[0].Query.Get("format") == "" || calls[0].Query.Get("format") == calls[1].Query.Get("format") {
		t.Errorf("two formats: %+v", calls)
	}
	f.Reset()
	frameOf(t, d, `{"queryType":"components","format":"raw","repositories":["raw-hosted","maven-central","docs-archive"]}`)
	if calls := f.Calls("v1/search"); len(calls) != 2 || calls[0].Query.Get("repository") == "maven-central" || calls[1].Query.Get("repository") == "maven-central" || calls[0].Query.Has("format") {
		t.Errorf("format and repositories: %+v", calls)
	}
	f.Reset()
	frameOf(t, d, `{"queryType":"components","repositories":["docs-archive,maven-central,maven-public,maven-releases,maven-snapshots,npm-proxy,nuget-group,nuget-hosted,nuget.org-proxy,raw-hosted"]}`)
	if calls := f.Calls("v1/search"); len(calls) != 1 || calls[0].Query.Has("repository") {
		t.Errorf("every repository: %+v", calls)
	}
	frame = frameOf(t, d, `{"queryType":"repositories","format":"raw,npm"}`)
	if got := strings.Join(texts(t, frame, "name"), ","); got != "docs-archive,npm-proxy,raw-hosted" {
		t.Errorf("two formats of repositories = %s", got)
	}

	frame = frameOf(t, d, `{"queryType":"components","limit":5}`)
	if frame.Rows() != 5 || !strings.Contains(notices(frame), "Showing the first 5 components") {
		t.Errorf("limit: %d rows, %q", frame.Rows(), notices(frame))
	}
	frame = frameOf(t, d, `{"queryType":"components","repositories":["docs-archive"]}`)
	if frame.Rows() != 0 || notices(frame) != "No matching components." {
		t.Errorf("empty: %d rows, %q", frame.Rows(), notices(frame))
	}
}

func TestAssets(t *testing.T) {
	d, _ := fakeDatasource(t)
	frame := frameOf(t, d, `{"queryType":"assets","repositories":["raw-hosted"]}`)
	if frame.Rows() != 3 || frame.Fields[0].Name != "uploaded" || frame.Fields[0].Type() != data.FieldTypeNullableTime {
		t.Fatalf("%d rows, first field %s", frame.Rows(), frame.Fields[0].Name)
	}
	if got := texts(t, frame, "path")[0]; got != "/tools/cli/1.1.0/cli-1.1.0.bin" {
		t.Errorf("path = %s", got)
	}
	if got := texts(t, frame, "uploaded_by")[0]; got != "admin" {
		t.Errorf("uploaded_by = %s", got)
	}
	if _, ok := column(t, frame, "last_downloaded").ConcreteAt(0); !ok {
		t.Error("the downloaded asset has no download time")
	}
	if _, ok := column(t, frame, "last_downloaded").ConcreteAt(1); ok {
		t.Error("an asset never downloaded has a download time")
	}
}

func TestTasksAndHealthChecks(t *testing.T) {
	d, _ := fakeDatasource(t)
	frame := frameOf(t, d, `{"queryType":"tasks"}`)
	if frame.Rows() != 9 || texts(t, frame, "name")[0] != "Cleanup service" || texts(t, frame, "state")[0] != "WAITING" || texts(t, frame, "last_result")[0] != "" {
		t.Errorf("tasks: %d rows, %v", frame.Rows(), texts(t, frame, "name"))
	}
	if _, ok := column(t, frame, "last_run").ConcreteAt(0); ok {
		t.Error("a task that never ran has a last run")
	}
	if _, ok := column(t, frame, "next_run").ConcreteAt(0); !ok {
		t.Error("no next run")
	}

	frame = frameOf(t, d, `{"queryType":"healthChecks"}`)
	checks, healthy := texts(t, frame, "check"), texts(t, frame, "healthy")
	if len(checks) < 15 || checks[0] != "Baseline Telemetry" || healthy[0] != "false" || healthy[1] != "false" || healthy[2] != "true" {
		t.Errorf("failing checks are not first: %v %v", checks[:3], healthy[:3])
	}
	for i, msg := range texts(t, frame, "message") {
		if strings.ContainsAny(msg, "<>") {
			t.Errorf("%s: HTML left in %q", checks[i], msg)
		}
	}
	frame = frameOf(t, d, `{"queryType":"healthChecks","unhealthyOnly":true}`)
	if got := strings.Join(texts(t, frame, "check"), "|"); got != "Baseline Telemetry|Default Secret Encryption Key" {
		t.Errorf("unhealthy = %s", got)
	}
}

func TestPlainText(t *testing.T) {
	for in, want := range map[string]string{
		"0/1 blob stores report issues<br>": "0/1 blob stores report issues",
		"<b>Total Components:</b> 0 / 100,000 - <b>Status:</b> Below Limit <br><b>Note:</b> Consider &amp; migrate": "Total Components: 0 / 100,000 - Status: Below Limit; Note: Consider & migrate",
		"plain": "plain",
	} {
		if got := plainText(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestUsage(t *testing.T) {
	d, f := fakeDatasource(t)
	frame := frameOf(t, d, `{"queryType":"usage"}`)
	if frame.Rows() != 7 || frame.Fields[0].Name != "time" || frame.Fields[1].Name != "requests" || frame.Meta.Type != data.FrameTypeTimeSeriesWide {
		t.Fatalf("daily requests: %d rows, %v", frame.Rows(), frame.Meta)
	}
	if got := texts(t, frame, "requests")[1]; got != "1250" {
		t.Errorf("value = %s", got)
	}
	frame = frameOf(t, d, `{"queryType":"usage","usageMetric":"components","usagePeriod":"monthly"}`)
	if frame.Rows() != 3 || column(t, frame, "components").Config.DisplayName != "Components" {
		t.Errorf("monthly components: %d rows", frame.Rows())
	}
	frame = frameOf(t, d, `{"queryType":"usage","usageMetric":"components"}`)
	if frame.Rows() != 0 || !strings.Contains(notices(frame), "not recorded any daily components") {
		t.Errorf("empty: %q", notices(frame))
	}

	frame = frameOf(t, d, `{"queryType":"monthlyUsage"}`)
	if frame.Rows() != 2 || texts(t, frame, "requests")[1] != "7380" || column(t, frame, "egress").Config.Unit != "bytes" {
		t.Errorf("monthly usage: %d rows", frame.Rows())
	}

	f.Missing["v1/usage-history"] = true
	if r := run(t, d, `{"queryType":"usage"}`); r.Error == nil || !strings.Contains(r.Error.Error(), "does not report usage history") || r.Status != backend.StatusBadRequest {
		t.Errorf("old version: %v", r.Error)
	}
}

func TestMetrics(t *testing.T) {
	d, f := fakeDatasource(t)
	d.now = func() time.Time { return time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC) }
	frame := frameOf(t, d, `{"queryType":"metrics","metrics":["jvm.memory.heap.used","nexus.analytics.component_total_count","no.such.metric"]}`)
	if frame.Rows() != 1 || len(frame.Fields) != 3 || frame.Fields[1].Labels["metric"] != "jvm.memory.heap.used" || frame.Fields[2].Config.DisplayNameFromDS != "nexus.analytics.component_total_count" {
		t.Fatalf("fields = %d, rows = %d", len(frame.Fields), frame.Rows())
	}
	if at, _ := frame.Fields[0].ConcreteAt(0); !at.(time.Time).Equal(d.now()) {
		t.Errorf("time = %v", at)
	}
	if !strings.Contains(notices(frame), "does not report: no.such.metric") {
		t.Errorf("notice = %q", notices(frame))
	}

	// Without a selection: every metric, by name.
	frame = frameOf(t, d, `{"queryType":"metrics"}`)
	names := texts(t, frame, "metric")
	if len(names) < 30 || names[0] > names[1] || frame.Fields[1].Type() != data.FieldTypeFloat64 {
		t.Errorf("%d metrics", len(names))
	}
	// Queries in the same refresh share one request.
	if n := len(f.Calls("metrics/data")); n != 1 {
		t.Errorf("%d metric requests, want 1", n)
	}
}

func TestMissingPrivilegesAreNamed(t *testing.T) {
	d, f := fakeDatasource(t)
	for path, tc := range map[string]struct{ query, privilege string }{
		"v1/blobstores":    {`{"queryType":"blobStores"}`, "nx-blobstores-read"},
		"v1/tasks":         {`{"queryType":"tasks"}`, "nx-tasks-read"},
		"v1/status/check":  {`{"queryType":"healthChecks"}`, "nx-metrics-all"},
		"v1/usage-history": {`{"queryType":"usage"}`, "nx-metrics-all"},
		"metrics/data":     {`{"queryType":"metrics"}`, "nx-metrics-all"},
		"v1/search":        {`{"queryType":"components"}`, "nx-search-read"},
		"v1/repositories":  {`{"queryType":"repositories"}`, "nx-repository-view-*-*-browse"},
	} {
		f.Forbidden[path] = true
		r := run(t, d, tc.query)
		if r.Error == nil || r.Status != backend.StatusForbidden || !strings.Contains(r.Error.Error(), "Give one of its roles the privilege "+tc.privilege) {
			t.Errorf("%s: %v (%v)", path, r.Error, r.Status)
		}
		if r.ErrorSource != backend.ErrorSourceDownstream {
			t.Errorf("%s: error source %q", path, r.ErrorSource)
		}
	}
}

func TestQueryErrors(t *testing.T) {
	d, f := fakeDatasource(t)
	r := run(t, d, `{"queryType":"components","repositories":["$repo"]}`)
	if r.Error == nil || r.Status != backend.StatusBadRequest || !strings.Contains(r.Error.Error(), "dashboard variable without a value") {
		t.Errorf("variable: %v", r.Error)
	}
	if len(f.Calls("v1/search")) != 0 {
		t.Error("an invalid query reached Nexus")
	}
	r = run(t, d, `{"queryType":"components","repositories":["nope"]}`)
	if r.Error == nil || !strings.Contains(r.Error.Error(), `Nexus: the repository "nope" does not exist`) {
		t.Errorf("unknown repository: %v", r.Error)
	}

	wrong := newDatasource(t, f.URL, `{"username":"grafana"}`, map[string]string{"password": "wrong-secret"})
	r = run(t, wrong, `{"queryType":"blobStores"}`)
	if r.Error == nil || r.Status != backend.StatusUnauthorized || !strings.Contains(r.Error.Error(), "rejected the user name or password") || strings.Contains(r.Error.Error(), "wrong-secret") {
		t.Errorf("wrong password: %v", r.Error)
	}
	anonymous := newDatasource(t, f.URL, ``, nil)
	r = run(t, anonymous, `{"queryType":"blobStores"}`)
	if r.Error == nil || !strings.Contains(r.Error.Error(), "requires a login") {
		t.Errorf("anonymous: %v", r.Error)
	}

	invalid := newDatasource(t, "nexus.example.com", ``, nil)
	r = run(t, invalid, `{"queryType":"blobStores"}`)
	if r.Error == nil || !strings.HasPrefix(r.Error.Error(), "Configuration problem: the Nexus URL must start with") {
		t.Errorf("invalid settings: %v", r.Error)
	}

	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	down := newDatasource(t, closed.URL, ``, nil)
	r = run(t, down, `{"queryType":"repositories"}`)
	if r.Error == nil || r.Status != backend.StatusBadGateway || !strings.Contains(r.Error.Error(), "Cannot reach Nexus at "+closed.URL) || !strings.Contains(r.Error.Error(), "Private data source connect") {
		t.Errorf("down: %v", r.Error)
	}
}

func TestQueriesRunSideBySide(t *testing.T) {
	d, _ := fakeDatasource(t)
	req := &backend.QueryDataRequest{}
	for i, q := range []string{`{"queryType":"repositories"}`, `{"queryType":"tasks"}`, `{"queryType":"nonsense"}`, `{"queryType":"blobStores"}`} {
		req.Queries = append(req.Queries, backend.DataQuery{RefID: string(rune('A' + i)), JSON: json.RawMessage(q)})
	}
	resp, err := d.QueryData(ctx, req)
	if err != nil || len(resp.Responses) != 4 {
		t.Fatalf("%d responses, %v", len(resp.Responses), err)
	}
	if resp.Responses["A"].Error != nil || resp.Responses["B"].Error != nil || resp.Responses["D"].Error != nil || resp.Responses["C"].Error == nil {
		t.Errorf("responses = %+v", resp.Responses)
	}
	if resp.Responses["B"].Frames[0].RefID != "B" {
		t.Error("frame has the wrong RefID")
	}
}

func health(t *testing.T, d *Datasource) *backend.CheckHealthResult {
	t.Helper()
	res, err := d.CheckHealth(ctx, &backend.CheckHealthRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestCheckHealth(t *testing.T) {
	d, f := fakeDatasource(t)
	res := health(t, d)
	if res.Status != backend.HealthStatusOk || res.Message != "Connected to Nexus Repository 3.98.3-01 (Community Edition). The user grafana can see 10 repositories." {
		t.Errorf("ok: %v %q", res.Status, res.Message)
	}

	// Areas the user may not read are listed, without failing the check.
	f.Forbidden["v1/blobstores"], f.Forbidden["v1/status/check"] = true, true
	res = health(t, d)
	if res.Status != backend.HealthStatusOk || !strings.HasSuffix(res.Message, "Not readable with this login: blob stores (nx-blobstores-read); health checks, usage, formats and metrics (nx-metrics-all).") {
		t.Errorf("limited: %v %q", res.Status, res.Message)
	}

	wrong := newDatasource(t, f.URL, `{"username":"grafana"}`, map[string]string{"password": "nope"})
	if res := health(t, wrong); res.Status != backend.HealthStatusError || !strings.Contains(res.Message, "rejected the user name or password") {
		t.Errorf("wrong password: %q", res.Message)
	}
	anonymous := newDatasource(t, f.URL, ``, nil)
	if res := health(t, anonymous); res.Status != backend.HealthStatusError || !strings.Contains(res.Message, "requires a login") {
		t.Errorf("anonymous: %q", res.Message)
	}
	invalid := newDatasource(t, "", ``, nil)
	if res := health(t, invalid); res.Status != backend.HealthStatusError || !strings.Contains(res.Message, "Configuration problem: the Nexus URL is required") {
		t.Errorf("invalid: %q", res.Message)
	}

	notNexus := httptest.NewServer(http.NotFoundHandler())
	defer notNexus.Close()
	if res := health(t, newDatasource(t, notNexus.URL, ``, nil)); res.Status != backend.HealthStatusError || !strings.Contains(res.Message, "did not answer like Nexus Repository 3") {
		t.Errorf("not nexus: %q", res.Message)
	}

	starting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer starting.Close()
	if res := health(t, newDatasource(t, starting.URL, ``, nil)); res.Status != backend.HealthStatusError || !strings.Contains(res.Message, "cannot serve requests yet") {
		t.Errorf("starting: %q", res.Message)
	}
}

func TestAnonymousHealthWhereNexusAllowsIt(t *testing.T) {
	f := nexustest.New(t)
	f.ServerHeader = "nginx"
	res := health(t, newDatasource(t, f.URL, ``, nil))
	if res.Status != backend.HealthStatusOk || res.Message != "Connected to Nexus Repository. Anonymous access can see 10 repositories." {
		t.Errorf("%v %q", res.Status, res.Message)
	}
}

type recorder struct {
	status int
	body   []byte
}

func (r *recorder) Send(resp *backend.CallResourceResponse) error {
	r.status, r.body = resp.Status, resp.Body
	return nil
}

func resource(t *testing.T, d *Datasource, path string) *recorder {
	t.Helper()
	rec := &recorder{}
	if err := d.CallResource(ctx, &backend.CallResourceRequest{Method: http.MethodGet, Path: path, URL: path}, rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestResources(t *testing.T) {
	d, f := fakeDatasource(t)
	rec := resource(t, d, "repositories")
	var repos []repositoryItem
	if err := json.Unmarshal(rec.body, &repos); err != nil || rec.status != 200 || len(repos) != 10 || repos[1] != (repositoryItem{Name: "maven-central", Format: "maven2", Type: "proxy"}) {
		t.Errorf("repositories: %d %s", rec.status, rec.body)
	}
	rec = resource(t, d, "metrics")
	var names []string
	if err := json.Unmarshal(rec.body, &names); err != nil || rec.status != 200 || len(names) < 30 {
		t.Errorf("metrics: %d, %d names", rec.status, len(names))
	}

	f.Forbidden["metrics/data"] = true
	d.cache.metrics = nil
	rec = resource(t, d, "metrics")
	if rec.status != 403 || !strings.Contains(string(rec.body), "nx-metrics-all") {
		t.Errorf("forbidden metrics: %d %s", rec.status, rec.body)
	}
	if rec := resource(t, d, "nothing"); rec.status != 404 {
		t.Errorf("unknown resource: %d", rec.status)
	}
	invalid := newDatasource(t, "", ``, nil)
	if rec := resource(t, invalid, "repositories"); rec.status != 400 || !strings.Contains(string(rec.body), "Configuration problem") {
		t.Errorf("invalid settings: %d %s", rec.status, rec.body)
	}
}
