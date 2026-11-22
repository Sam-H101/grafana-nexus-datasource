import { test, expect } from '@grafana/plugin-e2e';
import { column, DS_NAME, framesOf, nextQuery, pick, shot, typeCustom } from './helpers';

test.beforeEach(async ({ panelEditPage }) => {
  await panelEditPage.datasource.set(DS_NAME);
});

test('a new query lists the repositories, and the filters narrow it', async ({ panelEditPage, page }) => {
  const row = panelEditPage.getQueryEditorRow('A');
  await expect(row.locator('#nexus-query-type')).toHaveValue('Repositories');
  await expect(row.locator('#nexus-repositories')).toHaveAttribute('placeholder', 'Every repository');

  let response = nextQuery(page, '"queryType":"repositories"', '"format":"maven2"');
  await pick(page, row.locator('#nexus-format'), 'maven2');
  let frames = await framesOf(await response);
  expect(column(frames[0], 'name')).toEqual(['maven-central', 'maven-public', 'maven-releases', 'maven-snapshots']);
  expect(column(frames[0], 'blob_store')).toEqual(['default', 'default', 'default', 'default']);

  response = nextQuery(page, '"repositoryType":"proxy"');
  await row.getByRole('radio', { name: 'Proxy', exact: true }).check({ force: true });
  frames = await framesOf(await response);
  expect(column(frames[0], 'name')).toEqual(['maven-central']);
  expect(column(frames[0], 'remote_url')).toEqual(['https://repo1.maven.org/maven2/']);

  await panelEditPage.setVisualization('Table');
  await expect(page.getByRole('gridcell', { name: 'maven-central' }).first()).toBeVisible({ timeout: 15_000 });
  await shot(page, 'query', 'repositories');
});

test('components: search by repository, name and version, sorted', async ({ panelEditPage, page }) => {
  const row = panelEditPage.getQueryEditorRow('A');
  let response = nextQuery(page, '"queryType":"components"');
  await pick(page, row.locator('#nexus-query-type'), 'Components');
  let frames = await framesOf(await response);
  // 9 Maven and 5 raw components were seeded.
  expect(column(frames[0], 'name')).toHaveLength(14);
  await expect(row.locator('#nexus-limit')).toHaveValue('100');

  response = nextQuery(page, '"repositories":["maven-releases"]');
  await pick(page, row.locator('#nexus-repositories'), 'maven-releases');
  frames = await framesOf(await response);
  expect(column(frames[0], 'name')).toHaveLength(9);

  response = nextQuery(page, '"name":"billing"');
  await row.locator('#nexus-name').fill('billing');
  await row.locator('#nexus-name').press('Enter');
  await response;

  // Descending only shows once a sort is chosen.
  await expect(row.locator('#nexus-descending')).toBeHidden();
  response = nextQuery(page, '"sort":"version"');
  await pick(page, row.locator('#nexus-sort'), 'Version');
  frames = await framesOf(await response);
  expect(column(frames[0], 'version')).toEqual(['1.0.0', '1.1.0', '1.2.0', '2.0.0']);
  expect(column(frames[0], 'group')).toEqual(Array(4).fill('com.example.shop'));

  response = nextQuery(page, '"descending":true');
  await row.locator('#nexus-descending').check({ force: true });
  frames = await framesOf(await response);
  expect(column(frames[0], 'version')).toEqual(['2.0.0', '1.2.0', '1.1.0', '1.0.0']);

  // A limit below the number of matches says so.
  response = nextQuery(page, '"limit":2');
  await row.locator('#nexus-limit').fill('2');
  await row.locator('#nexus-limit').press('Enter');
  frames = await framesOf(await response);
  expect(column(frames[0], 'version')).toEqual(['2.0.0', '1.2.0']);
  await expect(row.getByTestId('nexus-query-notice')).toContainText('Showing the first 2 components');

  await panelEditPage.setVisualization('Table');
  await expect(page.getByRole('gridcell', { name: 'billing' }).first()).toBeVisible({ timeout: 15_000 });
  await shot(page, 'query', 'components');
});

test('a limit out of range is explained instead of sent', async ({ panelEditPage, page }) => {
  const row = panelEditPage.getQueryEditorRow('A');
  const first = nextQuery(page, '"queryType":"assets"');
  await pick(page, row.locator('#nexus-query-type'), 'Assets');
  await first;
  await row.locator('#nexus-limit').fill('20000');
  await row.locator('#nexus-limit').press('Enter');
  await expect(row.getByTestId('nexus-query-hint')).toHaveText('The limit must be between 1 and 10000.');
});

