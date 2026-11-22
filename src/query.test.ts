import { createDataFrame, FieldType } from '@grafana/data';
import {
  COMMON_METRICS,
  DEFAULT_LIMIT,
  defaultsFor,
  expandList,
  formatsOf,
  isVariable,
  missing,
  QUERY_TYPES,
  queryTypeInfo,
  toTextValueFrame,
  variableToQuery,
} from './query';
import { NexusQuery, QueryType } from './types';

// The backend's list (pkg/models/query.go): the two must stay the same.
const BACKEND_TYPES: QueryType[] = [
  'repositories',
  'formats',
  'blobStores',
  'components',
  'assets',
  'tasks',
  'healthChecks',
  'usage',
  'monthlyUsage',
  'metrics',
];

describe('query types', () => {
  it('offers every type the backend knows, once', () => {
    expect(QUERY_TYPES.map((t) => t.value).sort()).toEqual([...BACKEND_TYPES].sort());
  });

  it('describes each type and names the privilege it needs', () => {
    for (const t of QUERY_TYPES) {
      expect(t.label).not.toBe('');
      expect(t.description.length).toBeGreaterThan(20);
      expect(t.privilege).toMatch(/^nx-/);
    }
  });

  it('falls back to repositories for an unknown or missing type', () => {
    expect(queryTypeInfo(undefined).value).toBe('repositories');
    expect(queryTypeInfo('nonsense' as QueryType).value).toBe('repositories');
  });

  it('offers only metric names without spaces first', () => {
    for (const m of COMMON_METRICS) {
      expect(m).toMatch(/^[\w.-]+$/);
    }
  });
});

describe('defaultsFor', () => {
  const full: NexusQuery = {
    refId: 'B',
    hide: true,
    datasource: { uid: 'nexus-dev' },
    queryType: 'components',
    repositories: ['maven-releases'],
    format: 'maven2',
    repositoryType: 'hosted',
    keyword: 'billing',
    group: 'com.example',
    name: 'bill*',
    version: '1.*',
    sort: 'version',
    descending: true,
    limit: 25,
    usageMetric: 'components',
    usagePeriod: 'monthly',
    metrics: ['jvm.memory.heap.used'],
    unhealthyOnly: true,
  };

  it('keeps the search when switching between components and assets', () => {
    expect(defaultsFor('assets', full)).toEqual({
      refId: 'B',
      hide: true,
      datasource: { uid: 'nexus-dev' },
      queryType: 'assets',
      repositories: ['maven-releases'],
      format: 'maven2',
      keyword: 'billing',
      group: 'com.example',
      name: 'bill*',
      version: '1.*',
      sort: 'version',
      descending: true,
      limit: 25,
    });
  });

  it('keeps the repository filter for the repository list, and nothing of the search', () => {
    expect(defaultsFor('repositories', full)).toEqual({
      refId: 'B',
      hide: true,
      datasource: { uid: 'nexus-dev' },
      queryType: 'repositories',
      repositories: ['maven-releases'],
      format: 'maven2',
      repositoryType: 'hosted',
    });
  });

  it('drops everything a type does not use', () => {
    expect(defaultsFor('tasks', { ...full, hide: undefined, datasource: undefined })).toEqual({ refId: 'B', queryType: 'tasks' });
    expect(defaultsFor('blobStores', { refId: 'A' })).toEqual({ refId: 'A', queryType: 'blobStores' });
  });

  it('gives each type its own defaults', () => {
    expect(defaultsFor('usage', { refId: 'A' })).toEqual({ refId: 'A', queryType: 'usage', usageMetric: 'requests', usagePeriod: 'daily' });
    expect(defaultsFor('usage', full)).toMatchObject({ usageMetric: 'components', usagePeriod: 'monthly' });
    expect(defaultsFor('metrics', { refId: 'A' })).toEqual({ refId: 'A', queryType: 'metrics', metrics: [] });
    expect(defaultsFor('components', { refId: 'A' })).toEqual({ refId: 'A', queryType: 'components', limit: DEFAULT_LIMIT });
    expect(defaultsFor('healthChecks', full)).toMatchObject({ queryType: 'healthChecks', unhealthyOnly: true });
    expect(defaultsFor('healthChecks', { refId: 'A' })).toEqual({ refId: 'A', queryType: 'healthChecks' });
    expect(defaultsFor('blobStores', { refId: 'A', blobStoreValue: 'free' })).toEqual({ refId: 'A', queryType: 'blobStores', blobStoreValue: 'free' });
    expect(defaultsFor('tasks', { refId: 'A', blobStoreValue: 'free' })).toEqual({ refId: 'A', queryType: 'tasks' });
  });
});

