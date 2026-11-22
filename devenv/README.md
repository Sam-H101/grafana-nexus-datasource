# Development environment

Everything runs in Docker: you need Docker with Compose v2 and a Bash shell
(Linux, macOS, or WSL on Windows). Node, Go and the other build tools live in a
toolbox container, so nothing else has to be installed. Run the commands below
from the repository root.

## Toolbox

`devenv/toolbox/Dockerfile` has Node, Go, Mage, golangci-lint and Chromium's
system libraries. `node_modules`, Go caches, the npm cache and Playwright
browsers live in Docker volumes; only build output (`dist/`) is written to the
working tree. `devenv/run.sh` runs a command in the toolbox as your own user, so
files it writes belong to you. Always start the toolbox through it: it builds
the image for your user id.

```sh
devenv/run.sh npm ci                 # install frontend deps (into a volume)
devenv/run.sh npm run build          # frontend
devenv/run.sh npm run dev            # frontend, rebuild on change
devenv/run.sh mage -v build:linux    # backend for the dev stack
devenv/run.sh mage -v buildAll       # backend for every platform
devenv/run.sh npm run typecheck
devenv/run.sh npm run lint
devenv/run.sh npm run test:ci        # frontend unit tests
devenv/run.sh mage test              # backend unit tests
devenv/run.sh golangci-lint run ./...
devenv/run.sh                        # shell
```

## Dev stack

Build the plugin into `dist/` first, then:

```sh
docker compose up -d --build --wait  # Nexus, its seed, the proxy, Grafana
docker compose restart grafana       # pick up a rebuilt backend binary
docker compose logs -f grafana
docker compose down                  # add -v to drop volumes too
```

| Service | Address | What it is |
| --- | --- | --- |
| `grafana` | http://localhost:3000 | Grafana with the plugin from `dist/`, anonymous Admin |
| `nexus` | http://localhost:8081 | Nexus Repository Community Edition (admin / `admin-dev-only`) |
| `seed` | | Runs once: sample repositories, artifacts and users |
| `socks` | | A SOCKS5 proxy, standing in for Private data source connect |

Nexus takes a minute or two to start and uses about 2 GB of memory.

The seed accepts the Community Edition licence on the throwaway server, which
the REST API requires. Read the licence Nexus links to before using this
stack: by starting it you accept that licence for your copy.

`GRAFANA_PORT` and `NEXUS_PORT` move the published ports; `NEXUS_IMAGE` and
`GRAFANA_VERSION` choose other versions.

Provisioned data sources (`provisioning/datasources/datasources.yml`):

- **Nexus**: the user `grafana`, with every privilege the plugin can use.
- **Nexus (limited user)**: `grafana-limited`, which may only browse and
  search. Use it to see the messages about missing privileges.
- **Nexus (private network)**: reaches Nexus only through the proxy.

The dashboard **Nexus Repository overview** and three example alert rules are
provisioned too.

### The private network

`nexus` is also on a second Docker network, `private`, under the name
`nexus-private`. Grafana is not on that network, so it cannot resolve the
name; the `socks` proxy can. A data source with the secure socks proxy on
therefore reaches `http://nexus-private:8081`, and one without it does not.
That is how the tests prove that requests go through the proxy.

### Sample data

`devenv/nexus/seed.py` creates:

- a second blob store, `artifacts`, with a 10 GB quota;
- repositories `raw-hosted`, `docs-archive` (offline) and `npm-proxy`, next to
  the ones Nexus creates itself;
- nine Maven components in `maven-releases` (`billing`, `checkout`,
  `auth-client`, several versions each) and five files in `raw-hosted`;
- the users `grafana` and `grafana-limited`, whose generated passwords it
  writes to the `nexus-seed` volume for Grafana and the tests.

It can be run again without harm.

## Tests against the stack

```sh
devenv/run.sh npx playwright install chromium   # once, into the volume
devenv/run.sh npm run e2e                       # Playwright, tests/e2e
devenv/run.sh go test -tags integration ./tests/integration/...
```

Inside the toolbox these reach the compose services by name, and the passwords
are mounted at `/run/nexus`. To run them from elsewhere (as CI does), set
`NEXUS_URL` and `NEXUS_SEED_DIR`.

Playwright saves a screenshot of each step under `artifacts/app-tests/`, so you
can check what the tests saw. To regenerate the screenshots shipped with the
plugin:

```sh
devenv/run.sh bash -c 'DOCS_SCREENSHOTS=1 npx playwright test tests/e2e/docs-screenshots.spec.ts'
```

## Exposing the stack on the network

To reach the stack from other machines, turn off anonymous Admin and set an
admin password. The anonymous setting is built into the image, so rebuild:

```sh
BIND_ADDRESS=0.0.0.0 ANONYMOUS_AUTH_ENABLED=false \
GRAFANA_ADMIN_PASSWORD='<password>' NEXUS_ADMIN_PASSWORD='<another>' \
docker compose up -d --build --wait
```

Both passwords only take effect on a fresh stack (`docker compose down -v`
first). On an existing one, change Grafana's with
`docker compose exec grafana grafana cli admin reset-admin-password '<password>'`.
The e2e tests expect anonymous Admin, so run them against the default local
stack.
