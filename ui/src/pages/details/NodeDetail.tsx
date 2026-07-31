// Node detail — pods on this node grouped by workload (impact analysis).

import { useState } from 'react';
import { Link, useParams } from 'react-router';
import * as api from '../../api';
import { useClientSort, useResource, usePagedList } from '../../hooks';
import { SortHeader } from '../../components/SortHeader';
import { NodeCuratedCard } from '../node_curated';
import { ImpactSection } from '../ImpactGraph';
import { LabelsCard } from '../../components/inventory/LabelsCard';
import { NodeIcon } from '../../icons';
import {
  AsyncView,
  ClusterLink,
  Dash,
  IdLink,
  KV,
  LayerPill,
  SectionTitle,
  Empty,
  Paginator,
} from '../../components';

// Inline status badge used in detail-page h2s. Same colour scheme as the
// list-page NodeStatusBadge (green Ready, orange cordoned, red NotReady)
// but smaller and positioned next to the layer pill.
function NodeStatusInline({
  ready,
  unschedulable,
}: {
  ready?: boolean | null;
  unschedulable?: boolean | null;
}) {
  if (ready === null || ready === undefined) return null;
  const label = ready
    ? unschedulable
      ? 'Ready · Cordoned'
      : 'Ready'
    : 'NotReady';
  const cls = ready ? (unschedulable ? 'status-warn' : 'status-ok') : 'status-bad';
  return <span className={`pill ${cls}`} style={{ fontSize: '0.8rem' }}>{label}</span>;
}

// --- NodeDetail child section ---------------------------------------------

