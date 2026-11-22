import { DataSourcePlugin } from '@grafana/data';
import { ConfigEditor } from './components/ConfigEditor';
import { QueryEditor } from './components/QueryEditor';
import { DataSource } from './datasource';
import { NexusOptions, NexusQuery } from './types';

export const plugin = new DataSourcePlugin<DataSource, NexusQuery, NexusOptions>(DataSource)
  .setConfigEditor(ConfigEditor)
  .setQueryEditor(QueryEditor);
