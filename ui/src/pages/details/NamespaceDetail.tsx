// Namespace detail — workloads + pods + services + ingresses + PVCs in the
// NS (serves the "application = namespace" view).

import { useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import * as api from '../../api';
import { useResource, usePagedList, useLocalListControls } from '../../hooks';
import { NamespaceCuratedCard } from '../namespace_curated';
import { ImpactSection } from '../ImpactGraph';
import { NamespaceIcon } from '../../icons';
import {
  AsyncView,
  ClusterLink,
  Dash,
  IdLink,
  KV,
  Labels,
  LayerPill,
} from '../../components';
import { ListSection } from '../../components/ListSection';

// --- NamespaceDetail child sections ---------------------------------------

function NamespaceWorkloadsSection({ namespaceId }: { namespaceId: string }) {
  const controls = useLocalListControls();
  const list = usePagedList<api.Workload>(
    (cursor, limit) => api.listWorkloads({ namespace_id: namespaceId, ...controls.params, cursor, limit }),
    [namespaceId, ...controls.deps],
  );
  return (
    <ListSection
      title="Workloads"
      list={list}
      controls={controls}
      emptyMessage="None."
      rowKey={(w) => w.id}
      columns={[
        { key: 'name', label: 'Name', sortKey: 'name', link: (w) => `/workloads/${w.id}`, render: (w) => w.name },
        { key: 'kind', label: 'Kind', sortKey: 'kind', render: (w) => <span className="pill">{w.kind}</span> },
        {
          key: 'ready',
          label: 'Ready',
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
          render: (w) =>
            w.containers?.length ? (
              <code>{w.containers.map((c) => c.image).join(', ')}</code>
            ) : (
              <Dash />
            ),
        },
      ]}
    />
  );
}

function NamespacePodsSection({ namespaceId }: { namespaceId: string }) {
  const controls = useLocalListControls();
  const list = usePagedList<api.Pod>(
    (cursor, limit) => api.listPods({ namespace_id: namespaceId, ...controls.params, cursor, limit }),
    [namespaceId, ...controls.deps],
  );
  return (
    <ListSection
      title="Pods"
      list={list}
      controls={controls}
      emptyMessage="None."
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
        {
          key: 'workload',
          label: 'Workload',
          render: (p) =>
            p.workload_name ? (
              <Link to={`/workloads/${p.workload_id}`}>
                <strong>{p.workload_name}</strong>
              </Link>
            ) : p.workload_id ? (
              <IdLink to={`/workloads/${p.workload_id}`} id={p.workload_id} />
            ) : (
              <Dash />
            ),
        },
      ]}
    />
  );
}

function NamespaceServicesSection({ namespaceId }: { namespaceId: string }) {
  const controls = useLocalListControls();
  const list = usePagedList<api.Service>(
    (cursor, limit) => api.listServices({ namespace_id: namespaceId, ...controls.params, cursor, limit }),
    [namespaceId, ...controls.deps],
  );
  return (
    <ListSection
      title="Services"
      list={list}
      controls={controls}
      emptyMessage="None."
      rowKey={(s) => s.id}
      columns={[
        { key: 'name', label: 'Name', sortKey: 'name', link: (s) => `/services/${s.id}`, render: (s) => s.name },
        {
          key: 'type',
          label: 'Type',
          sortKey: 'type',
          render: (s) => <span className="pill">{s.type || 'ClusterIP'}</span>,
        },
        {
          key: 'cluster_ip',
          label: 'ClusterIP',
          sortKey: 'cluster_ip',
          render: (s) => (s.cluster_ip ? <code>{s.cluster_ip}</code> : <Dash />),
        },
      ]}
    />
  );
}

