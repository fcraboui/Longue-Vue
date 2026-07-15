// One-file home for every top-level list page. All of them use the same
// pattern — usePagedList(listFn) → Paginator → table with a few columns →
// id links through to the detail page. Kept together so adding a new kind
// means editing one file.

import { useMemo } from 'react';
import { Link } from 'react-router-dom';
import * as api from '../api';
import { useResource, usePagedList } from '../hooks';
import { Dash, IdLink, LayerPill, LoadBalancerAddresses, Empty, NamespaceLink, Paginator } from '../components';
import { useEntityTable } from '../components/column_filters';
import { EntityListPage } from '../components/EntityListPage';
import {
  ClusterIcon, NodeIcon, NamespaceIcon, WorkloadIcon, PodIcon,
  ServiceIcon, IngressIcon, VolumeIcon,
} from '../icons';

export function Clusters() {
  return (
    <EntityListPage<api.Cluster>
      title="Clusters"
      icon={<ClusterIcon size={20} />}
      storageKey="lists.clusters"
      emptyMessage="No clusters yet. Connect a collector to start populating your inventory."
      fetchPage={(params, cursor, limit) => api.listClusters({ ...params, cursor, limit })}
      rowKey={(c) => c.id}
      columns={[
        {
          key: 'name',
          label: 'Name',
          sortKey: 'name',
          render: (c) => (
            <>
              <Link to={`/clusters/${c.id}`}>
                <strong>{c.display_name || c.name}</strong>
              </Link>
              {c.display_name && (
                <div className="muted" style={{ fontSize: '0.8rem' }}>
                  {c.name}
                </div>
              )}
            </>
          ),
        },
        { key: 'environment', label: 'Environment', sortKey: 'environment', render: (c) => c.environment || <Dash /> },
        { key: 'provider', label: 'Provider', sortKey: 'provider', render: (c) => c.provider || <Dash /> },
        { key: 'region', label: 'Region', sortKey: 'region', render: (c) => c.region || <Dash /> },
        {
          key: 'k8s_version',
          label: 'K8s version',
          sortKey: 'kubernetes_version',
          render: (c) => (c.kubernetes_version ? <code>{c.kubernetes_version}</code> : <Dash />),
        },
        { key: 'layer', label: 'Layer', render: (c) => <LayerPill layer={c.layer} /> },
      ]}
    />
  );
}

// Lookup map only — not user-facing. Walks every server page so id→name
// resolution stays complete regardless of the UI's selected page size.
async function fetchAllClusters(): Promise<api.Cluster[]> {
  const items: api.Cluster[] = [];
  let cursor: string | undefined = undefined;
  for (let i = 0; i < 1000; i++) {
    const page = await api.listClusters({ cursor, limit: 500 });
    items.push(...page.items);
    if (!page.next_cursor) break;
    cursor = page.next_cursor;
  }
  return items;
}

export function Nodes() {
  const clustersState = useResource(() => fetchAllClusters(), []);
  const clustersById = useMemo(() => {
    if (clustersState.status !== 'ready') return new Map<string, api.Cluster>();
    return new Map(clustersState.data.map((c) => [c.id, c]));
  }, [clustersState]);
  return (
    <EntityListPage<api.Node>
      title="Nodes"
      icon={<NodeIcon size={20} />}
      storageKey="lists.nodes"
      emptyMessage="No nodes found. Ensure a collector is running and connected to a cluster."
      fetchPage={(params, cursor, limit) => api.listNodes({ ...params, cursor, limit })}
      rowKey={(n) => n.id}
      columns={[
        {
          key: 'name',
          label: 'Name',
          sortKey: 'name',
          render: (n) => (
            <Link to={`/nodes/${n.id}`}>
              <strong>{n.display_name || n.name}</strong>
            </Link>
          ),
        },
        {
          key: 'cluster',
          label: 'Cluster',
          render: (n) => {
            const cluster = clustersById.get(n.cluster_id);
            return cluster ? (
              <Link to={`/clusters/${cluster.id}`}>{cluster.name}</Link>
            ) : (
              <IdLink to={`/clusters/${n.cluster_id}`} id={n.cluster_id} />
            );
          },
        },
        { key: 'role', label: 'Role', sortKey: 'role', render: (n) => n.role ? <span className="pill">{n.role}</span> : <Dash /> },
        { key: 'zone', label: 'Zone', sortKey: 'zone', render: (n) => n.zone ? <code>{n.zone}</code> : <Dash /> },
        { key: 'instance_type', label: 'Instance type', sortKey: 'instance_type', render: (n) => n.instance_type ? <code>{n.instance_type}</code> : <Dash /> },
        {
          key: 'cpu_mem',
          label: 'CPU / Mem',
          render: (n) => (
            n.capacity_cpu || n.capacity_memory ? (
              <code>{n.capacity_cpu || '?'} / {n.capacity_memory || '?'}</code>
            ) : (
              <Dash />
            )
          ),
        },
        { key: 'status', label: 'Status', render: (n) => <NodeStatusBadge ready={n.ready} unschedulable={n.unschedulable} /> },
      ]}
    />
  );
}

