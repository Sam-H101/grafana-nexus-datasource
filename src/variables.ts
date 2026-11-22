import { CustomVariableSupport, DataFrame, DataQueryRequest, DataQueryResponse } from '@grafana/data';
import { getTemplateSrv } from '@grafana/runtime';
import { Observable, map } from 'rxjs';
import { VariableQueryEditor } from './components/VariableQueryEditor';
import type { DataSource } from './datasource';
import { toTextValueFrame, variableToQuery } from './query';
import { NexusQuery, NexusVariableQuery } from './types';

export class NexusVariableSupport extends CustomVariableSupport<DataSource, NexusVariableQuery, NexusQuery> {
  editor = VariableQueryEditor;

  constructor(private readonly datasource: DataSource) {
    super();
  }

  query(request: DataQueryRequest<NexusVariableQuery>): Observable<DataQueryResponse> {
    const templateSrv = getTemplateSrv();
    const targets = request.targets.map((t) => variableToQuery(t, (v) => templateSrv.replace(v, request.scopedVars, 'csv')));
    const column = request.targets[0]?.list === 'formats' ? 'format' : 'name';
    return this.datasource
      .query({ ...request, targets })
      .pipe(map((resp) => ({ ...resp, data: resp.data.map((f: DataFrame) => toTextValueFrame(f, column)) })));
  }
}
