import * as api from '../api';
import { Dash } from '../components';
import { EntityListPage } from '../components/EntityListPage';
import { PolicyIcon } from '../icons';

function ReadyDot({ ready }: { ready?: boolean | null }) {
  if (ready === null || ready === undefined)
    return <span className="muted" title="Unknown">—</span>;
  return ready ? (
    <span className="pill pill-green" title="Ready">Ready</span>
  ) : (
    <span className="pill pill-red" title="Not ready">Not ready</span>
  );
}

function SeverityPill({ severity }: { severity?: string | null }) {
  if (!severity) return <Dash />;
  const cls: Record<string, string> = {
    critical: 'pill-red',
    high: 'pill-orange',
    medium: 'pill-yellow',
    low: 'pill-blue',
    info: 'pill-green',
  };
  return <span className={`pill ${cls[severity.toLowerCase()] || ''}`}>{severity}</span>;
}

export function ClusterPolicies() {
  return (
    <EntityListPage<api.ClusterPolicy>
      title="Cluster Policies"
      icon={<PolicyIcon size={20} />}
      storageKey="lists.cluster-policies"
      emptyMessage="No Kyverno policies found. Ensure the collector is running and policies_enabled is on."
      fetchPage={(params, cursor, limit) =>
        api.listClusterPolicies({ ...params, cursor, limit })
      }
      rowKey={(c) => c.id}
      columns={[
        {
          key: 'name',
          label: 'Name',
          sortKey: 'name',
          render: (c) => <strong>{c.name}</strong>,
        },
        {
          key: 'resource_type',
          label: 'Kind',
          sortKey: 'scope',
          render: (c) => <code>{c.resource_type}</code>,
        },
        {
          key: 'category',
          label: 'Category',
          sortKey: 'category',
          render: (c) => c.category || <Dash />,
        },
        {
          key: 'severity',
          label: 'Severity',
          sortKey: 'severity',
          render: (c) => <SeverityPill severity={c.severity} />,
        },
        {
          key: 'action',
          label: 'Action',
          sortKey: 'action',
          render: (c) =>
            c.action ? <span className="pill">{c.action}</span> : <Dash />,
        },
        {
          key: 'failure_policy',
          label: 'Failure Policy',
          sortKey: 'failure_policy',
          render: (c) => c.failure_policy || <Dash />,
        },
        {
          key: 'background',
          label: 'Background',
          sortKey: 'background',
          render: (c) =>
            c.background != null ? (c.background ? 'yes' : 'no') : <Dash />,
        },
        {
          key: 'rules_count',
          label: 'Rules',
          sortKey: 'rules_count',
          render: (c) => c.rules_count ?? <Dash />,
        },
        {
          key: 'ready',
          label: 'Ready',
          sortKey: 'ready',
          render: (c) => <ReadyDot ready={c.ready} />,
        },
      ]}
    />
  );
}

export function PolicyReports() {
  return (
    <EntityListPage<api.PolicyReport>
      title="Policy Reports"
      icon={<PolicyIcon size={20} />}
      storageKey="lists.policy-reports"
      emptyMessage="No policy reports found. Ensure the collector is running and policies_enabled is on."
      fetchPage={(params, cursor, limit) =>
        api.listPolicyReports({ ...params, cursor, limit })
      }
      rowKey={(r) => r.id}
      columns={[
        {
          key: 'name',
          label: 'Name',
          sortKey: 'name',
          render: (r) => <strong>{r.name}</strong>,
        },
        {
          key: 'scope_kind',
          label: 'Scope Kind',
          sortKey: 'scope_kind',
          render: (r) => r.scope_kind || <Dash />,
        },
        {
          key: 'scope_name',
          label: 'Scope Name',
          sortKey: 'scope_name',
          render: (r) => r.scope_name || <Dash />,
        },
        {
          key: 'pass',
          label: 'Pass',
          render: (r) => r.summary_pass ?? 0,
        },
        {
          key: 'fail',
          label: 'Fail',
          render: (r) =>
            r.summary_fail ? (
              <span className="pill pill-red">{r.summary_fail}</span>
            ) : (
              0
            ),
        },
        {
          key: 'warn',
          label: 'Warn',
          render: (r) =>
            r.summary_warn ? (
              <span className="pill pill-yellow">{r.summary_warn}</span>
            ) : (
              0
            ),
        },
        {
          key: 'error',
          label: 'Error',
          render: (r) =>
            r.summary_error ? (
              <span className="pill pill-orange">{r.summary_error}</span>
            ) : (
              0
            ),
        },
        {
          key: 'skip',
          label: 'Skip',
          render: (r) => r.summary_skip ?? 0,
        },
      ]}
    />
  );
}
