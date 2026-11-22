# Changelog

## 1.0.0 (unreleased)

First version.

- Ten queries against Nexus Repository 3: repositories, totals by format, blob
  stores, components, assets, tasks, health checks, usage over time, monthly
  usage and internal metrics.
- Component and asset search by repository, format, keyword, group, name and
  version, with sorting and a limit.
- Blob stores as a table, or as one number per blob store (free space, size,
  blob count) for alert rules and gauges.
- Dashboard variables for repositories, formats and blob stores; annotations
  from asset uploads; queries work in alert rules.
- Settings: URL, user name and password, TLS options, query timeout, and
  Grafana's secure socks proxy for Private data source connect.
- Save & test reports the Nexus version and lists what the user may not read;
  a query that lacks a privilege names the privilege to grant.
- Developed and tested against Nexus Repository Community Edition 3.98.