// Kept hand-rolled rather than converted to ListSection: this section
// shares one pods page across an impact callout + two tables (group-by
// workload, then the raw pod list), which doesn't fit the generic shape.
function NodePodsSection({
  nodeName,
  workloadsById,
}: {
  nodeName: string;
  workloadsById: Map<string, api.Workload>;
}) {
  const pods = usePagedList<api.Pod>(
    (cursor, limit) => api.listPods({ node_name: nodeName, cursor, limit }),
    [nodeName],
  );

  if (pods.loading) {
    return <p className="loading">Loading…</p>;
  }
  if (pods.error) {
    return <p className="error">{pods.error}</p>;
  }

  // Group pods by workload_id to give "if this node dies,
  // workload X loses N pods" an immediate visual.
  // Note: grouping operates on the current page only (by design — paginated).
  const groups = new Map<string, api.Pod[]>();
  const unowned: api.Pod[] = [];
  for (const pod of pods.items) {
    if (!pod.workload_id) {
      unowned.push(pod);
      continue;
    }
    const list = groups.get(pod.workload_id) || [];
    list.push(pod);
    groups.set(pod.workload_id, list);
  }

  return (
    <>
      <p className="impact-callout">
        <strong>Impact analysis</strong> — if this node goes down,{' '}
        {pods.items.length} pod{pods.items.length === 1 ? '' : 's'} across{' '}
        {groups.size} workload{groups.size === 1 ? '' : 's'}
        {unowned.length > 0 && ` (+ ${unowned.length} unmanaged)`} are lost
        {(pods.hasPrev || pods.hasNext) ? ' (this page)' : ''}.
      </p>

      <SectionTitle count={groups.size}>
        Affected workloads
      </SectionTitle>
      <Paginator
        pageSize={pods.pageSize}
        hasPrev={pods.hasPrev}
        hasNext={pods.hasNext}
        onPrev={pods.prev}
        onNext={pods.next}
        onPageSize={pods.setPageSize}
      />
      {groups.size === 0 ? (
        <Empty message="No workload-owned pods on this node." />
      ) : (
        <table className="entities">
          <thead>
            <tr>
              <th>Workload</th>
              <th>Kind</th>
              <th>Pods on this node</th>
              <th>Total replicas</th>
            </tr>
          </thead>
          <tbody>
            {[...groups.entries()].map(([wid, list]) => {
              const wl = workloadsById.get(wid);
              return (
                <tr key={wid}>
                  <td>
                    {wl ? (
                      <Link to={`/workloads/${wl.id}`}>
                        <strong>{wl.name}</strong>
                      </Link>
                    ) : (
                      <IdLink to={`/workloads/${wid}`} id={wid} />
                    )}
                  </td>
                  <td>
                    {wl ? <span className="pill">{wl.kind}</span> : <Dash />}
                  </td>
                  <td>
                    <strong>{list.length}</strong>
                  </td>
                  <td>
                    {wl?.replicas != null ? (
                      <>
                        {list.length}
                        <span className="muted">/{wl.replicas}</span>
                      </>
                    ) : (
                      <Dash />
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}

      <SectionTitle count={pods.items.length}>All pods on this node</SectionTitle>
      {pods.items.length === 0 ? (
        <Empty message="None." />
      ) : (
        <table className="entities">
          <thead>
            <tr>
              <th>Name</th>
              <th>Phase</th>
              <th>Workload</th>
            </tr>
          </thead>
          <tbody>
            {pods.items.map((pod) => {
              const wl = pod.workload_id ? workloadsById.get(pod.workload_id) : undefined;
              return (
                <tr key={pod.id}>
                  <td>
                    <Link to={`/pods/${pod.id}`}>
                      <strong>{pod.name}</strong>
                    </Link>
                  </td>
                  <td>{pod.phase || <Dash />}</td>
                  <td>
                    {wl ? (
                      <Link to={`/workloads/${wl.id}`}>
                        {wl.name}{' '}
                        <span className="muted" style={{ fontSize: 'var(--fs-sm)' }}>
                          {wl.kind}
                        </span>
                      </Link>
                    ) : pod.workload_id ? (
                      <IdLink
                        to={`/workloads/${pod.workload_id}`}
                        id={pod.workload_id}
                      />
                    ) : (
                      <Dash />
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </>
  );
}

export function NodeDetail() {
  const { id = '' } = useParams();
  const [nonce, setNonce] = useState(0);
  const reload = () => setNonce((n) => n + 1);
  // Conditions table: initial key 'type' asc — stable alphabetical default.
  const condSort = useClientSort<api.NodeCondition>('type', {
    type: (c) => c.type,
    status: (c) => c.status,
  });
  // 1. Fetch the node record itself.
  const node = useResource(() => api.getNode(id), [id, nonce]);
  // Also pull all workloads so we can attach name/kind to each pod's
  // workload_id for the impact grouping. Walks every server page so the
  // id→workload map stays complete past the first page (mirrors the
  // fetchAllClusters pattern in Lists.tsx).
  const workloads = useResource(async () => {
    const items: api.Workload[] = [];
    let cursor: string | undefined = undefined;
    for (let i = 0; i < 1000; i++) {
      const page = await api.listWorkloads({ cursor, limit: 500 });
      items.push(...page.items);
      if (!page.next_cursor) break;
      cursor = page.next_cursor;
    }
    return items;
  }, []);

  return (
    <>
      <div className="breadcrumb">
        <Link to="/nodes">Nodes</Link> /{' '}
        {node.status === 'ready' && (
          <>
            <ClusterLink clusterId={node.data.cluster_id} clusterName={node.data.cluster_name} />
            {' / '}
          </>
        )}
        <span>this node</span>
      </div>
      <AsyncView state={node}>
        {(n) => (
          <>
            <h2>
              <NodeIcon size={20} /> {n.display_name || n.name} <LayerPill layer={n.layer} />{' '}
              <NodeStatusInline ready={n.ready} unschedulable={n.unschedulable} />
            </h2>

            <SectionTitle>Identity</SectionTitle>
            <dl className="kv-list">
              <KV k="Name" v={<code>{n.name}</code>} />
              <KV
                k="Cluster"
                v={<ClusterLink clusterId={n.cluster_id} clusterName={n.cluster_name} />}
              />
              <KV k="Role" v={n.role && <span className="pill">{n.role}</span>} />
              <KV k="Provider ID" v={n.provider_id && <code>{n.provider_id}</code>} />
              <KV k="Instance type" v={n.instance_type && <code>{n.instance_type}</code>} />
              <KV k="Zone" v={n.zone && <code>{n.zone}</code>} />
            </dl>

            <NodeCuratedCard node={n} onSaved={reload} />

            <SectionTitle>OS &amp; runtime</SectionTitle>
            <dl className="kv-list">
              <KV k="Kubelet" v={n.kubelet_version && <code>{n.kubelet_version}</code>} />
              <KV k="Kube-proxy" v={n.kube_proxy_version && <code>{n.kube_proxy_version}</code>} />
              <KV k="Container runtime" v={n.container_runtime_version && <code>{n.container_runtime_version}</code>} />
              <KV k="OS image" v={n.os_image} />
              <KV k="Operating system" v={n.operating_system} />
              <KV k="Kernel" v={n.kernel_version && <code>{n.kernel_version}</code>} />
              <KV k="Architecture" v={n.architecture} />
            </dl>

            <SectionTitle>Networking</SectionTitle>
            <dl className="kv-list">
              <KV k="Internal IP" v={n.internal_ip && <code>{n.internal_ip}</code>} />
              <KV k="External IP" v={n.external_ip && <code>{n.external_ip}</code>} />
              <KV k="Pod CIDR" v={n.pod_cidr && <code>{n.pod_cidr}</code>} />
            </dl>

            <SectionTitle>Resources</SectionTitle>
            <table className="entities">
              <thead>
                <tr>
                  <th>Dimension</th>
                  <th>Capacity</th>
                  <th>Allocatable</th>
                </tr>
              </thead>
              <tbody>
                <tr>
                  <td>CPU</td>
                  <td>{n.capacity_cpu ? <code>{n.capacity_cpu}</code> : <Dash />}</td>
                  <td>{n.allocatable_cpu ? <code>{n.allocatable_cpu}</code> : <Dash />}</td>
                </tr>
                <tr>
                  <td>Memory</td>
                  <td>{n.capacity_memory ? <code>{n.capacity_memory}</code> : <Dash />}</td>
                  <td>{n.allocatable_memory ? <code>{n.allocatable_memory}</code> : <Dash />}</td>
                </tr>
                <tr>
                  <td>Pods</td>
                  <td>{n.capacity_pods ? <code>{n.capacity_pods}</code> : <Dash />}</td>
                  <td>{n.allocatable_pods ? <code>{n.allocatable_pods}</code> : <Dash />}</td>
                </tr>
                <tr>
                  <td>Ephemeral storage</td>
                  <td>
                    {n.capacity_ephemeral_storage ? <code>{n.capacity_ephemeral_storage}</code> : <Dash />}
                  </td>
                  <td>
                    {n.allocatable_ephemeral_storage ? (
                      <code>{n.allocatable_ephemeral_storage}</code>
                    ) : (
                      <Dash />
                    )}
                  </td>
                </tr>
              </tbody>
            </table>

            <SectionTitle count={n.conditions?.length || 0}>Conditions</SectionTitle>
            {!n.conditions?.length ? (
              <Empty message="No conditions reported." />
            ) : (
              <table className="entities">
                <thead>
                  <tr>
                    <SortHeader label="Type" sortKey="type" activeKey={condSort.sort} asc={condSort.asc} onToggle={condSort.toggleSort} />
                    <SortHeader label="Status" sortKey="status" activeKey={condSort.sort} asc={condSort.asc} onToggle={condSort.toggleSort} />
                    <th>Reason</th>
                    <th>Message</th>
                  </tr>
                </thead>
                <tbody>
                  {condSort.sortItems(n.conditions).map((c) => {
                    const healthy =
                      (c.type === 'Ready' && c.status === 'True') ||
                      (c.type !== 'Ready' && c.status === 'False');
                    return (
                      <tr key={c.type}>
                        <td>
                          <strong>{c.type}</strong>
                        </td>
                        <td>
                          <span className={`pill ${healthy ? 'status-ok' : 'status-bad'}`}>
                            {c.status}
                          </span>
                        </td>
                        <td>{c.reason || <Dash />}</td>
                        <td>
                          <span className="muted" style={{ fontSize: '0.85rem' }}>
                            {c.message}
                          </span>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            )}

            <SectionTitle count={n.taints?.length || 0}>Taints</SectionTitle>
            {!n.taints?.length ? (
              <Empty message="No taints — every pod can schedule here." />
            ) : (
              <table className="entities">
                <thead>
                  <tr>
                    <th>Key</th>
                    <th>Value</th>
                    <th>Effect</th>
                  </tr>
                </thead>
                <tbody>
                  {n.taints.map((t, i) => (
                    <tr key={i}>
                      <td>
                        <code>{t.key}</code>
                      </td>
                      <td>{t.value ? <code>{t.value}</code> : <Dash />}</td>
                      <td>
                        <span className="pill">{t.effect}</span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}

            <LabelsCard labels={n.labels} />

            <AsyncView state={workloads}>
              {(wls) => {
                const wlById = new Map(wls.map((w) => [w.id, w]));
                return <NodePodsSection nodeName={n.name} workloadsById={wlById} />;
              }}
            </AsyncView>
          </>
        )}
      </AsyncView>
      <ImpactSection entityType="nodes" entityId={id} />
    </>
  );
}
