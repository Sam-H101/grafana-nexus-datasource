package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

func settings(url, jsonData string, secure map[string]string) backend.DataSourceInstanceSettings {
	return backend.DataSourceInstanceSettings{URL: url, JSONData: []byte(jsonData), DecryptedSecureJSONData: secure}
}

func TestLoadSettings(t *testing.T) {
	s, err := LoadSettings(settings(" https://nexus.example.com/nexus/ ", `{"username":" grafana ","timeoutSeconds":45}`, map[string]string{"password": " p w "}))
	if err != nil {
		t.Fatal(err)
	}
	if s.URL != "https://nexus.example.com/nexus" || s.Username != "grafana" || s.Password != " p w " || s.Timeout != 45*time.Second {
		t.Errorf("settings = %+v", s)
	}

	// Anonymous, with the default timeout.
	s, err = LoadSettings(settings("http://nexus:8081", ``, nil))
	if err != nil || s.Username != "" || s.Timeout != DefaultTimeoutSeconds*time.Second {
		t.Errorf("anonymous: %+v, %v", s, err)
	}
}

func TestURLIsReducedToTheBase(t *testing.T) {
	for in, want := range map[string]string{
		"http://nexus:8081/":                         "http://nexus:8081",
		"http://nexus:8081/#browse/browse:maven":     "http://nexus:8081",
		"https://host/nexus/service/rest/v1":         "https://host/nexus",
		"https://host/nexus/service/rest/":           "https://host/nexus",
		"https://host/repository":                    "https://host",
		"HTTPS://Host.Example.com:8443/nexus///":     "https://Host.Example.com:8443/nexus",
		"https://host/nexus/#admin/repository/blobs": "https://host/nexus",
	} {
		s, err := LoadSettings(settings(in, ``, nil))
		if err != nil || s.URL != want {
			t.Errorf("%q: got %v, %v; want %q", in, s, err, want)
		}
	}
}

func TestInvalidSettings(t *testing.T) {
	for name, tc := range map[string]struct {
		url, json string
		secure    map[string]string
		want      string
	}{
		"no url":            {"", ``, nil, "URL is required"},
		"no scheme":         {"nexus.example.com", ``, nil, "http:// or https://"},
		"ftp":               {"ftp://nexus", ``, nil, "http:// or https://"},
		"credentials":       {"https://admin:secret@nexus", ``, nil, "their own fields"},
		"query":             {"https://nexus/?a=b", ``, nil, "query string"},
		"password only":     {"https://nexus", ``, map[string]string{"password": "x"}, "user name too"},
		"user only":         {"https://nexus", `{"username":"grafana"}`, nil, "enter the password"},
		"colon in the user": {"https://nexus", `{"username":"a:b"}`, map[string]string{"password": "x"}, "colon"},
		"timeout":           {"https://nexus", `{"timeoutSeconds":301}`, nil, "between 1 and 300"},
		"bad json":          {"https://nexus", `{`, nil, "parse settings"},
	} {
		_, err := LoadSettings(settings(tc.url, tc.json, tc.secure))
		if !errors.Is(err, ErrInvalidSettings) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
		if err != nil && strings.Contains(err.Error(), "secret") {
			t.Errorf("%s: the message leaks credentials: %v", name, err)
		}
	}
}

func TestParseQueryDefaults(t *testing.T) {
	qm, err := ParseQuery(json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if qm.QueryType != QueryRepositories || qm.Limit != DefaultLimit || qm.UsageMetric != "requests" || qm.UsagePeriod != "daily" {
		t.Errorf("defaults = %+v", qm)
	}
}

func TestParseQuery(t *testing.T) {
	qm, err := ParseQuery(json.RawMessage(`{
		"queryType":"assets",
		"repositories":["maven-releases, raw-hosted","maven-releases",""],
		"format":" Maven2 ","keyword":" billing ","group":"com.example.*","name":"bill*","version":"1.*",
		"sort":"Version","descending":true,"limit":250,
		"metrics":["jvm.memory.heap.used","jvm.memory.pools.CodeHeap-'non-nmethods'.used"]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(qm.Repositories, "|"); got != "maven-releases|raw-hosted" {
		t.Errorf("repositories = %q", got)
	}
	if strings.Join(qm.Formats, "|") != "maven2" || qm.Keyword != "billing" || qm.Group != "com.example.*" || qm.Name != "bill*" || qm.Version != "1.*" {
		t.Errorf("terms = %+v", qm)
	}
	if qm.Sort != "version" || !qm.Descending || qm.Limit != 250 || len(qm.Metrics) != 2 {
		t.Errorf("sort and limit = %+v", qm)
	}
}

func TestSeveralFormats(t *testing.T) {
	// A dashboard variable set to All, or to several values, arrives as a
	// comma-separated list.
	qm, err := ParseQuery(json.RawMessage(`{"queryType":"components","format":"Maven2, npm,,raw,npm"}`))
	if err != nil || strings.Join(qm.Formats, "|") != "maven2|npm|raw" {
		t.Errorf("formats = %v, %v", qm, err)
	}
	if qm, _ := ParseQuery(json.RawMessage(`{"format":"  "}`)); len(qm.Formats) != 0 {
		t.Errorf("blank format = %v", qm.Formats)
	}
}

func TestInvalidQueries(t *testing.T) {
	names := make([]string, MaxRepositories+1)
	for i := range names {
		names[i] = fmt.Sprintf("repo-%d", i)
	}
	many := `["` + strings.Join(names, ",") + `"]`
	for name, tc := range map[string]struct{ json, want string }{
		"not json":            {`{`, "not valid JSON"},
		"unknown type":        {`{"queryType":"drop"}`, "unknown query type"},
		"variable repository": {`{"repositories":["$repo"]}`, "dashboard variable without a value"},
		"braced variable":     {`{"repositories":["${repo:csv}"]}`, "dashboard variable without a value"},
		"bad repository":      {`{"repositories":["a/b"]}`, "not a repository name"},
		"too many":            {`{"repositories":` + many + `}`, "too many repositories"},
		"variable format":     {`{"format":"$format"}`, "dashboard variable without a value"},
		"bad format":          {`{"format":"maven 2"}`, "not a format"},
		"repository type":     {`{"repositoryType":"virtual"}`, "hosted, proxy or group"},
		"variable keyword":    {`{"queryType":"components","keyword":"[[q]]"}`, "dashboard variable without a value"},
		"long name":           {`{"name":"` + strings.Repeat("a", 301) + `"}`, "longer than 300"},
		"sort":                {`{"sort":"size"}`, "sorted by name"},
		"limit low":           {`{"limit":-1}`, "between 1 and 10000"},
		"limit high":          {`{"limit":10001}`, "between 1 and 10000"},
		"usage metric":        {`{"queryType":"usage","usageMetric":"storage"}`, "requests or components"},
		"usage period":        {`{"queryType":"usage","usagePeriod":"hourly"}`, "daily or monthly"},
		"variable metric":     {`{"queryType":"metrics","metrics":["$metric"]}`, "dashboard variable without a value"},
		"blob store value":    {`{"queryType":"blobStores","blobStoreValue":"quota"}`, "free space, size or blob count"},
	} {
		_, err := ParseQuery(json.RawMessage(tc.json))
		if !errors.Is(err, ErrInvalidQuery) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
	}
	// A usage setting left over from another query type is not checked.
	if _, err := ParseQuery(json.RawMessage(`{"queryType":"tasks","usagePeriod":"hourly"}`)); err != nil {
		t.Errorf("leftover usage period: %v", err)
	}
}
