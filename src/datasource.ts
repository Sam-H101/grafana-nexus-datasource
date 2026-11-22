import { AnnotationSupport, CoreApp, DataSourceInstanceSettings, ScopedVars } from '@grafana/data';
import { DataSourceWithBackend, getTemplateSrv } from '@grafana/runtime';
import { DEFAULT_QUERY, expandList, missing } from './query';
import { NexusOptions, NexusQuery, RepositoryInfo } from './types';
import { NexusVariableSupport } from './variables';

export class DataSource extends DataSourceWithBackend<NexusQuery, NexusOptions> {
  private repositoryList?: Promise<RepositoryInfo[]>;
  private metricList?: Promise<string[]>;

  constructor(instanceSettings: DataSourceInstanceSettings<NexusOptions>) {
    super(instanceSettings);
    this.variables = new NexusVariableSupport(this);
    // Standard annotation support: the query editor is reused, and the
    // result's first time field and its text fields become the annotation.
    const annotations: AnnotationSupport<NexusQuery> = {
      getDefaultQuery: () => ({ queryType: 'assets', repositories: [], limit: 100 }),
    };
    this.annotations = annotations;
  }

  getDefaultQuery(_: CoreApp): Partial<NexusQuery> {
    return DEFAULT_QUERY;
  }

  /** Incomplete queries are not sent: the editor says what is missing. */
  filterQuery(query: NexusQuery): boolean {
    return !query.hide && missing(query) === null;
  }

  applyTemplateVariables(query: NexusQuery, scopedVars: ScopedVars): NexusQuery {
    const templateSrv = getTemplateSrv();
    const list = (values?: string[]) => expandList(values, (v) => templateSrv.replace(v, scopedVars, 'csv'));
    const text = (value?: string) => (value ? templateSrv.replace(value, scopedVars) : value);
    return {
      ...query,
      repositories: list(query.repositories),
      metrics: list(query.metrics),
      // A variable set to All or to several values becomes a comma-separated list.
      format: query.format ? templateSrv.replace(query.format, scopedVars, 'csv') : query.format,
      keyword: text(query.keyword),
      group: text(query.group),
      name: text(query.name),
      version: text(query.version),
    };
  }

  /** Repositories for the editors' dropdowns, fetched once per data source instance. */
  getRepositories(): Promise<RepositoryInfo[]> {
    if (!this.repositoryList) {
      this.repositoryList = this.getResource<RepositoryInfo[]>('repositories').catch((err) => {
        this.repositoryList = undefined;
        throw err;
      });
    }
    return this.repositoryList;
  }

  /** The names of the internal metrics Nexus reports. */
  getMetricNames(): Promise<string[]> {
    if (!this.metricList) {
      this.metricList = this.getResource<string[]>('metrics').catch((err) => {
        this.metricList = undefined;
        throw err;
      });
    }
    return this.metricList;
  }
}