describe('missing', () => {
  it('lets every type run as it is', () => {
    for (const type of BACKEND_TYPES) {
      expect(missing(defaultsFor(type, { refId: 'A' }))).toBeNull();
    }
    expect(missing({ refId: 'A' })).toBeNull();
  });

  it('stops a limit out of range', () => {
    for (const limit of [0, -5, 10001, 1.5, NaN]) {
      expect(missing({ refId: 'A', queryType: 'assets', limit })).toMatch(/between 1 and 10000/);
    }
    expect(missing({ refId: 'A', queryType: 'assets', limit: 10000 })).toBeNull();
  });
});

describe('variables in queries', () => {
  it('recognises the three ways to write a variable', () => {
    for (const v of ['$repo', '${repo}', '${repo:csv}', '[[repo]]', 'prefix-$repo']) {
      expect(isVariable(v)).toBe(true);
    }
    for (const v of ['maven-releases', 'costs$', '$', '100$ off', '']) {
      expect(isVariable(v)).toBe(false);
    }
  });

  it('expands a multi-value variable into several entries', () => {
    const replace = (v: string) => v.replace('$repos', 'maven-releases,raw-hosted').replace('$none', '');
    expect(expandList(['$repos', 'npm-proxy', 'raw-hosted', ' ', '$none'], replace)).toEqual(['maven-releases', 'raw-hosted', 'npm-proxy']);
    expect(expandList(undefined, replace)).toEqual([]);
  });

  it('does not run plain names through the replacer', () => {
    const replace = jest.fn((v: string) => v);
    expandList(['maven-releases'], replace);
    expect(replace).not.toHaveBeenCalled();
  });
});

describe('dashboard variable queries', () => {
  const replace = (v: string) => v.replace('$format', 'npm');

  it('lists repositories, narrowed by format and type', () => {
    expect(variableToQuery({ refId: '' }, replace)).toEqual({ refId: 'variable', queryType: 'repositories', format: undefined, repositoryType: undefined });
    expect(variableToQuery({ refId: 'A', list: 'repositories', format: ' $format ', repositoryType: 'proxy' }, replace)).toEqual({
      refId: 'A',
      queryType: 'repositories',
      format: 'npm',
      repositoryType: 'proxy',
    });
  });

  it('lists formats from every repository, and blob stores', () => {
    expect(variableToQuery({ refId: 'A', list: 'formats', format: 'npm' }, replace)).toEqual({ refId: 'A', queryType: 'repositories' });
    expect(variableToQuery({ refId: 'A', list: 'blobStores' }, replace)).toEqual({ refId: 'A', queryType: 'blobStores' });
  });

  const frame = createDataFrame({
    refId: 'A',
    fields: [
      { name: 'name', type: FieldType.string, values: ['raw-hosted', 'maven-releases', 'maven-central', 'unknown'] },
      { name: 'format', type: FieldType.string, values: ['raw', 'maven2', 'maven2', ''] },
    ],
  });

  it('turns names into options in the order given', () => {
    const out = toTextValueFrame(frame, 'name');
    expect(out.refId).toBe('A');
    expect(out.fields.map((f) => f.name)).toEqual(['text', 'value']);
    expect(out.fields[0].values).toEqual(['raw-hosted', 'maven-releases', 'maven-central', 'unknown']);
    expect(out.fields[1].values).toEqual(out.fields[0].values);
  });

  it('turns formats into distinct, sorted options', () => {
    expect(toTextValueFrame(frame, 'format').fields[1].values).toEqual(['maven2', 'raw']);
  });

  it('copes with an empty result', () => {
    expect(toTextValueFrame(createDataFrame({ fields: [] }), 'name').fields[0].values).toEqual([]);
  });
});

describe('formatsOf', () => {
  it('lists each format once, sorted', () => {
    expect(
      formatsOf([
        { name: 'a', format: 'npm', type: 'proxy' },
        { name: 'b', format: 'maven2', type: 'hosted' },
        { name: 'c', format: 'npm', type: 'hosted' },
        { name: 'd', format: '', type: 'hosted' },
      ])
    ).toEqual(['maven2', 'npm']);
  });
});
