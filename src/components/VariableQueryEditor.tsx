import React, { useEffect, useState } from 'react';
import { QueryEditorProps } from '@grafana/data';
import { Combobox, ComboboxOption, InlineField, RadioButtonGroup, Stack, Text } from '@grafana/ui';
import { DataSource } from '../datasource';
import { formatsOf, REPOSITORY_TYPES } from '../query';
import { NexusOptions, NexusQuery, NexusVariableQuery } from '../types';

type Props = QueryEditorProps<DataSource, NexusQuery, NexusOptions, NexusVariableQuery>;

const listOptions: Array<ComboboxOption<NonNullable<NexusVariableQuery['list']>>> = [
  { value: 'repositories', label: 'Repositories', description: 'The repository names' },
  { value: 'formats', label: 'Formats', description: 'The formats of the repositories, for example maven2 and npm' },
  { value: 'blobStores', label: 'Blob stores', description: 'The blob store names' },
];

/** The editor for a dashboard variable of type Query. */
export function VariableQueryEditor({ query, onChange, datasource }: Props) {
  const list = query.list ?? 'repositories';
  const [formats, setFormats] = useState<Array<ComboboxOption<string>>>([]);
  useEffect(() => {
    let live = true;
    datasource.getRepositories().then(
      (repos) => live && setFormats(formatsOf(repos).map((f) => ({ value: f, label: f }))),
      () => live && setFormats([])
    );
    return () => {
      live = false;
    };
  }, [datasource]);

  const set = (patch: Partial<NexusVariableQuery>) => onChange({ ...query, refId: query.refId || 'variable', ...patch });

  // A new variable has no query yet: save the default, so that its values
  // are listed without the user having to change something first.
  useEffect(() => {
    if (!query.list) {
      onChange({ refId: 'variable', list: 'repositories' });
    }
    // Once, when the editor opens.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <Stack direction="column" gap={1}>
      <InlineField label="List" labelWidth={14}>
        <Combobox id="nexus-variable-list" width={32} options={listOptions} value={list} onChange={(o) => set({ list: o.value })} />
      </InlineField>
      {list === 'repositories' && (
        <Stack direction="row" gap={1} wrap="wrap">
          <InlineField label="Format" labelWidth={14} tooltip="Only repositories of this format. You can also type another variable such as $format.">
            <Combobox
              id="nexus-variable-format"
              width={24}
              isClearable
              createCustomValue
              placeholder="Every format"
              options={formats}
              value={query.format || null}
              onChange={(o) => set({ format: o?.value?.trim() || undefined })}
            />
          </InlineField>
          <InlineField label="Type" labelWidth={8}>
            <RadioButtonGroup
              id="nexus-variable-repository-type"
              options={REPOSITORY_TYPES}
              value={query.repositoryType ?? ''}
              onChange={(repositoryType) => set({ repositoryType: repositoryType || undefined })}
            />
          </InlineField>
        </Stack>
      )}
      <Text variant="bodySmall" color="secondary">
        Use the variable in a query by choosing it in the Repositories or Format field. A multi-value variable selects
        several repositories at once.
      </Text>
    </Stack>
  );
}
