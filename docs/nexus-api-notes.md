# Nexus REST API notes

What the backend relies on, as observed on Nexus Repository Community Edition
3.98.3 (image `sonatype/nexus3:3.98.3`). Each point has a test in
`pkg/nexus/client_test.go` or `tests/integration/`.

## 1. Addressing and authentication

- Everything is under `<base>/service/rest/`. The API description is at
  `/service/rest/swagger.json`.
- Authentication is HTTP Basic. A user token (Pro) is sent the same way.
- Nexus names itself in the `Server` header of every answer:
  `Nexus/3.98.3-01 (COMMUNITY)`. That is where the version and edition in the
  health check come from. A reverse proxy may replace the header.
- `GET v1/status` needs no login and answers 200 with an empty body. Sent with
  wrong credentials it answers 401, like every endpoint.
- A 401 has an empty body. A 403 has an empty body too, so the plugin maps
  each query to the privilege it needs (below).
- Community Edition refuses most of the API until its licence is accepted
  (`POST v1/system/eula`). The seed does that for the dev stack; on a real
  server an administrator has done it in the setup wizard.

## 2. Privileges

Observed with a role holding only the privileges named:

| Endpoint | Privilege |
| --- | --- |
| `v1/repositories` | `nx-repository-view-*-*-browse` (lists what the user may browse) |
| `v1/repositorySettings` | Answers 200 to anyone, but with an **empty list** unless the user has `nx-repository-admin-*-*-read` |
| `v1/search`, `v1/search/assets` | `nx-search-read`, plus read on the repositories |
| `v1/blobstores` | `nx-blobstores-read` |
| `v1/tasks` | `nx-tasks-read` |
| `v1/status/check`, `v1/usage-history`, `v1/monthly-metrics`, `metrics/data` | `nx-metrics-all` |

`nx-settings-read`, `nx-analytics-all` and `nx-healthcheck-read` open none of
these.

## 3. Repositories

- `v1/repositories` has name, format, type, URL, online and, for a proxy,
  `attributes.proxy.remoteUrl`. Its `size` field was always 0.
- `v1/repositorySettings` adds storage (blob store, write policy), cleanup
  policies, group members and the Maven version policy. It **leaves `format`
  out for some repositories** (seen for `nuget-hosted`), so the format always
  comes from the list.

## 4. Search

- `v1/search` returns components with their assets; `v1/search/assets`
  returns assets. Both take `repository`, `format`, `q`, `group`, `name`,
  `version`, `sort` (`name`, `group`, `version`, `repository`) and `direction`
  (`asc`, `desc`), and page with `continuationToken`.
- `repository` and `format` take one value each.
- A search in a repository that does not exist answers a bare **400**
  (`{"status-message":"BAD_REQUEST"}`), not 404. An invalid `sort` answers 400
  with `[{"id":"sort","message":"..."}]`.
- `uploader` and `uploaderIp` are `null` unless the caller is an
  administrator.
- For a raw repository the component name is the path, the group is the
  directory, and the version is empty.
- Datetimes look like `2026-11-22T00:00:04.497+00:00`.

## 5. Errors

Three shapes, all handled by `errorMessage`:

```
[{"id":"sort","message":"Invalid sort field"}]
{"error":"metric parameter is required (requests or components)"}
{"status-message":"NOT_FOUND","status-code":404,"siesta-faultid":"..."}
```

## 6. Usage

- `v1/usage-history?metric=requests|components&period=daily|monthly` returns
  `{"data":[{"date":"2026-11-21","value":0}, ...]}`. Monthly dates are
  `2026-11`. Both parameters are required.
- A new server **repeats the same date** for the days it has no record of
  (seven entries, six of them the same day). The client keeps the last value
  for each date.
- `v1/monthly-metrics` returns up to twelve months with `requestCount`,
  `componentCount`, `peakStorage`, `responseSize` and `metricDate`.
- These endpoints came with Community Edition. The client reports a 404 from
  them as "usage history is not available".

## 7. Metrics

- `service/metrics/data` redirects (301) to `service/rest/metrics/data`. Go's
  HTTP client follows it and keeps the credentials, as the host is the same.
- The body is a Dropwizard registry: `gauges` (`{"value": ...}`), `counters`
  (`{"count": n}`), `meters`, `timers` and `histograms`.
- Gauge values may be numbers, booleans, text, lists or objects. Only numbers
  and booleans become metric values.
- The totals by format come from structured gauges under `nexus.analytics.`:
  `repository_type_counts`, `repository_component_count_by_format_type`,
  `repository_asset_count_by_format_type` (lists of `{format, type, count}`)
  and `bytes_transferred_by_format` (a list of one-key objects, one per known
  format, most of them zero). NuGet repositories are counted under the format
  `nugetv3` there, but under `nuget` everywhere else.

## 8. Health checks

`v1/status/check` is an object keyed by check name, each with `healthy`,
`message` and `timestamp`. Messages contain HTML (`<br>`, `<b>`), which the
plugin turns into plain text.
