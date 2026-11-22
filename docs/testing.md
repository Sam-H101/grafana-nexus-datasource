# Testing the Nexus Repository data source

This guide is for anyone who wants to try the plugin or review it. It starts a
Grafana with the plugin and a Nexus Repository Community Edition filled with
sample data, and lists what to try.

## The test environment

You need Docker with Compose v2 and a Bash shell. From the repository root:

```sh
devenv/run.sh npm ci
devenv/run.sh npm run build
devenv/run.sh mage -v build:linux
docker compose up -d --build --wait
```

The first start downloads Nexus and takes a few minutes. Then open
http://localhost:3000 (no login needed). Nexus itself is at
http://localhost:8081 (admin / `admin-dev-only`).

Starting the stack accepts the Nexus Community Edition licence on that
throwaway server; see [devenv/README.md](../devenv/README.md).

What is there:

| | |
| --- | --- |
| Data source **Nexus** | A user with every privilege the plugin can use |
| Data source **Nexus (limited user)** | A user that may only browse and search |
| Data source **Nexus (private network)** | Reaches Nexus only through a SOCKS proxy |
| Dashboard **Nexus Repository overview** | Every query type in a panel, with Format and Repository variables |
| Alert rules, folder **Nexus Repository** | Heap use, blob store free space, and a rule that fires |

The sample Nexus has ten repositories (Maven, NuGet, npm and raw; hosted,
proxy and group; one offline), two blob stores (one with a quota), nine Maven
components and five raw files.

## What to try

### Settings

1. Open **Connections > Data sources > Nexus** and press **Save & test**. It
   reports the Nexus version and "The user grafana can see 10 repositories."
2. Do the same on **Nexus (limited user)**: it connects, and lists what that
   user cannot read, with the privilege for each.
3. Add a new data source of type Nexus Repository:
   - URL `http://nexus:8081` and nothing else: "This Nexus requires a login."
   - A wrong password: "Nexus rejected the user name or password."
   - URL `http://nexus:8081/wrong`: "did not answer like Nexus Repository 3."
   - URL `http://nexus.invalid:8081`: "host not found."

### Queries

In **Explore** or a new panel, with the data source **Nexus**:

| Query | Try | Expect |
| --- | --- | --- |
| Repositories | Format `maven2`, Type Proxy | One row, `maven-central`, with the URL it proxies |
| Blob stores | Show: Table, then Free space | Two rows; then one number per blob store |
| Components | Repository `maven-releases`, Name `billing`, Sort by Version | Versions 1.0.0, 1.1.0, 1.2.0, 2.0.0 |
| Components | Group `com.example.*`, Limit 2 | Two rows and a note that more exist |
| Components | Type a repository that does not exist | An error naming the repository |
| Assets | Repository `raw-hosted` | Five files with upload times and sizes |
| Tasks | | The scheduled tasks, with their next run |
| Health checks | Failing only | The checks a new Nexus fails (telemetry, encryption key) |
| Usage over time | Components, Monthly | One point for this month |
| Monthly usage | | One row per month |
| Totals by format | | A row each for maven2, raw and others |
| Internal metrics | No metric chosen | A list of several hundred metrics with values |
| Internal metrics | `jvm.memory.heap.used` | One number; use the Stat visualization |

With the data source **Nexus (limited user)**, Blob stores fails with "Give
one of its roles the privilege nx-blobstores-read", and Repositories works but
notes why the storage columns are blank.

### Dashboard, variables and alerts

- Open the dashboard and set **Format** to `raw`: the repository table and the
  component list narrow to the raw repositories.
- In the dashboard settings, add a variable of type Query on this data source:
  the editor offers repositories, formats and blob stores, and previews the
  values.
- Under **Alerting > Alert rules**, the folder Nexus Repository has two rules
  in state Normal and one, "More than 5 components stored", firing.

## Private data source connect

The dev stack turns on Grafana's secure socks proxy and runs a plain SOCKS5
proxy, which is the mechanism behind Grafana Cloud's Private data source
connect. Nexus has a second name, `nexus-private`, that only the proxy can
resolve.

1. Add a data source with URL `http://nexus-private:8081`, the user `grafana`
   and its password (`docker compose run --rm --no-deps --entrypoint cat seed /seed-out/password`).
   **Save & test** fails: "host not found."
2. Turn on the switch under **Private network** and test again: it connects.

This shows that the plugin sends its requests through the proxy. It does not
test Grafana Cloud's own service, which has not been tried.

## Automated tests

| Suite | Command | What it covers |
| --- | --- | --- |
| Frontend unit | `devenv/run.sh npm run test:ci` | Query defaults, variables, the list of query types |
| Backend unit | `devenv/run.sh mage test` | Settings and query validation, the API client against a fake Nexus built from captured responses, frames, error messages, health check |
| Integration | `devenv/run.sh go test -tags integration ./tests/integration/...` | Every query type, both users, against the dev stack's Nexus |
| Browser | `devenv/run.sh npm run e2e` | Settings page, query editor, dashboard, variables, alert rules, the private network path |

CI runs all four on every push: the browser tests on three Grafana versions,
and the integration tests against Nexus Repository Community Edition 3.98.

## What has not been tested

- Nexus Repository Pro, and high-availability or cloud deployments.
- A large Nexus: the sample holds a handful of components.
- Grafana Cloud with Private data source connect.
- Nexus versions before 3.98. On 3.76 (the last OSS release) a quick check
  showed repositories, blob stores, search, tasks, health checks and internal
  metrics working, and usage reported as not available.
