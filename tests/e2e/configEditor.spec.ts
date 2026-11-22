import { test, expect } from '@grafana/plugin-e2e';
import { NEXUS_URL, password, shot, TYPE } from './helpers';

test('the plugin is installed and listed', async ({ page }) => {
  await page.goto(`/plugins/${TYPE}`);
  await expect(page.getByRole('heading', { name: 'Nexus Repository', exact: true })).toBeVisible({ timeout: 15_000 });
});

test('provisioned data source: Save & test reports the version and the visible repositories', async ({
  gotoDataSourceConfigPage,
  page,
}) => {
  const configPage = await gotoDataSourceConfigPage('nexus-dev');
  // The first page load after Grafana starts can take more than 5 s.
  await expect(page.locator('#nexus-url')).toHaveValue(NEXUS_URL, { timeout: 15_000 });
  await expect(page.locator('#nexus-username')).toHaveValue('grafana');
  // A saved password is write-only: it is never sent back to the browser.
  await expect(page.locator('#nexus-password')).toBeDisabled();
  await expect(page.locator('#nexus-password')).toHaveValue('configured');
  await expect(configPage.saveAndTest()).toBeOK();
  await expect(configPage).toHaveAlert('success', {
    hasText: /Connected to Nexus Repository 3\.[\d.-]+ \(Community Edition\)\. The user grafana can see 10 repositories\./,
  });
  await shot(page, 'config', 'provisioned-ok');
});

test('a user with fewer privileges connects, and is told what it cannot read', async ({ gotoDataSourceConfigPage, page }) => {
  const configPage = await gotoDataSourceConfigPage('nexus-limited');
  await expect(page.locator('#nexus-username')).toHaveValue('grafana-limited', { timeout: 15_000 });
  await expect(configPage.saveAndTest()).toBeOK();
  await expect(configPage).toHaveAlert('success', {
    hasText:
      'Not readable with this login: blob stores (nx-blobstores-read); tasks (nx-tasks-read); health checks, usage, formats and metrics (nx-metrics-all).',
  });
  await shot(page, 'config', 'limited-user');
});

test('a new data source: no login, a wrong password, then the right one', async ({ createDataSourceConfigPage, page }) => {
  const configPage = await createDataSourceConfigPage({ type: TYPE });
  await page.locator('#nexus-url').fill(NEXUS_URL);

  await expect(configPage.saveAndTest()).not.toBeOK();
  await expect(configPage).toHaveAlert('error', { hasText: 'This Nexus requires a login. Enter a user name and password' });
  await shot(page, 'config', 'login-required');

  // Edits made right after Save & test can be undone by Grafana reloading the
  // saved settings, so each step repeats until its expected outcome is seen.
  const user = page.locator('#nexus-username');
  const pass = page.locator('#nexus-password');
  const setLogin = async (value: string) => {
    await user.fill('grafana');
    if (await pass.isDisabled()) {
      await page.getByRole('button', { name: 'Reset' }).first().click();
    }
    await expect(pass).toBeEditable();
    await pass.fill(value);
  };

  await page.waitForLoadState('networkidle');
  await expect(async () => {
    await setLogin('not-the-password');
    await expect(configPage.saveAndTest()).not.toBeOK();
    await expect(configPage).toHaveAlert('error', { hasText: 'Nexus rejected the user name or password.' });
  }).toPass({ timeout: 30_000 });
  // The password goes over http:// here, and the page says so.
  await expect(page.getByText('Password over plain HTTP')).toBeVisible();
  await shot(page, 'config', 'wrong-password');

  await expect(async () => {
    await setLogin(password());
    await expect(configPage.saveAndTest()).toBeOK();
  }).toPass({ timeout: 30_000 });
  await expect(configPage).toHaveAlert('success', { hasText: 'The user grafana can see 10 repositories.' });
});

test('a user name without a password is a configuration problem', async ({ createDataSourceConfigPage, page }) => {
  const configPage = await createDataSourceConfigPage({ type: TYPE });
  await page.locator('#nexus-url').fill(NEXUS_URL);
  await page.locator('#nexus-username').fill('grafana');
  await expect(configPage.saveAndTest()).not.toBeOK();
  await expect(configPage).toHaveAlert('error', { hasText: 'Configuration problem: enter the password of the Nexus user "grafana"' });
});

test('URL problems are explained', async ({ createDataSourceConfigPage, page }) => {
  const configPage = await createDataSourceConfigPage({ type: TYPE });
  const url = page.locator('#nexus-url');
  const check = async (value: string, message: string | RegExp) => {
    await page.waitForLoadState('networkidle');
    await expect(async () => {
      await url.fill(value);
      await expect(configPage.saveAndTest()).not.toBeOK();
      await expect(configPage).toHaveAlert('error', { hasText: message });
    }).toPass({ timeout: 30_000 });
  };

  await expect(configPage.saveAndTest()).not.toBeOK();
  await expect(configPage).toHaveAlert('error', { hasText: 'Configuration problem: the Nexus URL is required' });
  await check('nexus:8081', 'the Nexus URL must start with http:// or https://');
  await check('http://nexus.invalid:8081', 'Cannot reach Nexus at http://nexus.invalid:8081: host not found.');
  await check('http://nexus:8081/wrong-path', 'did not answer like Nexus Repository 3. Check the URL');
  // Grafana itself is an HTTP server that is not Nexus.
  await check('http://localhost:3000', /did not answer like Nexus Repository 3|asked for a login/);
  await shot(page, 'config', 'not-nexus');
});

test('the address of a Nexus page or of its API works as the URL', async ({ createDataSourceConfigPage, page }) => {
  const configPage = await createDataSourceConfigPage({ type: TYPE });
  await page.locator('#nexus-url').fill(`${NEXUS_URL}/#browse/browse:maven-releases`);
  await page.locator('#nexus-username').fill('grafana');
  await page.locator('#nexus-password').fill(password());
  await expect(configPage.saveAndTest()).toBeOK();
  await expect(configPage).toHaveAlert('success', { hasText: 'Connected to Nexus Repository 3.' });
});

test('TLS options appear when switched on', async ({ createDataSourceConfigPage, page }) => {
  await createDataSourceConfigPage({ type: TYPE });
  await expect(page.locator('#nexus-tls-ca')).toBeHidden();
  await page.locator('#nexus-tls-with-ca').check({ force: true });
  await expect(page.locator('#nexus-tls-ca')).toBeVisible();
  await page.locator('#nexus-tls-client-auth').check({ force: true });
  await expect(page.locator('#nexus-tls-client-cert')).toBeVisible();
  await expect(page.locator('#nexus-tls-client-key')).toBeVisible();
  await expect(page.locator('#nexus-tls-server-name')).toBeVisible();
  await expect(page.locator('#nexus-timeout')).toHaveAttribute('placeholder', '30');
});
