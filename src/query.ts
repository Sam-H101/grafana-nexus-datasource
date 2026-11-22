import { createDataFrame, DataFrame, Field, FieldType } from '@grafana/data';
import { NexusQuery, NexusVariableQuery, QueryType, RepositoryInfo } from './types';

export interface QueryTypeInfo {
  value: QueryType;
  label: string;
  description: string;
  /** The query can be narrowed to repositories. */
  repositories?: boolean;
  /** The query can be narrowed to a format. */
  format?: boolean;
  /** The query searches: keyword, group, name, version, sort and limit. */
  search?: boolean;
  /** What a Nexus user needs to run it. */
  privilege: string;
}

export const QUERY_TYPES: QueryTypeInfo[] = [
  {
    value: 'repositories',
    label: 'Repositories',
    description: 'Every repository with its format, type, status and storage',
    repositories: true,
    format: true,
    privilege: 'nx-repository-view-*-*-browse',
  },
  {
    value: 'formats',
    label: 'Totals by format',
    description: 'Repositories, components, assets and bytes transferred for each format',
    privilege: 'nx-metrics-all',
  },
  {
    value: 'blobStores',
    label: 'Blob stores',
    description: 'Size, free space, blob count and quota of each blob store',
    privilege: 'nx-blobstores-read',
  },
  {
    value: 'components',
    label: 'Components',
    description: 'Search packages and artifacts: versions, size, last update and last download',
    repositories: true,
    format: true,
    search: true,
    privilege: 'nx-search-read',
  },
  {
    value: 'assets',
    label: 'Assets',
    description: 'Search files: upload time, size, uploader and last download. Also for annotations',
    repositories: true,
    format: true,
    search: true,
    privilege: 'nx-search-read',
  },
  {
    value: 'tasks',
    label: 'Tasks',
    description: 'Scheduled tasks: state, last result, last and next run',
    privilege: 'nx-tasks-read',
  },
  {
    value: 'healthChecks',
    label: 'Health checks',
    description: 'The system status checks and which of them fail',
    privilege: 'nx-metrics-all',
  },
  {
    value: 'usage',
    label: 'Usage over time',
    description: 'Requests or components per day or per month, as a time series',
    privilege: 'nx-metrics-all',
  },
  {
    value: 'monthlyUsage',
    label: 'Monthly usage',
    description: 'Requests, components, peak storage and data served for each of the last twelve months',
    privilege: 'nx-metrics-all',
  },
  {
    value: 'metrics',
    label: 'Internal metrics',
    description: 'Current values from the Nexus process: memory, threads, request rates and more',
    privilege: 'nx-metrics-all',
  },
];

export function queryTypeInfo(type?: QueryType): QueryTypeInfo {
  return QUERY_TYPES.find((t) => t.value === type) ?? QUERY_TYPES[0];
}

export const REPOSITORY_TYPES = [
  { label: 'All', value: '' as const },
  { label: 'Hosted', value: 'hosted' as const },
  { label: 'Proxy', value: 'proxy' as const },
  { label: 'Group', value: 'group' as const },
];

export const SORTS = [
  { label: 'Relevance', value: '' as const, description: 'The order Nexus finds them in' },
  { label: 'Name', value: 'name' as const },
  { label: 'Group', value: 'group' as const },
  { label: 'Version', value: 'version' as const },
  { label: 'Repository', value: 'repository' as const },
];

export const USAGE_METRICS = [
  { label: 'Requests', value: 'requests' as const },
  { label: 'Components', value: 'components' as const },
];

export const BLOB_STORE_VALUES = [
  { label: 'Table', value: '' as const, description: 'Every detail of each blob store' },
  { label: 'Free space', value: 'free' as const, description: 'One number per blob store, for alert rules and gauges' },
  { label: 'Size', value: 'size' as const, description: 'One number per blob store' },
  { label: 'Blob count', value: 'blobs' as const, description: 'One number per blob store' },
];

export const USAGE_PERIODS = [
  { label: 'Daily', value: 'daily' as const },
  { label: 'Monthly', value: 'monthly' as const },
];

/** Metrics offered first in the editor, when Nexus reports them. */
export const COMMON_METRICS = [
  'nexus.analytics.component_total_count',
  'nexus.analytics.repository_total_count',
  'jvm.memory.heap.used',
  'jvm.memory.heap.max',
  'jvm.memory.heap.usage',
  'jvm.thread-states.count',
  'jvm.vm.uptime',
  'org.sonatype.nexus.metrics.InstrumentedFilter.activeRequests',
  'org.sonatype.nexus.metrics.InstrumentedFilter.responseCodes.ok.m1_rate',
  'org.sonatype.nexus.metrics.InstrumentedFilter.responseCodes.serverError.m1_rate',
  'org.sonatype.nexus.internal.metrics.ReadOnlyMetricSet.enabled',
];

