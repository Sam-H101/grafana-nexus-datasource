// Package nexustest is a fake Nexus Repository for tests. It answers with
// responses captured from Nexus Repository Community Edition 3.98.
package nexustest

import (
	"embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
)

//go:embed testdata/*.json
var testdata embed.FS

// Call is one request the fake received.
type Call struct {
	Path  string
	Query url.Values
	Auth  string
}

// Fake is a fake Nexus. Change its fields before making requests.
type Fake struct {
	*httptest.Server
	// Username and Password, when set, are required on every endpoint but
	// the status check; wrong ones get a 401 everywhere, as on Nexus.
	Username, Password string
	// Forbidden lists the paths (without the service/rest/ prefix) that
	// answer 403.
	Forbidden map[string]bool
	// Missing lists the paths that answer 404, as on older versions.
	Missing map[string]bool
	// Handlers replace the built-in answer for a path.
	Handlers map[string]http.HandlerFunc
	// ServerHeader is sent with every answer.
	ServerHeader string
	// PageSize is how many search results one page holds; 0 means all.
	PageSize int

	mu    sync.Mutex
	calls []Call
}

// routes maps a path under service/rest/ to its captured response.
var routes = map[string]string{
	"v1/repositories":       "repositories.json",
	"v1/repositorySettings": "repository_settings.json",
	"v1/blobstores":         "blobstores.json",
	"v1/search":             "search.json",
	"v1/search/assets":      "search_assets.json",
	"v1/tasks":              "tasks.json",
	"v1/status/check":       "status_check.json",
	"v1/monthly-metrics":    "monthly_metrics.json",
	"metrics/data":          "metrics.json",
}

// New starts a fake Nexus that is stopped when the test ends.
func New(t testing.TB) *Fake {
	t.Helper()
	f := &Fake{
		Forbidden:    map[string]bool{},
		Missing:      map[string]bool{},
		Handlers:     map[string]http.HandlerFunc{},
		ServerHeader: "Nexus/3.98.3-01 (COMMUNITY)",
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

// Calls returns the requests received for a path such as "v1/search".
func (f *Fake) Calls(path string) []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Call
	for _, c := range f.calls {
		if c.Path == path {
			out = append(out, c)
		}
	}
	return out
}

// Reset forgets the recorded requests.
func (f *Fake) Reset() {
	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()
}

// Fixture returns a captured response.
func Fixture(name string) []byte {
	b, err := testdata.ReadFile("testdata/" + name)
	if err != nil {
		panic(err)
	}
	return b
}

// Error writes an error the way Nexus does for a bad parameter.
func Error(w http.ResponseWriter, status int, id, message string) {
	writeJSON(w, status, []map[string]string{{"id": id, "message": message}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func bare(w http.ResponseWriter, status int, name string) {
	writeJSON(w, status, map[string]any{"status-message": name, "status-code": status})
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Server", f.ServerHeader)
	// The metrics endpoint moved under service/rest/; the old address
	// redirects, as on Nexus.
	if r.URL.Path == "/service/metrics/data" {
		http.Redirect(w, r, "/service/rest/metrics/data", http.StatusMovedPermanently)
		return
	}
	path, ok := strings.CutPrefix(r.URL.Path, "/service/rest/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	f.mu.Lock()
	f.calls = append(f.calls, Call{Path: path, Query: r.URL.Query(), Auth: r.Header.Get("Authorization")})
	f.mu.Unlock()

	user, pass, sent := r.BasicAuth()
	if sent && f.Username != "" && (user != f.Username || pass != f.Password) {
		w.Header().Set("WWW-Authenticate", `BASIC realm="Sonatype Nexus Repository Manager"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if h, ok := f.Handlers[path]; ok {
		h(w, r)
		return
	}
	if path == "v1/status" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if f.Username != "" && !sent {
		w.Header().Set("WWW-Authenticate", `BASIC realm="Sonatype Nexus Repository Manager"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if f.Forbidden[path] {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if f.Missing[path] {
		bare(w, http.StatusNotFound, "NOT_FOUND")
		return
	}

	q := r.URL.Query()
	switch path {
	case "v1/usage-history":
		f.usage(w, q)
	case "v1/search", "v1/search/assets":
		f.search(w, path, q)
	default:
		name, ok := routes[path]
		if !ok {
			bare(w, http.StatusNotFound, "NOT_FOUND")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(Fixture(name))
	}
}

func (f *Fake) usage(w http.ResponseWriter, q url.Values) {
	metric, period := q.Get("metric"), q.Get("period")
	switch {
	case metric != "requests" && metric != "components":
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "metric parameter is required (requests or components)"})
	case period != "daily" && period != "monthly":
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid period. Use 'daily' or 'monthly'"})
	case metric == "requests" && period == "daily":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(Fixture("usage_requests_daily.json"))
	case metric == "components" && period == "monthly":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(Fixture("usage_components_monthly.json"))
	default:
		writeJSON(w, http.StatusOK, map[string]any{"metric": metric, "period": period, "data": []any{}})
	}
}

// search filters the captured results by repository and pages them.
func (f *Fake) search(w http.ResponseWriter, path string, q url.Values) {
	if s := q.Get("sort"); s != "" && s != "name" && s != "group" && s != "version" && s != "repository" {
		Error(w, http.StatusBadRequest, "sort", "Invalid sort field: '"+s+"'. Please use a valid sort field.")
		return
	}
	var all struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(Fixture(routes[path]), &all); err != nil {
		panic(err)
	}
	items := all.Items
	if repo := q.Get("repository"); repo != "" {
		known := false
		var repos []map[string]any
		_ = json.Unmarshal(Fixture("repositories.json"), &repos)
		for _, r := range repos {
			known = known || r["name"] == repo
		}
		if !known {
			bare(w, http.StatusBadRequest, "BAD_REQUEST")
			return
		}
		items = nil
		for _, it := range all.Items {
			if it["repository"] == repo {
				items = append(items, it)
			}
		}
	}
	start, _ := strconv.Atoi(q.Get("continuationToken"))
	if start > len(items) {
		start = len(items)
	}
	end := len(items)
	var token any
	if f.PageSize > 0 && start+f.PageSize < len(items) {
		end = start + f.PageSize
		token = strconv.Itoa(end)
	}
	page := items[start:end]
	if page == nil {
		page = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": page, "continuationToken": token})
}
