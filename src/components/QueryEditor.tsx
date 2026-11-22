import React, { useEffect, useMemo, useState } from 'react';
import { QueryEditorProps } from '@grafana/data';
import { getTemplateSrv } from '@grafana/runtime';
import {
  Alert,
  Combobox,
  ComboboxOption,
  InlineField,
  InlineSwitch,
  Input,
  MultiCombobox,
  RadioButtonGroup,
  Stack,
  Text,
} from '@grafana/ui';
import { DataSource } from '../datasource';
import {
  BLOB_STORE_VALUES,
  COMMON_METRICS,
  DEFAULT_LIMIT,
  defaultsFor,
  formatsOf,
  MAX_LIMIT,
  missing,
  QUERY_TYPES,
  queryTypeInfo,
  REPOSITORY_TYPES,
  SORTS,
  USAGE_METRICS,
  USAGE_PERIODS,
} from '../query';
import { NexusOptions, NexusQuery, QueryType, RepositoryInfo, SearchSort } from '../types';

type Props = QueryEditorProps<DataSource, NexusQuery, NexusOptions>;
type Option = ComboboxOption<string>;

const LABEL_WIDTH = 14;

const queryTypeOptions: Option[] = QUERY_TYPES.map((t) => ({ value: t.value, label: t.label, description: t.description }));
const sortOptions: Option[] = SORTS.map((s) => ({ ...s }));

