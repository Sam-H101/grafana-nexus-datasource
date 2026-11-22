import { DataSourceJsonData } from '@grafana/data';
import { DataQuery } from '@grafana/schema';

export type QueryType =
  | 'repositories'
  | 'formats'
  | 'blobStores'
  | 'components'
  | 'assets'
  | 'tasks'
  | 'healthChecks'
  | 'usage'
  | 'monthlyUsage'
  | 'metrics';

export type RepositoryType = '' | 'hosted' | 'proxy' | 'group';
export type SearchSort = '' | 'name' | 'group' | 'version' | 'repository';
export type UsageMetric = 'requests' | 'components';
export type UsagePeriod = 'daily' | 'monthly';
export type BlobStoreValue = '' | 'free' | 'size' | 'blobs';

/** One query. The backend validates it (pkg/models/query.go). */
export interface NexusQuery extends DataQuery {
  queryType?: QueryType;
  /** Repository names or dashboard variables. Empty: every repository. */
  repositories?: string[];
  format?: string;
  repositoryType?: RepositoryType;
  keyword?: string;
  group?: string;
  name?: string;
  version?: string;
  sort?: SearchSort;
  descending?: boolean;
  limit?: number;
  usageMetric?: UsageMetric;
  usagePeriod?: UsagePeriod;
  /** Internal metric names. Empty: list every metric. */
  metrics?: string[];
  unhealthyOnly?: boolean;
  /** Blob stores as one number each instead of a table. */
  blobStoreValue?: BlobStoreValue;
}

/** A dashboard variable of type Query. */
export interface NexusVariableQuery extends DataQuery {
  list?: 'repositories' | 'formats' | 'blobStores';
  /** Repositories only: keep one format (or a variable such as $format). */
  format?: string;
  /** Repositories only: keep hosted, proxy or group repositories. */
  repositoryType?: RepositoryType;
}

export interface NexusOptions extends DataSourceJsonData {
  username?: string;
  timeoutSeconds?: number;
  tlsAuth?: boolean;
  tlsAuthWithCACert?: boolean;
  tlsSkipVerify?: boolean;
  serverName?: string;
  enableSecureSocksProxy?: boolean;
}

export interface NexusSecureJsonData {
  password?: string;
  tlsCACert?: string;
  tlsClientCert?: string;
  tlsClientKey?: string;
}

/** A repository as the backend lists it for the editors. */
export interface RepositoryInfo {
  name: string;
  format: string;
  type: string;
}