test('assets: files of a repository with their upload time', async ({ panelEditPage, page }) => {
  const row = panelEditPage.getQueryEditorRow('A');
  let response = nextQuery(page, '"queryType":"assets"');
  await pick(page, row.locator('#nexus-query-type'), 'Assets');
  await response;
  response = nextQuery(page, '"repositories":["raw-hosted"]');
  await pick(page, row.locator('#nexus-repositories'), 'raw-hosted');
  const frames = await framesOf(await response);
  expect(frames[0].schema.fields[0].name).toBe('uploaded');
  expect((column(frames[0], 'path') as string[]).sort()).toEqual([
    '/tools/cli/1.1.0/cli-1.1.0.bin',
    '/tools/cli/1.2.0/cli-1.2.0.bin',
    '/tools/cli/1.3.0/cli-1.3.0.bin',
    '/tools/installer/2025.10/installer-2025.10.bin',
    '/tools/installer/2025.11/installer-2025.11.bin',
  ]);
  for (const size of column(frames[0], 'size') as number[]) {
    expect(size).toBeGreaterThan(50_000);
  }
});

test('a repository that does not exist is reported by name', async ({ panelEditPage, page }) => {
  const row = panelEditPage.getQueryEditorRow('A');
  const first = nextQuery(page, '"queryType":"components"');
  await pick(page, row.locator('#nexus-query-type'), 'Components');
  await first;
  const response = nextQuery(page, 'no-such-repo');
  await typeCustom(page, row.locator('#nexus-repositories'), 'no-such-repo');
  const body = await (await response).json();
  expect(body.results.A.error).toContain('the repository "no-such-repo" does not exist');
  await expect(page.getByText('the repository "no-such-repo" does not exist').first()).toBeVisible({ timeout: 15_000 });
  await shot(page, 'query', 'unknown-repository');
});

test('blob stores, tasks and health checks need no settings', async ({ panelEditPage, page }) => {
  const row = panelEditPage.getQueryEditorRow('A');

  let response = nextQuery(page, '"queryType":"blobStores"');
  await pick(page, row.locator('#nexus-query-type'), 'Blob stores');
  let frames = await framesOf(await response);
  expect(column(frames[0], 'name')).toEqual(['artifacts', 'default']);
  expect(column(frames[0], 'quota')).toEqual([10737418240, null]);
  await expect(row.locator('#nexus-repositories')).toBeHidden();

  // As one number per blob store, which is what an alert rule needs.
  response = nextQuery(page, '"blobStoreValue":"free"');
  await row.getByRole('radio', { name: 'Free space', exact: true }).check({ force: true });
  frames = await framesOf(await response);
  expect(frames[0].schema.fields.map((f) => f.config?.displayNameFromDS ?? f.name)).toEqual(['time', 'artifacts', 'default']);
  expect(frames[0].data.values[1][0] as number).toBeGreaterThan(1_000_000);

  response = nextQuery(page, '"queryType":"tasks"');
  await pick(page, row.locator('#nexus-query-type'), 'Tasks');
  frames = await framesOf(await response);
  expect(column(frames[0], 'name')).toContain('Cleanup service');

  response = nextQuery(page, '"queryType":"healthChecks"');
  await pick(page, row.locator('#nexus-query-type'), 'Health checks');
  frames = await framesOf(await response);
  const all = column(frames[0], 'healthy') as boolean[];
  expect(all.length).toBeGreaterThan(10);
  expect(column(frames[0], 'check')).toContain('Blob Stores Ready');

  response = nextQuery(page, '"unhealthyOnly":true');
  await row.locator('#nexus-unhealthy-only').check({ force: true });
  frames = await framesOf(await response);
  expect(column(frames[0], 'healthy')).toEqual(all.filter((h) => !h));
  await panelEditPage.setVisualization('Table');
  await shot(page, 'query', 'health-checks');
});

