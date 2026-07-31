// Workload detail — its pods (via workload_id) + nodes they run on +
// containers (serves the "application = workload" view).

import { useState } from 'react';
import { Link, useParams } from 'react-router';
import * as api from '../../api';
import { useResource, usePagedList, useLocalListControls } from '../../hooks';
import { useMe, canEdit } from '../../me';
import { ApplicationCard } from '../../components/inventory/ApplicationCard';
import { EffectiveDICTCard } from '../../components/inventory/EffectiveDICTCard';
import { ImpactSection } from '../ImpactGraph';
import { ContainerVersionBadge } from '../../components/ContainerVersionBadge';
import { OriginLine } from '../../components/OriginLine';
import { WorkloadIcon } from '../../icons';
import { NetworkRulesTab } from '../../components/network/NetworkRulesTab';
import {
  AsyncView,
  ClusterLink,
  Dash,
  KV,
  Labels,
  LayerPill,
  SectionTitle,
  Empty,
} from '../../components';
import { ListSection } from '../../components/ListSection';
import { TabBar } from './shared';

type WorkloadTab = 'overview' | 'network-rules';

// --- WorkloadDetail child section -----------------------------------------

function WorkloadPodsSection({ workloadId }: { workloadId: string }) {
  const controls = useLocalListControls();
  const pods = usePagedList<api.Pod>(
    (cursor, limit) => api.listPods({ workload_id: workloadId, ...controls.params, cursor, limit }),
    [workloadId, ...controls.deps],
  );
  // Nodes running this workload are derived from the current pods page only.
  // This is intentional: pagination means we only see nodes for the visible page.
  const nodes = Array.from(
    new Set(pods.items.map((p) => p.node_name).filter(Boolean)),
  ) as string[];

  return (
    <>
      <ListSection
        title="Pods"
        list={pods}
        controls={controls}
        emptyMessage="No pods currently point at this workload."
        rowKey={(p) => p.id}
        columns={[
          { key: 'name', label: 'Name', sortKey: 'name', link: (p) => `/pods/${p.id}`, render: (p) => p.name },
          { key: 'phase', label: 'Phase', sortKey: 'phase', render: (p) => p.phase || <Dash /> },
          {
            key: 'node',
            label: 'Node',
            sortKey: 'node_name',
            render: (p) => (p.node_name ? <code>{p.node_name}</code> : <Dash />),
          },
          {
            key: 'pod_ip',
            label: 'Pod IP',
            sortKey: 'pod_ip',
            render: (p) => (p.pod_ip ? <code>{p.pod_ip}</code> : <Dash />),
          },
        ]}
      />

      <SectionTitle count={nodes.length}>Nodes running this workload</SectionTitle>
      {nodes.length === 0 ? (
        <Empty message="No pods scheduled yet." />
      ) : (
        <ul className="node-list">
          {nodes.map((n) => (
            <li key={n}>
              <code>{n}</code>
            </li>
          ))}
        </ul>
      )}
    </>
  );
}

export function WorkloadDetail() {
  const { id = '' } = useParams();
  const me = useMe();
  const [nonce, setNonce] = useState(0);
  const reload = () => setNonce((n) => n + 1);
  const [activeTab, setActiveTab] = useState<WorkloadTab>('overview');
  const workloadState = useResource(() => api.getWorkload(id), [id, nonce]);

  return (
    <>
      <div className="breadcrumb">
        <Link to="/workloads">Workloads</Link> /{' '}
        {workloadState.status === 'ready' && workloadState.data.cluster_id && (
          <>
            <ClusterLink
              clusterId={workloadState.data.cluster_id}
              clusterName={workloadState.data.cluster_name}
            />
            {' / '}
          </>
        )}
        {workloadState.status === 'ready' && (
          <>
            <Link to={`/namespaces/${workloadState.data.namespace_id}`}>
              {workloadState.data.namespace_name ?? (
                <span title="namespace row missing">(orphan)</span>
              )}
            </Link>
            {' / '}
          </>
        )}
        <span>this workload</span>
      </div>
      <AsyncView state={workloadState}>
        {(workload) => (
          <>
            <h2>
              <WorkloadIcon size={20} /> {workload.name} <LayerPill layer={workload.layer} />
            </h2>

            <TabBar
              active={activeTab}
              tabs={[
                { id: 'overview', label: 'Overview' },
                { id: 'network-rules', label: 'Network rules' },
              ]}
              onChange={(t) => setActiveTab(t as WorkloadTab)}
            />

            {activeTab === 'network-rules' && (
              <NetworkRulesTab kind="workload" id={workload.id} />
            )}

            {activeTab === 'overview' && (<>
            <dl className="kv-list">
              <KV k="Kind" v={<span className="pill">{workload.kind}</span>} />
              <KV
                k="Replicas"
                v={
                  <>
                    {workload.ready_replicas ?? '?'}
                    <span className="muted">/{workload.replicas ?? '?'}</span>
                  </>
                }
              />
              <KV
                k="Namespace"
                v={
                  <Link to={`/namespaces/${workload.namespace_id}`}>
                    {workload.namespace_name ?? (
                      <span title="namespace row missing">(orphan)</span>
                    )}
                  </Link>
                }
              />
              <KV
                k="Cluster"
                v={<ClusterLink clusterId={workload.cluster_id} clusterName={workload.cluster_name} />}
              />
              <KV k="Labels" v={<Labels labels={workload.labels} />} />
            </dl>

            <ApplicationCard
              linkedId={workload.application_id ?? null}
              linkedName={workload.application_name ?? null}
              onLink={async (appId) => {
                await api.updateWorkload(workload.id, { application_id: appId });
                reload();
              }}
              editable={canEdit(me)}
            />

            <EffectiveDICTCard
              dict={workload.effective_dict}
              linkedAppId={workload.application_id ?? null}
              linkedAppName={workload.application_name ?? null}
            />

            <p className="impact-callout">
              <strong>All assets hosting this application</strong> — the "application = workload" view.
            </p>

            <SectionTitle count={workload.containers?.length || 0}>
              Containers (template)
            </SectionTitle>
            {!workload.containers?.length ? (
              <Empty message="None." />
            ) : (
              <table className="entities">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Image</th>
                    <th>Last version</th>
                    <th>Init</th>
                  </tr>
                </thead>
                <tbody>
                  {workload.containers.map((c) => {
                    const info = workload.containers_versions?.[c.name]
                    return (
                      <tr key={c.name}>
                        <td>
                          <strong>{c.name}</strong>
                        </td>
                        <td>
                          <code>{c.image}</code>
                          <OriginLine image={c.image} info={info} />
                        </td>
                        <td>
                          {info?.latest_tag ? (
                            <code
                              className={info.is_behind ? 'pill status-bad' : 'pill status-ok'}
                              title={
                                info.is_behind
                                  ? `Behind: latest available is ${info.latest_tag} (checked ${new Date(info.last_checked_at ?? '').toLocaleString()})`
                                  : `Up to date (checked ${new Date(info.last_checked_at ?? '').toLocaleString()})`
                              }
                            >
                              {info.latest_tag}
                            </code>
                          ) : (
                            <ContainerVersionBadge info={info ?? undefined} />
                          )}
                        </td>
                        <td>{c.init ? 'yes' : <Dash />}</td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            )}

            <WorkloadPodsSection workloadId={id} />
            </>)}
          </>
        )}
      </AsyncView>
      <ImpactSection entityType="workloads" entityId={id} />
    </>
  );
}