/** Loads something for the editor and reports a failure as text. `enabled` delays it until needed. */
function useLoaded<T>(load: () => Promise<T>, datasource: DataSource, enabled = true): { data?: T; error?: string } {
  const [state, setState] = useState<{ data?: T; error?: string }>({});
  useEffect(() => {
    if (!enabled) {
      return;
    }
    let live = true;
    load().then(
      (data) => live && setState({ data }),
      (err) => live && setState({ error: err?.data?.message ?? err?.message ?? 'The request failed.' })
    );
    return () => {
      live = false;
    };
    // `load` is a method of `datasource`; reload only when that changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [datasource, enabled]);
  return state;
}

/** The selected values as options, keeping ones that are not in the list (typed names, variables). */
function selected(values: string[] | undefined, options: Option[]): Option[] {
  return (values ?? []).map((v) => options.find((o) => o.value === v) ?? { value: v, label: v });
}

interface TextFieldProps {
  id: string;
  label: string;
  tooltip: string;
  placeholder: string;
  saved: string;
  width?: number;
  onSave: (value: string) => void;
}

/** A text field that is saved, and the query run, when it loses focus or Enter is pressed. */
function TextField({ id, label, tooltip, placeholder, saved, width = 24, onSave }: TextFieldProps) {
  // The text follows the saved value when that changes from outside.
  const [draft, setDraft] = useState({ base: saved, text: saved });
  const text = draft.base === saved ? draft.text : saved;
  const save = () => {
    if (text.trim() !== saved) {
      onSave(text.trim());
    }
  };
  return (
    <InlineField label={label} labelWidth={LABEL_WIDTH} tooltip={tooltip}>
      <Input
        id={id}
        width={width}
        placeholder={placeholder}
        value={text}
        onChange={(e) => setDraft({ base: saved, text: e.currentTarget.value })}
        onBlur={save}
        onKeyDown={(e) => e.key === 'Enter' && save()}
      />
    </InlineField>
  );
}

export function QueryEditor({ query, onChange, onRunQuery, datasource, data }: Props) {
  const info = queryTypeInfo(query.queryType);
  const repositories = useLoaded<RepositoryInfo[]>(() => datasource.getRepositories(), datasource);
  // Metric names need their own privilege: ask only when they are wanted.
  const metrics = useLoaded<string[]>(() => datasource.getMetricNames(), datasource, info.value === 'metrics');
  const [limit, setLimit] = useState<string | null>(null);

  // Dashboard variables can be chosen wherever a repository or format can.
  const variableOptions = useMemo<Option[]>(
    () =>
      getTemplateSrv()
        .getVariables()
        .map((v) => ({ value: `$${v.name}`, label: `$${v.name}`, description: 'Dashboard variable', group: 'Variables' })),
    []
  );
  const repositoryOpts = useMemo<Option[]>(
    () => [
      ...variableOptions,
      ...(repositories.data ?? []).map((r) => ({
        value: r.name,
        label: r.name,
        description: `${r.format} (${r.type})`,
        group: 'Repositories',
      })),
    ],
    [repositories.data, variableOptions]
  );
  const formatOpts = useMemo<Option[]>(
    () => [...variableOptions, ...formatsOf(repositories.data ?? []).map((f) => ({ value: f, label: f, group: 'Formats' }))],
    [repositories.data, variableOptions]
  );
  const metricOpts = useMemo<Option[]>(() => {
    const names = metrics.data ?? [];
    const common = COMMON_METRICS.filter((m) => names.includes(m)).map((m) => ({ value: m, label: m, group: 'Common' }));
    const rest = names.filter((m) => !COMMON_METRICS.includes(m)).map((m) => ({ value: m, label: m, group: 'All metrics' }));
    return [...variableOptions, ...common, ...rest];
  }, [metrics.data, variableOptions]);

  // A query that has never been edited has no type yet: give it the defaults
  // (Grafana applies getDefaultQuery in some places only).
  useEffect(() => {
    if (!query.queryType) {
      onChange(defaultsFor('repositories', query));
    }
    // Once, when the editor opens.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const update = (patch: Partial<NexusQuery>) => {
    onChange({ ...query, ...patch });
    onRunQuery();
  };
  const values = (opts: Option[]) => opts.map((o) => o.value.trim()).filter(Boolean);

  const onQueryType = (option: Option) => {
    onChange(defaultsFor(option.value as QueryType, query));
    onRunQuery();
  };

  const saveLimit = () => {
    if (limit === null) {
      return;
    }
    const n = parseInt(limit, 10);
    setLimit(null);
    if (Number.isFinite(n) && n !== query.limit) {
      update({ limit: n });
    }
  };

  const hint = missing(query);
  const notice = data?.series?.flatMap((s) => s.meta?.notices ?? []).map((n) => n.text)[0];
  const loadError = repositories.error ?? (info.value === 'metrics' ? metrics.error : undefined);

  return (
    <Stack direction="column" gap={1}>
      <InlineField label="Query" labelWidth={LABEL_WIDTH} tooltip={`${info.description}. Needs the privilege ${info.privilege}.`}>
        <Combobox id="nexus-query-type" width={40} options={queryTypeOptions} value={info.value} onChange={onQueryType} />
      </InlineField>

      {info.repositories && (
        <InlineField
          label="Repositories"
          labelWidth={LABEL_WIDTH}
          grow
          tooltip="Leave empty for every repository. You can also type a repository name or choose a dashboard variable."
        >
          <MultiCombobox
            id="nexus-repositories"
            width="auto"
            minWidth={40}
            maxWidth={100}
            isClearable
            createCustomValue
            placeholder="Every repository"
            options={repositoryOpts}
            value={selected(query.repositories, repositoryOpts)}
            onChange={(opts) => update({ repositories: values(opts) })}
          />
        </InlineField>
      )}

      {info.format && (
        <Stack direction="row" gap={1} wrap="wrap">
          <InlineField label="Format" labelWidth={LABEL_WIDTH} tooltip="Only this format, for example maven2, npm or docker. Leave empty for every format.">
            <Combobox
              id="nexus-format"
              width={24}
              isClearable
              createCustomValue
              placeholder="Every format"
              options={formatOpts}
              value={query.format || null}
              onChange={(o) => update({ format: o?.value?.trim() || undefined })}
            />
          </InlineField>
          {info.value === 'repositories' && (
            <InlineField label="Type" labelWidth={8} tooltip="Hosted repositories store what you publish, proxies cache a remote repository, and groups combine others.">
              <RadioButtonGroup
                id="nexus-repository-type"
                options={REPOSITORY_TYPES}
                value={query.repositoryType ?? ''}
                onChange={(repositoryType) => update({ repositoryType: repositoryType || undefined })}
              />
            </InlineField>
          )}
        </Stack>
      )}

      {info.search && (
        <>
          <Stack direction="row" gap={1} wrap="wrap">
            <TextField
              id="nexus-keyword"
              label="Keyword"
              tooltip="Words to find anywhere in a component: its name, group, version or other attributes."
              placeholder="Anything"
              saved={query.keyword ?? ''}
              onSave={(keyword) => update({ keyword: keyword || undefined })}
            />
            <TextField
              id="nexus-group"
              label="Group"
              tooltip="The group, namespace or scope, for example com.example for Maven. * matches any text."
              placeholder="Any group"
              saved={query.group ?? ''}
              onSave={(group) => update({ group: group || undefined })}
            />
          </Stack>
          <Stack direction="row" gap={1} wrap="wrap">
            <TextField
              id="nexus-name"
              label="Name"
              tooltip="The component name. * matches any text, for example billing*."
              placeholder="Any name"
              saved={query.name ?? ''}
              onSave={(name) => update({ name: name || undefined })}
            />
            <TextField
              id="nexus-version"
              label="Version"
              tooltip="The version. * matches any text, for example 1.*."
              placeholder="Any version"
              saved={query.version ?? ''}
              onSave={(version) => update({ version: version || undefined })}
            />
          </Stack>
          <Stack direction="row" gap={1} wrap="wrap">
            <InlineField label="Sort by" labelWidth={LABEL_WIDTH} tooltip="The order Nexus returns results in, which decides what the limit keeps.">
              <Combobox
                id="nexus-sort"
                width={24}
                options={sortOptions}
                value={query.sort ?? ''}
                onChange={(o) => update({ sort: (o.value as SearchSort) || undefined })}
              />
            </InlineField>
            {query.sort && (
              <InlineField label="Descending" labelWidth={LABEL_WIDTH}>
                <InlineSwitch
                  id="nexus-descending"
                  value={!!query.descending}
                  onChange={(e) => update({ descending: e.currentTarget.checked })}
                />
              </InlineField>
            )}
            <InlineField label="Limit" labelWidth={8} tooltip={`The most ${info.value} to return, up to ${MAX_LIMIT}.`}>
              <Input
                id="nexus-limit"
                type="number"
                width={12}
                min={1}
                max={MAX_LIMIT}
                value={limit ?? String(query.limit ?? DEFAULT_LIMIT)}
                onChange={(e) => setLimit(e.currentTarget.value)}
                onBlur={saveLimit}
                onKeyDown={(e) => e.key === 'Enter' && saveLimit()}
              />
            </InlineField>
          </Stack>
        </>
      )}

      {info.value === 'blobStores' && (
        <InlineField
          label="Show"
          labelWidth={LABEL_WIDTH}
          tooltip="The table has every detail. For an alert rule, a stat or a gauge, choose one number per blob store instead."
        >
          <RadioButtonGroup
            id="nexus-blob-store-value"
            options={BLOB_STORE_VALUES}
            value={query.blobStoreValue ?? ''}
            onChange={(blobStoreValue) => update({ blobStoreValue: blobStoreValue || undefined })}
          />
        </InlineField>
      )}

      {info.value === 'healthChecks' && (
        <InlineField label="Failing only" labelWidth={LABEL_WIDTH} tooltip="Only the checks that fail. Off: every check.">
          <InlineSwitch
            id="nexus-unhealthy-only"
            value={!!query.unhealthyOnly}
            onChange={(e) => update({ unhealthyOnly: e.currentTarget.checked })}
          />
        </InlineField>
      )}

      {info.value === 'usage' && (
        <Stack direction="row" gap={1} wrap="wrap">
          <InlineField label="Count" labelWidth={LABEL_WIDTH} tooltip="Requests served, or components stored.">
            <RadioButtonGroup
              id="nexus-usage-metric"
              options={USAGE_METRICS}
              value={query.usageMetric ?? 'requests'}
              onChange={(usageMetric) => update({ usageMetric })}
            />
          </InlineField>
          <InlineField label="Per" labelWidth={8} tooltip="Nexus keeps a figure per day for recent days and one per month for the last year.">
            <RadioButtonGroup
              id="nexus-usage-period"
              options={USAGE_PERIODS}
              value={query.usagePeriod ?? 'daily'}
              onChange={(usagePeriod) => update({ usagePeriod })}
            />
          </InlineField>
        </Stack>
      )}

      {info.value === 'metrics' && (
        <InlineField
          label="Metrics"
          labelWidth={LABEL_WIDTH}
          grow
          tooltip="The metrics to return, each as a number field with its current value. Leave empty to list every metric and its value."
        >
          <MultiCombobox
            id="nexus-metrics"
            width="auto"
            minWidth={40}
            maxWidth={120}
            isClearable
            createCustomValue
            placeholder="List every metric"
            options={metricOpts}
            value={selected(query.metrics, metricOpts)}
            onChange={(opts) => update({ metrics: values(opts) })}
          />
        </InlineField>
      )}

      {hint && (
        <Text color="info" data-testid="nexus-query-hint">
          {hint}
        </Text>
      )}
      {!hint && notice && (
        <Text color="info" data-testid="nexus-query-notice">
          {notice}
        </Text>
      )}
      {loadError && (
        <Alert severity="error" title="Could not load the lists from Nexus" data-testid="nexus-load-error">
          {loadError} Check the data source settings with Save &amp; test.
        </Alert>
      )}
    </Stack>
  );
}
