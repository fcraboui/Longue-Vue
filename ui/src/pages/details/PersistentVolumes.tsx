// PersistentVolume + PersistentVolumeClaim detail pages.

import { Link, useParams } from 'react-router';
import * as api from '../../api';
import { useResource } from '../../hooks';
import { ImpactSection } from '../ImpactGraph';
import { VolumeIcon } from '../../icons';
import {
  AsyncView,
  ClusterLink,
  Dash,
  IdLink,
  KV,
  Labels,
  LayerPill,
  SectionTitle,
  Empty,
} from '../../components';

// --- PersistentVolume detail ----------------------------------------------

export function PersistentVolumeDetail() {
  const { id = '' } = useParams();
  const pv = useResource(() => api.getPersistentVolume(id), [id]);
  const cluster = useResource(
    async () => (pv.status === 'ready' ? api.getCluster(pv.data.cluster_id) : null),
    [pv.status === 'ready' ? pv.data.cluster_id : ''],
  );

  return (
    <>
      <div className="breadcrumb">
        <Link to="/persistentvolumes">Persistent Volumes</Link> /{' '}
        {cluster.status === 'ready' && cluster.data && (
          <>
            <Link to={`/clusters/${cluster.data.id}`}>{cluster.data.name}</Link>
            {' / '}
          </>
        )}
        <span>this volume</span>
      </div>
      <AsyncView state={pv}>
        {(v) => (
          <>
            <h2>
              <VolumeIcon size={20} /> {v.name} <LayerPill layer={v.layer} />
            </h2>
            <dl className="kv-list">
              <KV k="Capacity" v={v.capacity ? <code>{v.capacity}</code> : <Dash />} />
              <KV k="Phase" v={v.phase || <Dash />} />
              <KV k="Reclaim policy" v={v.reclaim_policy || <Dash />} />
              <KV k="Storage class" v={v.storage_class_name || <Dash />} />
              <KV k="CSI driver" v={v.csi_driver ? <code>{v.csi_driver}</code> : <Dash />} />
              <KV k="Volume handle" v={v.volume_handle ? <code>{v.volume_handle}</code> : <Dash />} />
              <KV
                k="Access modes"
                v={v.access_modes?.length ? <code>{v.access_modes.join(', ')}</code> : <Dash />}
              />
              <KV
                k="Cluster"
                v={<IdLink to={`/clusters/${v.cluster_id}`} id={v.cluster_id} />}
              />
              <KV k="Labels" v={<Labels labels={v.labels} />} />
            </dl>

            <SectionTitle count={v.claim_ref_name ? 1 : 0}>Bound claim</SectionTitle>
            {!v.claim_ref_name ? (
              <Empty message="No claim bound to this volume." />
            ) : (
              <dl className="kv-list">
                <KV k="Namespace" v={v.claim_ref_namespace || <Dash />} />
                <KV k="Name" v={<code>{v.claim_ref_name}</code>} />
              </dl>
            )}
          </>
        )}
      </AsyncView>
      <ImpactSection entityType="persistentvolumes" entityId={id} />
    </>
  );
}

// --- PersistentVolumeClaim detail -----------------------------------------

export function PersistentVolumeClaimDetail() {
  const { id = '' } = useParams();
  const pvc = useResource(() => api.getPersistentVolumeClaim(id), [id]);
  const boundPv = useResource(
    async () =>
      pvc.status === 'ready' && pvc.data.bound_volume_id
        ? api.getPersistentVolume(pvc.data.bound_volume_id)
        : null,
    [pvc.status === 'ready' && pvc.data.bound_volume_id ? pvc.data.bound_volume_id : ''],
  );

  return (
    <>
      <div className="breadcrumb">
        <Link to="/persistentvolumeclaims">Persistent Volume Claims</Link> /{' '}
        {pvc.status === 'ready' && pvc.data.cluster_id && (
          <>
            <ClusterLink
              clusterId={pvc.data.cluster_id}
              clusterName={pvc.data.cluster_name}
            />
            {' / '}
          </>
        )}
        {pvc.status === 'ready' && (
          <>
            <Link to={`/namespaces/${pvc.data.namespace_id}`}>
              {pvc.data.namespace_name ?? (
                <span title="namespace row missing">(orphan)</span>
              )}
            </Link>
            {' / '}
          </>
        )}
        <span>this claim</span>
      </div>
      <AsyncView state={pvc}>
        {(c) => (
          <>
            <h2>
              <VolumeIcon size={20} /> {c.name} <LayerPill layer={c.layer} />
            </h2>
            <dl className="kv-list">
              <KV k="Phase" v={c.phase || <Dash />} />
              <KV
                k="Requested storage"
                v={c.requested_storage ? <code>{c.requested_storage}</code> : <Dash />}
              />
              <KV k="Storage class" v={c.storage_class_name || <Dash />} />
              <KV
                k="Access modes"
                v={c.access_modes?.length ? <code>{c.access_modes.join(', ')}</code> : <Dash />}
              />
              <KV
                k="Bound PV"
                v={
                  boundPv.status === 'ready' && boundPv.data ? (
                    <Link to={`/persistentvolumes/${boundPv.data.id}`}>
                      <strong>{boundPv.data.name}</strong>
                    </Link>
                  ) : c.volume_name ? (
                    <code>{c.volume_name}</code>
                  ) : (
                    <Dash />
                  )
                }
              />
              <KV
                k="Namespace"
                v={
                  <Link to={`/namespaces/${c.namespace_id}`}>
                    {c.namespace_name ?? (
                      <span title="namespace row missing">(orphan)</span>
                    )}
                  </Link>
                }
              />
              <KV
                k="Cluster"
                v={<ClusterLink clusterId={c.cluster_id} clusterName={c.cluster_name} />}
              />
              <KV k="Labels" v={<Labels labels={c.labels} />} />
            </dl>
          </>
        )}
      </AsyncView>
      <ImpactSection entityType="persistentvolumeclaims" entityId={id} />
    </>
  );
}