// Compact at-a-glance status: green Ready, orange cordoned, red NotReady.
function NodeStatusBadge({
  ready,
  unschedulable,
}: {
  ready?: boolean | null;
  unschedulable?: boolean | null;
}) {
  if (ready === null || ready === undefined) return <Dash />;
  const parts = [ready ? 'Ready' : 'NotReady'];
  if (unschedulable) parts.push('Cordoned');
  const cls = ready ? (unschedulable ? 'status-warn' : 'status-ok') : 'status-bad';
  return <span className={`pill ${cls}`}>{parts.join(' · ')}</span>;
}

export function Namespaces() {
  return (
    <EntityListPage<api.Namespace>
      title="Namespaces"
      icon={<NamespaceIcon size={20} />}
      storageKey="lists.namespaces"
      emptyMessage="No namespaces found. They are collected automatically from your clusters."
      fetchPage={(params, cursor, limit) => api.listNamespaces({ ...params, cursor, limit })}
      rowKey={(n) => n.id}
      columns={[
        {
          key: 'name',
          label: 'Name',
          sortKey: 'name',
          render: (n) => (
            <Link to={`/namespaces/${n.id}`}>
              <strong>{n.name}</strong>
            </Link>
          ),
        },
        {
          key: 'cluster',
          label: 'Cluster',
          render: (n) => (
            <Link to={`/clusters/${n.cluster_id}`}>
              {n.cluster_name ?? <span title="cluster row missing">(orphan)</span>}
            </Link>
          ),
        },
        { key: 'phase', label: 'Phase', sortKey: 'phase', render: (n) => n.phase || <Dash /> },
      ]}
    />
  );
}

export function Workloads() {
  return (
    <EntityListPage<api.Workload>
      title="Workloads"
      icon={<WorkloadIcon size={20} />}
      storageKey="lists.workloads"
      emptyMessage="No workloads found. Deployments, StatefulSets and DaemonSets will appear here once collected."
      fetchPage={(params, cursor, limit) => api.listWorkloads({ ...params, cursor, limit })}
      rowKey={(w) => w.id}
      columns={[
        {
          key: 'name',
          label: 'Name',
          sortKey: 'name',
          render: (w) => (
            <Link to={`/workloads/${w.id}`}>
              <strong>{w.name}</strong>
            </Link>
          ),
        },
        { key: 'kind', label: 'Kind', sortKey: 'kind', render: (w) => <span className="pill">{w.kind}</span> },
        {
          key: 'namespace',
          label: 'Namespace',
          render: (w) => (
            <NamespaceLink
              namespaceId={w.namespace_id}
              namespaceName={w.namespace_name}
              clusterId={w.cluster_id}
              clusterName={w.cluster_name}
            />
          ),
        },
        {
          key: 'replicas',
          label: 'Replicas',
          render: (w) => (
            <>
              {w.ready_replicas ?? '?'}
              <span className="muted">/{w.replicas ?? '?'}</span>
            </>
          ),
        },
        {
          key: 'containers',
          label: 'Containers',
          render: (w) => (
            w.containers?.length ? (
              <code>{w.containers.map((c) => c.image).join(', ')}</code>
            ) : (
              <Dash />
            )
          ),
        },
      ]}
    />
  );
}

export function Pods() {
  return (
    <EntityListPage<api.Pod>
      title="Pods"
      icon={<PodIcon size={20} />}
      storageKey="lists.pods"
      emptyMessage="No pods found. Pods are collected from all connected clusters."
      fetchPage={(params, cursor, limit) => api.listPods({ ...params, cursor, limit })}
      rowKey={(p) => p.id}
      columns={[
        {
          key: 'name',
          label: 'Name',
          sortKey: 'name',
          render: (p) => (
            <Link to={`/pods/${p.id}`}>
              <strong>{p.name}</strong>
            </Link>
          ),
        },
        {
          key: 'namespace',
          label: 'Namespace',
          render: (p) => (
            <NamespaceLink
              namespaceId={p.namespace_id}
              namespaceName={p.namespace_name}
              clusterId={p.cluster_id}
              clusterName={p.cluster_name}
            />
          ),
        },
        { key: 'phase', label: 'Phase', sortKey: 'phase', render: (p) => p.phase || <Dash /> },
        { key: 'node', label: 'Node', sortKey: 'node_name', render: (p) => p.node_name ? <code>{p.node_name}</code> : <Dash /> },
        { key: 'pod_ip', label: 'Pod IP', sortKey: 'pod_ip', render: (p) => p.pod_ip ? <code>{p.pod_ip}</code> : <Dash /> },
        {
          key: 'workload',
          label: 'Workload',
          render: (p) => (
            p.workload_id ? (
              <Link to={`/workloads/${p.workload_id}`}>
                {p.workload_name ?? <IdLink to={`/workloads/${p.workload_id}`} id={p.workload_id} />}
              </Link>
            ) : (
              <Dash />
            )
          ),
        },
      ]}
    />
  );
}

