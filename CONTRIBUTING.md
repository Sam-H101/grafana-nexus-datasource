# Contributing

Bug reports and pull requests are welcome in the
[issue tracker](https://github.com/Sam-H101/grafana-nexus-datasource/issues).

To try the plugin, or to see what to test, read the
[testing guide](docs/testing.md).

## Building and testing

Everything runs in Docker; nothing else needs installing. The toolbox
container, the dev stack (Nexus Repository Community Edition with sample
artifacts, a SOCKS proxy and Grafana) and every command are described in
[devenv/README.md](devenv/README.md). In short:

```sh
devenv/run.sh npm ci
devenv/run.sh npm run build            # frontend
devenv/run.sh mage -v build:linux      # backend
docker compose up -d --build --wait    # Grafana on http://localhost:3000
```

Before opening a pull request, run the checks CI runs:

```sh
devenv/run.sh npm run typecheck
devenv/run.sh npm run lint
devenv/run.sh npm run test:ci
devenv/run.sh mage test
devenv/run.sh golangci-lint run ./...
devenv/run.sh go test -tags integration ./tests/integration/...
devenv/run.sh npm run e2e
```

## Layout

| Path | What is there |
| --- | --- |
| `src/` | Frontend: settings page, query editor, variable editor |
| `src/query.ts` | Query types, defaults and variable handling, without UI (unit-tested) |
| `pkg/models/` | Settings and query validation; no I/O |
| `pkg/nexus/` | Nexus REST API client; `nexustest/` is a fake server with captured responses |
| `pkg/plugin/` | Grafana handlers: queries to data frames, health check, error messages, editor lists |
| `tests/integration/` | Go tests against the dev stack's Nexus |
| `tests/e2e/` | Playwright tests against the dev stack's Grafana |
| `devenv/nexus/seed.py` | Creates the sample repositories, artifacts and users |
| `docs/nexus-api-notes.md` | API behaviour the backend relies on, as observed |
| `provisioning/` | Data sources, dashboard and alert rules of the dev stack |

## Adding a query type

1. Add the type and its fields to `pkg/models/query.go`, with validation and
   tests.
2. Add the API call to `pkg/nexus/`, with a captured response in
   `pkg/nexus/nexustest/testdata/` and a route in `nexustest/fake.go`.
3. Turn the answer into a data frame in `pkg/plugin/query.go`. Name the
   privilege the call needs, so a refusal can say what to grant.
4. Add the type to `src/types.ts` and `src/query.ts` (the list there is
   checked against the backend's by a unit test), and its fields to
   `src/components/QueryEditor.tsx`.
5. Cover it in `tests/integration/` and `tests/e2e/`, and describe it in
   `README.md`.

Anything surprising about the Nexus API belongs in `docs/nexus-api-notes.md`,
with a test.

## Installing a build without the catalog

Unzip a release archive into Grafana's plugins directory, so that you have
`<plugins>/smartquinoa1504-nexus-datasource/plugin.json`. Archives from
this repository are unsigned until the plugin is approved for the Grafana
catalog, so allow it in `grafana.ini` and restart Grafana:

```ini
[plugins]
allow_loading_unsigned_plugins = smartquinoa1504-nexus-datasource
```

## Releasing

1. Set the version in `package.json` (and `package-lock.json`) and add a dated
   entry to `CHANGELOG.md`.
2. Push that commit, then push a tag for it, for example `v1.0.0`, or create a
   release with that tag in the GitHub UI. The Release workflow builds the
   plugin for every platform and attaches the zip and its SHA-1 to a draft
   GitHub release.

The tag must equal `v` plus the version in `package.json`: Grafana rejects a
plugin whose built version differs from the source. The workflow checks this
first and fails within seconds if they differ.

Signing is controlled by repository variables, described at the top of
`.github/workflows/release.yml`.