export const DEFAULT_LIMIT = 100;
export const MAX_LIMIT = 10000;

export const DEFAULT_QUERY: Partial<NexusQuery> = { queryType: 'repositories' };

/**
 * The query after choosing a type: settings the new type does not use are
 * dropped, so a saved query holds only what it means.
 */
export function defaultsFor(type: QueryType, query: NexusQuery): NexusQuery {
  const info = queryTypeInfo(type);
  const next: NexusQuery = { refId: query.refId, queryType: type };
  for (const key of ['datasource', 'hide', 'key'] as const) {
    if (query[key] !== undefined) {
      (next as unknown as Record<string, unknown>)[key] = query[key];
    }
  }
  if (info.repositories && query.repositories?.length) {
    next.repositories = query.repositories;
  }
  if (info.format && query.format) {
    next.format = query.format;
  }
  if (info.search) {
    for (const key of ['keyword', 'group', 'name', 'version'] as const) {
      if (query[key]) {
        next[key] = query[key];
      }
    }
    if (query.sort) {
      next.sort = query.sort;
      next.descending = query.descending;
    }
    next.limit = query.limit ?? DEFAULT_LIMIT;
  }
  if (type === 'repositories' && query.repositoryType) {
    next.repositoryType = query.repositoryType;
  }
  if (type === 'usage') {
    next.usageMetric = query.usageMetric ?? 'requests';
    next.usagePeriod = query.usagePeriod ?? 'daily';
  }
  if (type === 'metrics') {
    next.metrics = query.metrics ?? [];
  }
  if (type === 'blobStores' && query.blobStoreValue) {
    next.blobStoreValue = query.blobStoreValue;
  }
  if (type === 'healthChecks' && query.unhealthyOnly) {
    next.unhealthyOnly = true;
  }
  return next;
}

/** What stops a query from running, or null. Every query type can run as it is. */
export function missing(query: NexusQuery): string | null {
  if (query.limit !== undefined && (!Number.isInteger(query.limit) || query.limit < 1 || query.limit > MAX_LIMIT)) {
    return `The limit must be between 1 and ${MAX_LIMIT}.`;
  }
  return null;
}

export function isVariable(value: string): boolean {
  return /\$\{?[A-Za-z_]\w*|\[\[\w+/.test(value);
}

/**
 * Replaces variables in a list. A multi-value variable becomes several
 * entries: `replace` must format values comma-separated.
 */
export function expandList(list: string[] | undefined, replace: (value: string) => string): string[] {
  const out: string[] = [];
  for (const entry of list ?? []) {
    const expanded = isVariable(entry) ? replace(entry) : entry;
    for (const v of expanded.split(',')) {
      const trimmed = v.trim();
      if (trimmed && !out.includes(trimmed)) {
        out.push(trimmed);
      }
    }
  }
  return out;
}

/** The query a dashboard variable runs. */
export function variableToQuery(variable: NexusVariableQuery, replace: (v: string) => string): NexusQuery {
  const refId = variable.refId || 'variable';
  switch (variable.list) {
    case 'blobStores':
      return { refId, queryType: 'blobStores' };
    case 'formats':
      return { refId, queryType: 'repositories' };
    default:
      return {
        refId,
        queryType: 'repositories',
        format: variable.format ? replace(variable.format).trim() : undefined,
        repositoryType: variable.repositoryType || undefined,
      };
  }
}

/** The formats of the listed repositories, sorted. */
export function formatsOf(repositories: RepositoryInfo[]): string[] {
  return [...new Set(repositories.map((r) => r.format).filter(Boolean))].sort();
}

/**
 * Turns a result into the text/value frame Grafana builds variable options
 * from. `column` is the field whose distinct values become the options: the
 * repository or blob store name, or the format of the repositories.
 */
export function toTextValueFrame(frame: DataFrame, column: string): DataFrame {
  const field = frame.fields.find((f: Field) => f.name === column) ?? frame.fields[0];
  const seen = new Set<string>();
  for (const v of field?.values ?? []) {
    if (v !== null && v !== undefined && String(v) !== '') {
      seen.add(String(v));
    }
  }
  const values = column === 'format' ? [...seen].sort() : [...seen];
  return createDataFrame({
    refId: frame.refId,
    fields: [
      { name: 'text', type: FieldType.string, values },
      { name: 'value', type: FieldType.string, values },
    ],
  });
}