export function Services() {
  const list = usePagedList<api.Service>(
    (cursor, limit) => api.listServices({ cursor, limit }),
    [],
  );
  const tableRef = useEntityTable('lists.services');
  return (
    <>
      <h2><ServiceIcon size={20} /> Services</h2>
      <Paginator
        pageSize={list.pageSize}
        hasPrev={list.hasPrev}
        hasNext={list.hasNext}
        onPrev={list.prev}
        onNext={list.next}
        onPageSize={list.setPageSize}
      />
      {list.loading ? (
        <p className="loading">Loading…</p>
      ) : list.error ? (
        <div className="error">Failed to load: {list.error}</div>
      ) : list.items.length === 0 ? (
        <Empty message="No services found. Kubernetes Services are collected automatically." />
      ) : (
        <div className="table-wrap">
          <table className="entities" ref={tableRef}>
            <thead>
              <tr>
                <th>Name</th>
                <th>Namespace</th>
                <th>Type</th>
                <th>ClusterIP</th>
                <th>Ports</th>
                <th>Load balancer</th>
              </tr>
            </thead>
            <tbody>
              {list.items.map((s) => (
                <tr key={s.id}>
                  <td>
                    <Link to={`/services/${s.id}`}>
                      <strong>{s.name}</strong>
                    </Link>
                  </td>
                  <td>
                    <NamespaceLink
                      namespaceId={s.namespace_id}
                      namespaceName={s.namespace_name}
                      clusterId={s.cluster_id}
                      clusterName={s.cluster_name}
                    />
                  </td>
                  <td><span className="pill">{s.type || 'ClusterIP'}</span></td>
                  <td>{s.cluster_ip ? <code>{s.cluster_ip}</code> : <Dash />}</td>
                  <td>
                    {s.ports?.length ? (
                      <code>{s.ports.map((p) => `${p.port}/${p.protocol || 'TCP'}`).join(', ')}</code>
                    ) : (
                      <Dash />
                    )}
                  </td>
                  <td><LoadBalancerAddresses entries={s.load_balancer} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

export function Ingresses() {
  const list = usePagedList<api.Ingress>(
    (cursor, limit) => api.listIngresses({ cursor, limit }),
    [],
  );
  const tableRef = useEntityTable('lists.ingresses');
  return (
    <>
      <h2><IngressIcon size={20} /> Ingresses</h2>
      <Paginator
        pageSize={list.pageSize}
        hasPrev={list.hasPrev}
        hasNext={list.hasNext}
        onPrev={list.prev}
        onNext={list.next}
        onPageSize={list.setPageSize}
      />
      {list.loading ? (
        <p className="loading">Loading…</p>
      ) : list.error ? (
        <div className="error">Failed to load: {list.error}</div>
      ) : list.items.length === 0 ? (
        <Empty message="No ingresses found. Ingress resources are collected from all namespaces." />
      ) : (
        <div className="table-wrap">
          <table className="entities" ref={tableRef}>
            <thead>
              <tr>
                <th>Name</th>
                <th>Namespace</th>
                <th>Class</th>
                <th>Hosts</th>
                <th>Load balancer</th>
              </tr>
            </thead>
            <tbody>
              {list.items.map((i) => (
                <tr key={i.id}>
                  <td>
                    <Link to={`/ingresses/${i.id}`}>
                      <strong>{i.name}</strong>
                    </Link>
                  </td>
                  <td>
                    <NamespaceLink
                      namespaceId={i.namespace_id}
                      namespaceName={i.namespace_name}
                      clusterId={i.cluster_id}
                      clusterName={i.cluster_name}
                    />
                  </td>
                  <td>{i.ingress_class_name || <Dash />}</td>
                  <td>
                    {i.rules?.length ? (
                      <code>{i.rules.map((r) => r.host).filter(Boolean).join(', ')}</code>
                    ) : (
                      <Dash />
                    )}
                  </td>
                  <td><LoadBalancerAddresses entries={i.load_balancer} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

export function PersistentVolumes() {
  const list = usePagedList<api.PersistentVolume>(
    (cursor, limit) => api.listPersistentVolumes({ cursor, limit }),
    [],
  );
  const clusters = useResource(() => fetchAllClusters(), []);
  const clustersById =
    clusters.status === 'ready'
      ? new Map(clusters.data.map((c) => [c.id, c]))
      : new Map<string, api.Cluster>();
  const tableRef = useEntityTable('lists.persistent_volumes');
  return (
    <>
      <h2><VolumeIcon size={20} /> Persistent Volumes</h2>
      <Paginator
        pageSize={list.pageSize}
        hasPrev={list.hasPrev}
        hasNext={list.hasNext}
        onPrev={list.prev}
        onNext={list.next}
        onPageSize={list.setPageSize}
      />
      {list.loading ? (
        <p className="loading">Loading…</p>
      ) : list.error ? (
        <div className="error">Failed to load: {list.error}</div>
      ) : list.items.length === 0 ? (
        <Empty message="No persistent volumes found. PVs are collected cluster-wide by the collector." />
      ) : (
        <div className="table-wrap">
          <table className="entities" ref={tableRef}>
            <thead>
              <tr>
                <th>Name</th>
                <th>Cluster</th>
                <th>Capacity</th>
                <th>Storage class</th>
                <th>CSI driver</th>
                <th>Phase</th>
              </tr>
            </thead>
            <tbody>
              {list.items.map((pv) => {
                const cluster = clustersById.get(pv.cluster_id);
                return (
                  <tr key={pv.id}>
                    <td>
                      <Link to={`/persistentvolumes/${pv.id}`}>
                        <strong>{pv.name}</strong>
                      </Link>
                    </td>
                    <td>
                      {cluster ? (
                        <Link to={`/clusters/${cluster.id}`}>{cluster.name}</Link>
                      ) : (
                        <IdLink to={`/clusters/${pv.cluster_id}`} id={pv.cluster_id} />
                      )}
                    </td>
                    <td>{pv.capacity ? <code>{pv.capacity}</code> : <Dash />}</td>
                    <td>{pv.storage_class_name || <Dash />}</td>
                    <td>{pv.csi_driver ? <code>{pv.csi_driver}</code> : <Dash />}</td>
                    <td>{pv.phase || <Dash />}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

export function PersistentVolumeClaims() {
  const list = usePagedList<api.PersistentVolumeClaim>(
    (cursor, limit) => api.listPersistentVolumeClaims({ cursor, limit }),
    [],
  );
  const tableRef = useEntityTable('lists.persistent_volume_claims');
  return (
    <>
      <h2><VolumeIcon size={20} /> Persistent Volume Claims</h2>
      <Paginator
        pageSize={list.pageSize}
        hasPrev={list.hasPrev}
        hasNext={list.hasNext}
        onPrev={list.prev}
        onNext={list.next}
        onPageSize={list.setPageSize}
      />
      {list.loading ? (
        <p className="loading">Loading…</p>
      ) : list.error ? (
        <div className="error">Failed to load: {list.error}</div>
      ) : list.items.length === 0 ? (
        <Empty message="No persistent volume claims found. PVCs are collected from all namespaces." />
      ) : (
        <div className="table-wrap">
          <table className="entities" ref={tableRef}>
            <thead>
              <tr>
                <th>Name</th>
                <th>Namespace</th>
                <th>Phase</th>
                <th>Requested</th>
                <th>Storage class</th>
                <th>Bound PV</th>
              </tr>
            </thead>
            <tbody>
              {list.items.map((pvc) => (
                <tr key={pvc.id}>
                  <td>
                    <Link to={`/persistentvolumeclaims/${pvc.id}`}>
                      <strong>{pvc.name}</strong>
                    </Link>
                  </td>
                  <td>
                    <NamespaceLink
                      namespaceId={pvc.namespace_id}
                      namespaceName={pvc.namespace_name}
                      clusterId={pvc.cluster_id}
                      clusterName={pvc.cluster_name}
                    />
                  </td>
                  <td>{pvc.phase || <Dash />}</td>
                  <td>{pvc.requested_storage ? <code>{pvc.requested_storage}</code> : <Dash />}</td>
                  <td>{pvc.storage_class_name || <Dash />}</td>
                  <td>{pvc.volume_name ? <code>{pvc.volume_name}</code> : <Dash />}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}