test('usage over time is a time series, per day or per month', async ({ panelEditPage, page }) => {
  const row = panelEditPage.getQueryEditorRow('A');
  let response = nextQuery(page, '"queryType":"usage"', '"usageMetric":"requests"', '"usagePeriod":"daily"');
  await pick(page, row.locator('#nexus-query-type'), 'Usage over time');
  let frames = await framesOf(await response);
  expect(frames[0].schema.fields.map((f) => f.name)).toEqual(['time', 'requests']);

  response = nextQuery(page, '"usageMetric":"components"');
  await row.getByRole('radio', { name: 'Components', exact: true }).check({ force: true });
  await response;
  response = nextQuery(page, '"usageMetric":"components"', '"usagePeriod":"monthly"');
  await row.getByRole('radio', { name: 'Monthly', exact: true }).check({ force: true });
  frames = await framesOf(await response);
  expect(frames[0].schema.fields.map((f) => f.name)).toEqual(['time', 'components']);
  expect(column(frames[0], 'components').length).toBeGreaterThan(0);

  response = nextQuery(page, '"queryType":"monthlyUsage"');
  await pick(page, row.locator('#nexus-query-type'), 'Monthly usage');
  frames = await framesOf(await response);
  expect(frames[0].schema.fields.map((f) => f.name)).toEqual(['month', 'requests', 'components', 'peak_storage', 'egress']);
});

test('internal metrics: a list of everything, then chosen metrics as numbers', async ({ panelEditPage, page }) => {
  const row = panelEditPage.getQueryEditorRow('A');
  let response = nextQuery(page, '"queryType":"metrics"');
  await pick(page, row.locator('#nexus-query-type'), 'Internal metrics');
  let frames = await framesOf(await response);
  expect(frames[0].schema.fields.map((f) => f.name)).toEqual(['metric', 'value']);
  expect(column(frames[0], 'metric').length).toBeGreaterThan(100);

  response = nextQuery(page, 'jvm.memory.heap.used');
  await pick(page, row.locator('#nexus-metrics'), 'jvm.memory.heap.used');
  frames = await framesOf(await response);
  expect(frames[0].schema.fields.map((f) => f.config?.displayNameFromDS ?? f.name)).toEqual(['time', 'jvm.memory.heap.used']);
  expect(frames[0].data.values[1][0] as number).toBeGreaterThan(1_000_000);

  response = nextQuery(page, 'nexus.analytics.repository_total_count');
  await pick(page, row.locator('#nexus-metrics'), 'nexus.analytics.repository_total_count');
  frames = await framesOf(await response);
  expect(frames[0].schema.fields.map((f) => f.config?.displayNameFromDS)).toEqual([undefined, 'jvm.memory.heap.used', 'nexus.analytics.repository_total_count']);
  expect(frames[0].data.values[2]).toEqual([10]);

  await panelEditPage.setVisualization('Stat');
  await shot(page, 'query', 'metrics');
});

test('totals by format', async ({ panelEditPage, page }) => {
  const row = panelEditPage.getQueryEditorRow('A');
  const response = nextQuery(page, '"queryType":"formats"');
  await pick(page, row.locator('#nexus-query-type'), 'Totals by format');
  const frames = await framesOf(await response);
  const formats = column(frames[0], 'format') as string[];
  expect(formats).toEqual(expect.arrayContaining(['maven2', 'raw']));
  expect(column(frames[0], 'repositories')[formats.indexOf('maven2')]).toBe(4);
});

test('a user without the privilege is told which one to grant', async ({ panelEditPage, page }) => {
  await panelEditPage.datasource.set('Nexus (limited user)');
  const row = panelEditPage.getQueryEditorRow('A');
  const response = nextQuery(page, '"queryType":"blobStores"');
  await pick(page, row.locator('#nexus-query-type'), 'Blob stores');
  const body = await (await response).json();
  expect(body.results.A.error).toBe(
    'The Nexus user is not allowed to read blob stores. Give one of its roles the privilege nx-blobstores-read.'
  );
  await expect(page.getByText('Give one of its roles the privilege nx-blobstores-read').first()).toBeVisible({ timeout: 15_000 });

  // The metric list cannot be loaded either, and the editor says why.
  const metrics = nextQuery(page, '"queryType":"metrics"');
  await pick(page, row.locator('#nexus-query-type'), 'Internal metrics');
  await metrics;
  await expect(row.getByTestId('nexus-load-error')).toContainText('nx-metrics-all');
  await shot(page, 'query', 'missing-privilege');

  // What it may read works.
  const repos = nextQuery(page, '"queryType":"repositories"');
  await pick(page, row.locator('#nexus-query-type'), 'Repositories');
  const frames = await framesOf(await repos);
  expect(column(frames[0], 'name')).toHaveLength(10);
  await expect(row.getByTestId('nexus-query-notice')).toContainText('nx-repository-admin-*-*-read');
});
