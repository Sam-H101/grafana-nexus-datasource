import { test, expect } from '@grafana/plugin-e2e';
import type { Page, Response } from '@playwright/test';
import { column, runQuery, shot } from './helpers';

interface Seen {
  body: string;
  result: { error?: string; frames?: Array<{ schema: { fields: Array<{ name: string }> }; data: { values: unknown[][] } }> };
}

/** Collects every query result of the page. */
function watchQueries(page: Page): Seen[] {
  const seen: Seen[] = [];
  page.on('response', async (r: Response) => {
    if (!r.url().includes('/api/ds/query')) {
      return;
    }
    const body = r.request().postData() ?? '';
    try {
      const json = await r.json();
      for (const result of Object.values(json.results ?? {})) {
        seen.push({ body, result: result as Seen['result'] });
      }
    } catch {
      // The page navigated away before the body was read.
    }
  });
  return seen;
}

test('the provisioned dashboard draws every panel from Nexus', async ({ gotoDashboardPage, page }) => {
  const seen = watchQueries(page);
  await gotoDashboardPage({ uid: 'nexus-overview' });

  await expect(page.getByRole('gridcell', { name: 'artifacts' }).first()).toBeVisible({ timeout: 30_000 });
  // Panels further down load when scrolled to.
  for (const title of ['Repository details', 'Component list', 'Totals by format', 'Tasks', 'Monthly usage']) {
    const heading = page.getByRole('heading', { name: title, exact: true }).first();
    await heading.scrollIntoViewIfNeeded();
    await expect(heading).toBeVisible();
  }
  for (const type of ['metrics', 'healthChecks', 'blobStores', 'usage', 'repositories', 'components', 'formats', 'tasks', 'monthlyUsage']) {
    await expect.poll(() => seen.filter((s) => s.body.includes(`"queryType":"${type}"`)).length, { timeout: 30_000 }).toBeGreaterThan(0);
  }
  expect(seen.filter((s) => s.result.error).map((s) => s.result.error)).toEqual([]);
  // "All" repositories and formats: every component is found.
  const components = seen.find((s) => s.body.includes('"queryType":"components"'))!;
  expect(column(components.result.frames![0], 'name')).toHaveLength(14);

  await page.getByRole('heading', { name: 'Blob stores', exact: true }).first().scrollIntoViewIfNeeded();
  await shot(page, 'dashboard', 'overview');
});

test('the format and repository variables filter the panels', async ({ gotoDashboardPage, page }) => {
  // The variables' own queries.
  const formats = await runQuery(page, 'nexus-dev', { queryType: 'repositories' });
  expect([...new Set(column(formats, 'format') as string[])].sort()).toEqual(['maven2', 'npm', 'nuget', 'raw']);

  const seen = watchQueries(page);
  await gotoDashboardPage({
    uid: 'nexus-overview',
    queryParams: new URLSearchParams({ 'var-format': 'raw', 'var-repository': 'raw-hosted' }),
  });
  await expect(page.getByTestId('data-testid Dashboard template variables submenu Label Format').locator('..')).toContainText('raw', {
    timeout: 30_000,
  });
  // The repository table holds the raw repositories only.
  await expect(page.getByRole('gridcell', { name: 'docs-archive' }).first()).toBeVisible({ timeout: 30_000 });
  await expect(page.getByRole('gridcell', { name: 'maven-central' })).toHaveCount(0);

  const heading = page.getByRole('heading', { name: 'Component list', exact: true }).first();
  await heading.scrollIntoViewIfNeeded();
  await expect.poll(() => seen.filter((s) => s.body.includes('"queryType":"components"')).length, { timeout: 30_000 }).toBeGreaterThan(0);
  const components = seen.find((s) => s.body.includes('"queryType":"components"'))!;
  expect(components.body).toContain('"repositories":["raw-hosted"]');
  expect(components.body).toContain('"format":"raw"');
  expect(column(components.result.frames![0], 'repository')).toEqual(Array(5).fill('raw-hosted'));
  await shot(page, 'dashboard', 'raw-only');
});

test('a query variable editor offers repositories, formats and blob stores', async ({ page }) => {
  await page.goto('/dashboard/new?editview=variables');
  await page.getByTestId('data-testid Call to action button Add variable').click();
  const list = page.locator('#nexus-variable-list');
  await expect(list).toBeVisible({ timeout: 30_000 });
  await expect(list).toHaveValue('Repositories');
  await expect(page.locator('#nexus-variable-format')).toBeVisible();
  // The preview lists the repository names.
  await expect(page.getByText('maven-releases').first()).toBeVisible({ timeout: 30_000 });

  await list.click();
  await page.getByRole('option', { name: /^Blob stores/ }).click();
  await expect(page.locator('#nexus-variable-format')).toBeHidden();
  await expect(page.getByText('artifacts').first()).toBeVisible({ timeout: 30_000 });
  await shot(page, 'dashboard', 'variable-editor');
});

test('assets work as an annotation query', async ({ page }) => {
  const frame = await runQuery(page, 'nexus-dev', { queryType: 'assets', repositories: ['raw-hosted'], limit: 100 });
  // The first field is the time of each annotation.
  expect(frame.schema.fields[0].name).toBe('uploaded');
  const times = column(frame, 'uploaded') as number[];
  expect(times).toHaveLength(5);
  for (const t of times) {
    expect(Math.abs(Date.now() - t)).toBeLessThan(366 * 24 * 3600 * 1000);
  }
});

test('the provisioned alert rules evaluate on the server', async ({ page }) => {
  test.setTimeout(150_000);
  const states: Record<string, string | undefined> = {};
  await expect
    .poll(
      async () => {
        const resp = await page.request.get('/api/prometheus/grafana/api/v1/rules');
        const groups = (await resp.json()).data?.groups ?? [];
        for (const rule of groups.flatMap((g: { rules: Array<{ name: string; state: string; health: string }> }) => g.rules)) {
          states[rule.name] = `${rule.state}/${rule.health}`;
        }
        return states;
      },
      { timeout: 120_000, intervals: [3000] }
    )
    .toEqual({
      // Healthy heap: evaluated without error, not firing.
      'Nexus Java heap above 90%': 'inactive/ok',
      // One instance per blob store, none of them short of space.
      'Blob store disk almost full': 'inactive/ok',
      // The sample Nexus holds 14 components.
      'More than 5 components stored': 'firing/ok',
    });
});
