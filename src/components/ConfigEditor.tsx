import React, { ChangeEvent } from 'react';
import {
  DataSourcePluginOptionsEditorProps,
  onUpdateDatasourceJsonDataOption,
  onUpdateDatasourceSecureJsonDataOption,
  updateDatasourcePluginResetOption,
} from '@grafana/data';
import { config } from '@grafana/runtime';
import {
  Alert,
  Field,
  Input,
  SecretInput,
  SecretTextArea,
  SecureSocksProxySettings,
  Stack,
  Switch,
  Text,
} from '@grafana/ui';
import { NexusOptions, NexusSecureJsonData } from '../types';

type Props = DataSourcePluginOptionsEditorProps<NexusOptions, NexusSecureJsonData>;

const WIDTH = 50;

export function ConfigEditor(props: Props) {
  const { options, onOptionsChange } = props;
  const { jsonData, secureJsonFields } = options;
  const plainHttp = options.url?.trim().toLowerCase().startsWith('http://');
  const passwordSet = !!secureJsonFields?.password || !!options.secureJsonData?.password;

  const setJson = <K extends keyof NexusOptions>(key: K, value: NexusOptions[K]) =>
    onOptionsChange({ ...options, jsonData: { ...jsonData, [key]: value } });

  const onTimeout = (e: ChangeEvent<HTMLInputElement>) => {
    const n = parseInt(e.currentTarget.value, 10);
    setJson('timeoutSeconds', Number.isFinite(n) ? n : undefined);
  };

  return (
    <Stack direction="column" gap={3}>
      <section>
        <Text element="h3">Connection</Text>
        <Field
          label="URL"
          description="The address you open Nexus Repository at, including any path prefix, for example https://nexus.example.com or https://tools.example.com/nexus."
          required
        >
          <Input
            id="nexus-url"
            width={WIDTH}
            value={options.url || ''}
            placeholder="https://nexus.example.com"
            onChange={(e) => onOptionsChange({ ...options, url: e.currentTarget.value })}
          />
        </Field>
      </section>

      <section>
        <Text element="h3">Authentication</Text>
        <Field
          label="User name"
          description="A Nexus user for Grafana. The plugin only reads. The README lists the privilege each query needs, and Save & test reports any that are missing. Leave empty only if your Nexus allows anonymous access."
        >
          <Input
            id="nexus-username"
            width={WIDTH}
            autoComplete="off"
            placeholder="grafana"
            value={jsonData.username || ''}
            onChange={onUpdateDatasourceJsonDataOption(props, 'username')}
          />
        </Field>
        <Field label="Password" description="The user's password, or the pass code of a user token with its name code as the user name.">
          <SecretInput
            id="nexus-password"
            width={WIDTH}
            autoComplete="new-password"
            isConfigured={!!secureJsonFields?.password}
            value={options.secureJsonData?.password || ''}
            onChange={onUpdateDatasourceSecureJsonDataOption(props, 'password')}
            onReset={() => updateDatasourcePluginResetOption(props, 'password')}
          />
        </Field>
        {plainHttp && passwordSet && (
          <Alert severity="warning" title="Password over plain HTTP">
            The URL uses http://, so the password is sent unencrypted. Use https:// unless Grafana and Nexus share a
            trusted network.
          </Alert>
        )}
      </section>

      {config.secureSocksDSProxyEnabled && (
        <section>
          <Text element="h3">Private network</Text>
          <SecureSocksProxySettings options={options} onOptionsChange={onOptionsChange} />
        </section>
      )}

      <section>
        <Text element="h3">TLS</Text>
        <Field label="Custom CA certificate" description="Verify Nexus' certificate against this CA (PEM), for a private or internal CA.">
          <Switch
            id="nexus-tls-with-ca"
            value={!!jsonData.tlsAuthWithCACert}
            onChange={(e) => setJson('tlsAuthWithCACert', e.currentTarget.checked)}
          />
        </Field>
        {jsonData.tlsAuthWithCACert && (
          <Field label="CA certificate">
            <SecretTextArea
              id="nexus-tls-ca"
              cols={60}
              rows={6}
              placeholder="-----BEGIN CERTIFICATE-----"
              isConfigured={!!secureJsonFields?.tlsCACert}
              onChange={onUpdateDatasourceSecureJsonDataOption(props, 'tlsCACert')}
              onReset={() => updateDatasourcePluginResetOption(props, 'tlsCACert')}
            />
          </Field>
        )}
        <Field label="Client certificate" description="Present a client certificate to Nexus (mutual TLS), if a proxy in front of it requires one.">
          <Switch id="nexus-tls-client-auth" value={!!jsonData.tlsAuth} onChange={(e) => setJson('tlsAuth', e.currentTarget.checked)} />
        </Field>
        {jsonData.tlsAuth && (
          <>
            <Field label="Server name" description="Expected name in the server's certificate, if it differs from the URL host.">
              <Input
                id="nexus-tls-server-name"
                width={WIDTH}
                value={jsonData.serverName || ''}
                onChange={onUpdateDatasourceJsonDataOption(props, 'serverName')}
              />
            </Field>
            <Field label="Client certificate (PEM)">
              <SecretTextArea
                id="nexus-tls-client-cert"
                cols={60}
                rows={6}
                placeholder="-----BEGIN CERTIFICATE-----"
                isConfigured={!!secureJsonFields?.tlsClientCert}
                onChange={onUpdateDatasourceSecureJsonDataOption(props, 'tlsClientCert')}
                onReset={() => updateDatasourcePluginResetOption(props, 'tlsClientCert')}
              />
            </Field>
            <Field label="Client key (PEM)">
              <SecretTextArea
                id="nexus-tls-client-key"
                cols={60}
                rows={6}
                placeholder="-----BEGIN PRIVATE KEY-----"
                isConfigured={!!secureJsonFields?.tlsClientKey}
                onChange={onUpdateDatasourceSecureJsonDataOption(props, 'tlsClientKey')}
                onReset={() => updateDatasourcePluginResetOption(props, 'tlsClientKey')}
              />
            </Field>
          </>
        )}
        <Field label="Skip TLS certificate verification" description="Insecure. Only for testing.">
          <Switch
            id="nexus-tls-skip-verify"
            value={!!jsonData.tlsSkipVerify}
            onChange={(e) => setJson('tlsSkipVerify', e.currentTarget.checked)}
          />
        </Field>
      </section>

      <section>
        <Text element="h3">Advanced</Text>
        <Field
          label="Query timeout (seconds)"
          description="How long one panel query may take. A search over several repositories makes several requests. Default 30."
        >
          <Input
            id="nexus-timeout"
            type="number"
            min={1}
            max={300}
            width={20}
            placeholder="30"
            value={jsonData.timeoutSeconds ?? ''}
            onChange={onTimeout}
          />
        </Field>
      </section>
    </Stack>
  );
}
