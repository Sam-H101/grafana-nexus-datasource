import { existsSync, readFileSync } from 'node:fs';
import type { Locator, Page, Response } from '@playwright/test';

export const TYPE = 'smartquinoa1504-nexus-datasource';
export const DS_NAME = 'Nexus';

/** As Grafana reaches the dev stack's Nexus (docker-compose.yaml). */
export const NEXUS_URL = 'http://nexus:8081';
/** The same server under the name only the socks proxy can resolve. */
export const NEXUS_PRIVATE_URL = 'http://nexus-private:8081';

/** The password the seed created for a user (devenv/nexus/seed.py). */
export function password(file: 'password' | 'limited-password' = 'password'): string {
  const path = `${process.env.NEXUS_SEED_DIR ?? '/run/nexus'}/${file}`;
  if (!existsSync(path)) {
    throw new Error(`No Nexus password: start the dev stack, or set NEXUS_SEED_DIR (looked for ${path})`);
  }
  return readFileSync(path, 'utf8').trim();
}

/**
 * Save a full-page screenshot under artifacts/app-tests/<group>/<name>.png so
 * a person can check what the test saw.
 */
export async function shot(page: Page, group: string, name: string): Promise<void> {
  await page.screenshot({ path: `artifacts/app-tests/${group}/${name}.png`, fullPage: true });
}

/**
 * Choose an option in one of the editor's dropdowns: type `text` to filter,
 * then click the option whose name starts with `option` (default: `text`).
 * Dropdowns that accept custom values list "<text> Use custom value" first,
 * which is never what this picks.
 */
export async function pick(page: Page, input: Locator, text: string, option?: string): Promise<void> {
  await input.click();
  await input.fill(text);
  await page
    .getByRole('option', { name: new RegExp(`^${escapeRegExp(option ?? text)}`) })
    .filter({ hasNotText: 'Use custom value' })
    .first()
    .click();
}

function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

/** Type a value that is not in a dropdown's list (a name, a variable) and accept it. */
export async function typeCustom(page: Page, input: Locator, text: string): Promise<void> {
  await input.click();
  await input.fill(text);
  await page.getByRole('option').filter({ hasText: text }).first().click();
}

/** The next query response whose request body contains every given fragment. */
export function nextQuery(page: Page, ...fragments: string[]): Promise<Response> {
  return page.waitForResponse((r) => {
    if (!r.url().includes('/api/ds/query')) {
      return false;
    }
    const body = r.request().postData() ?? '';
    return fragments.every((f) => body.includes(f));
  });
}

export interface Frame {
  schema: { fields: Array<{ name: string; config?: { displayNameFromDS?: string; displayName?: string } }> };
  data: { values: unknown[][] };
}

/** The frames of query A in a query response, failing on an error. */
export async function framesOf(resp: Response, refId = 'A'): Promise<Frame[]> {
  const body = await resp.json();
  const result = body.results?.[refId];
  if (!result) {
    throw new Error(`no result for ${refId}: ${JSON.stringify(body).slice(0, 300)}`);
  }
  if (result.error) {
    throw new Error(`query ${refId} failed: ${result.error}`);
  }
  return result.frames ?? [];
}

/** The values of one field of a frame, by field name. */
export function column(frame: Frame, name: string): unknown[] {
  const i = frame.schema.fields.findIndex((f) => f.name === name);
  if (i < 0) {
    throw new Error(`no field ${name} in ${frame.schema.fields.map((f) => f.name).join(', ')}`);
  }
  return frame.data.values[i];
}

/** Run one query through Grafana's API and return its first frame. */
export async function runQuery(page: Page, uid: string, query: Record<string, unknown>): Promise<Frame> {
  const now = Date.now();
  const resp = await page.request.post('/api/ds/query', {
    data: {
      from: String(now - 30 * 24 * 3600 * 1000),
      to: String(now),
      queries: [{ refId: 'A', datasource: { type: TYPE, uid }, ...query }],
    },
  });
  const result = (await resp.json()).results?.A;
  if (!result || result.error) {
    throw new Error(`query failed: ${result?.error ?? resp.status()}`);
  }
  return result.frames[0];
}
