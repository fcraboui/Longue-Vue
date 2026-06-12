// Cluster detail — namespaces + nodes + PVs in the cluster, plus the
// impact and history tabs.

import { useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import * as api from '../../api';
import { useResource, usePagedList } from '../../hooks';
import { useMe, isAdmin } from '../../me';
import { ClusterCuratedCard } from '../cluster_curated';
import { ImpactSection } from '../ImpactGraph';
import { ClusterHistory } from '../ClusterHistory';
import { ClusterIcon } from '../../icons';
import { AsyncView, Dash, KV, Labels, LayerPill } from '../../components';
import { ListSection } from '../../components/ListSection';
import { TabBar } from './shared';

type ClusterTab = 'overview' | 'impact' | 'history';

// --- ClusterDetail child sections -----------------------------------------

function ClusterNamespacesSection({ clusterId }: { clusterId: string }) {
  const list = usePagedList<api.Namespace>(
    (cursor, limit) => api.listNamespaces({ cluster_id: clusterId, cursor, limit }),
    [clusterId],
  );
  return (
    <ListSection
      title="Namespaces"
      list={list}
      emptyMessage="No namespaces ingested yet."
      rowKey={(n) => n.id}
      columns={[
        { key: 'name', label: 'Name', link: (n) => `/namespaces/${n.id}`, render: (n) => n.name },
        { key: 'phase', label: 'Phase', render: (n) => n.phase || <Dash /> },
      ]}
    />
  );
}

function ClusterNodesSection({ clusterId }: { clusterId: string }) {
  const list = usePagedList<api.Node>(
    (cursor, limit) => api.listNodes({ cluster_id: clusterId, cursor, limit }),
    [clusterId],
  );
  return (
    <ListSection
      title="Nodes"
      list={list}
      emptyMessage="No nodes ingested yet."
      rowKey={(n) => n.id}
      columns={[
        {
          key: 'name',
          label: 'Name',
          link: (n) => `/nodes/${n.id}`,
          render: (n) => n.display_name || n.name,
        },
        {
          key: 'kubelet',
          label: 'Kubelet',
          render: (n) => (n.kubelet_version ? <code>{n.kubelet_version}</code> : <Dash />),
        },
        { key: 'arch', label: 'Arch', render: (n) => n.architecture || <Dash /> },
      ]}
    />
  );
}

function ClusterPVsSection({ clusterId }: { clusterId: string }) {
  const list = usePagedList<api.PersistentVolume>(
    (cursor, limit) => api.listPersistentVolumes({ cluster_id: clusterId, cursor, limit }),
    [clusterId],
  );
  return (
    <ListSection
      title="Persistent Volumes"
      list={list}
      emptyMessage="No PVs in this cluster."
      rowKey={(pv) => pv.id}
      columns={[
        {
          key: 'name',
          label: 'Name',
          link: (pv) => `/persistentvolumes/${pv.id}`,
          render: (pv) => pv.name,
        },
        {
          key: 'capacity',
          label: 'Capacity',
          render: (pv) => (pv.capacity ? <code>{pv.capacity}</code> : <Dash />),
        },
        {
          key: 'storage_class',
          label: 'Storage class',
          render: (pv) => pv.storage_class_name || <Dash />,
        },
        { key: 'phase', label: 'Phase', render: (pv) => pv.phase || <Dash /> },
      ]}
    />
  );
}

export function ClusterDetail() {
  const { id = '' } = useParams();
  const navigate = useNavigate();
  const me = useMe();
  const [nonce, setNonce] = useState(0);
  const [deleting, setDeleting] = useState(false);
  const [activeTab, setActiveTab] = useState<ClusterTab>('overview');
  const reload = () => setNonce((n) => n + 1);
  const state = useResource(() => api.getCluster(id), [id, nonce]);

  const handleDelete = async (cluster: api.Cluster) => {
    const typed = prompt(
      `This will permanently delete cluster "${cluster.name}" and all its child resources.\n\nType the cluster name to confirm:`,
    );
    if (typed === null) return; // cancelled
    if (typed !== cluster.name) {
      alert(`Name does not match. Expected "${cluster.name}".`);
      return;
    }
    setDeleting(true);
    try {
      await api.deleteCluster(cluster.id);
      navigate('/clusters', { replace: true });
    } catch (err) {
      alert(err instanceof api.ApiError ? err.message : String(err));
      setDeleting(false);
    }
  };

  return (
    <>
      <div className="breadcrumb">
        <Link to="/clusters">Clusters</Link> / <span>this cluster</span>
      </div>
      <AsyncView state={state}>
        {(cluster) => (
          <>
            <h2>
              <ClusterIcon size={20} /> {cluster.display_name || cluster.name} <LayerPill layer={cluster.layer} />
              {isAdmin(me) && (
                <button
                  className="danger"
                  style={{ marginLeft: '1rem', fontSize: '0.85rem' }}
                  disabled={deleting}
                  onClick={() => handleDelete(cluster)}
                >
                  {deleting ? 'Deleting…' : 'Delete cluster'}
                </button>
              )}
            </h2>

            <TabBar
              active={activeTab}
              tabs={[
                { id: 'overview', label: 'Overview' },
                { id: 'impact',   label: 'Impact' },
                { id: 'history',  label: 'History' },
              ]}
              onChange={(t) => setActiveTab(t as ClusterTab)}
            />

            {activeTab === 'overview' && (
              <>
                <dl className="kv-list">
                  <KV k="Name" v={<code>{cluster.name}</code>} />
                  <KV k="Environment" v={cluster.environment} />
                  <KV k="Provider" v={cluster.provider} />
                  <KV k="Region" v={cluster.region} />
                  <KV k="K8s version" v={cluster.kubernetes_version && <code>{cluster.kubernetes_version}</code>} />
                  <KV k="API endpoint" v={cluster.api_endpoint && <code>{cluster.api_endpoint}</code>} />
                  <KV k="Labels" v={<Labels labels={cluster.labels} />} />
                </dl>

                <ClusterCuratedCard cluster={cluster} onSaved={reload} />

                <ClusterNamespacesSection clusterId={id} />
                <ClusterNodesSection clusterId={id} />
                <ClusterPVsSection clusterId={id} />
              </>
            )}

            {activeTab === 'impact' && <ImpactSection entityType="clusters" entityId={id} />}

            {activeTab === 'history' && <ClusterHistory clusterId={id} />}
          </>
        )}
      </AsyncView>
    </>
  );
}