function NamespaceIngressesSection({ namespaceId }: { namespaceId: string }) {
  const controls = useLocalListControls();
  const list = usePagedList<api.Ingress>(
    (cursor, limit) => api.listIngresses({ namespace_id: namespaceId, ...controls.params, cursor, limit }),
    [namespaceId, ...controls.deps],
  );
  return (
    <ListSection
      title="Ingresses"
      list={list}
      controls={controls}
      emptyMessage="None."
      rowKey={(i) => i.id}
      columns={[
        { key: 'name', label: 'Name', sortKey: 'name', link: (i) => `/ingresses/${i.id}`, render: (i) => i.name },
        { key: 'class', label: 'Class', sortKey: 'ingress_class_name', render: (i) => i.ingress_class_name || <Dash /> },
        {
          key: 'hosts',
          label: 'Hosts',
          render: (i) =>
            i.rules?.length ? (
              <code>
                {i.rules
                  .map((r) => r.host)
                  .filter(Boolean)
                  .join(', ')}
              </code>
            ) : (
              <Dash />
            ),
        },
      ]}
    />
  );
}

function NamespacePVCsSection({ namespaceId }: { namespaceId: string }) {
  const controls = useLocalListControls();
  const list = usePagedList<api.PersistentVolumeClaim>(
    (cursor, limit) => api.listPersistentVolumeClaims({ namespace_id: namespaceId, ...controls.params, cursor, limit }),
    [namespaceId, ...controls.deps],
  );
  return (
    <ListSection
      title="Persistent Volume Claims"
      list={list}
      controls={controls}
      emptyMessage="None."
      rowKey={(pvc) => pvc.id}
      columns={[
        {
          key: 'name',
          label: 'Name',
          sortKey: 'name',
          link: (pvc) => `/persistentvolumeclaims/${pvc.id}`,
          render: (pvc) => pvc.name,
        },
        { key: 'phase', label: 'Phase', sortKey: 'phase', render: (pvc) => pvc.phase || <Dash /> },
        {
          key: 'requested',
          label: 'Requested',
          sortKey: 'requested_storage',
          render: (pvc) => (pvc.requested_storage ? <code>{pvc.requested_storage}</code> : <Dash />),
        },
        {
          key: 'bound_pv',
          label: 'Bound PV',
          render: (pvc) => (pvc.volume_name ? <code>{pvc.volume_name}</code> : <Dash />),
        },
      ]}
    />
  );
}

export function NamespaceDetail() {
  const { id = '' } = useParams();
  const [nonce, setNonce] = useState(0);
  const reload = () => setNonce((n) => n + 1);
  const state = useResource(() => api.getNamespace(id), [id, nonce]);

  return (
    <>
      <div className="breadcrumb">
        <Link to="/namespaces">Namespaces</Link> /{' '}
        {state.status === 'ready' && state.data.cluster_id && (
          <>
            <ClusterLink
              clusterId={state.data.cluster_id}
              clusterName={state.data.cluster_name}
            />
            {' / '}
          </>
        )}
        <span>this namespace</span>
      </div>
      <AsyncView state={state}>
        {(ns) => (
          <>
            <h2>
              <NamespaceIcon size={20} /> {ns.name} <LayerPill layer={ns.layer} />
            </h2>
            <dl className="kv-list">
              <KV
                k="Cluster"
                v={<ClusterLink clusterId={ns.cluster_id} clusterName={ns.cluster_name} />}
              />
              <KV k="Phase" v={ns.phase} />
              <KV k="Labels" v={<Labels labels={ns.labels} />} />
            </dl>

            <NamespaceCuratedCard namespace={ns} onSaved={reload} />

            <p className="impact-callout">
              <strong>All assets in this namespace</strong> — the "application = namespace" view.
            </p>

            <NamespaceWorkloadsSection namespaceId={id} />
            <NamespacePodsSection namespaceId={id} />
            <NamespaceServicesSection namespaceId={id} />
            <NamespaceIngressesSection namespaceId={id} />
            <NamespacePVCsSection namespaceId={id} />
          </>
        )}
      </AsyncView>
      <ImpactSection entityType="namespaces" entityId={id} />
    </>
  );
}
