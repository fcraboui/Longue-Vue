// Pod detail — containers + backlinks to parent workload / namespace.

import { Link, useParams } from 'react-router';
import * as api from '../../api';
import { useResource } from '../../hooks';
import { ImpactSection } from '../ImpactGraph';
import { ContainerVersionBadge } from '../../components/ContainerVersionBadge';
import { OriginLine } from '../../components/OriginLine';
import { PodIcon } from '../../icons';
import {
  AsyncView,
  ClusterLink,
  Dash,
  KV,
  Labels,
  LayerPill,
  SectionTitle,
  Empty,
  WorkloadLink,
} from '../../components';

export function PodDetail() {
  const { id = '' } = useParams();
  const state = useResource(() => api.getPod(id), [id]);
  return (
    <>
      <div className="breadcrumb">
        <Link to="/pods">Pods</Link> /{' '}
        {state.status === 'ready' && state.data.cluster_id && (
          <>
            <ClusterLink
              clusterId={state.data.cluster_id}
              clusterName={state.data.cluster_name}
            />
            {' / '}
          </>
        )}
        {state.status === 'ready' && (
          <>
            <Link to={`/namespaces/${state.data.namespace_id}`}>
              {state.data.namespace_name ?? (
                <span title="namespace row missing">(orphan)</span>
              )}
            </Link>
            {' / '}
          </>
        )}
        <span>this pod</span>
      </div>
      <AsyncView state={state}>
        {(pod) => {
          return (
          <>
            <h2>
              <PodIcon size={20} /> {pod.name} <LayerPill layer={pod.layer} />
            </h2>
            <dl className="kv-list">
              <KV k="Phase" v={pod.phase} />
              <KV k="Node" v={pod.node_name && <code>{pod.node_name}</code>} />
              <KV k="Pod IP" v={pod.pod_ip && <code>{pod.pod_ip}</code>} />
              <KV
                k="Namespace"
                v={
                  <Link to={`/namespaces/${pod.namespace_id}`}>
                    {pod.namespace_name ?? (
                      <span title="namespace row missing">(orphan)</span>
                    )}
                  </Link>
                }
              />
              <KV
                k="Workload"
                v={<WorkloadLink workloadId={pod.workload_id} workloadName={pod.workload_name} />}
              />
              <KV k="Labels" v={<Labels labels={pod.labels} />} />
            </dl>

            <SectionTitle count={pod.containers?.length || 0}>Containers (runtime)</SectionTitle>
            {!pod.containers?.length ? (
              <Empty message="None." />
            ) : (
              <table className="entities">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Image</th>
                    <th>Last version</th>
                    <th>Image ID</th>
                    <th>Init</th>
                  </tr>
                </thead>
                <tbody>
                  {pod.containers.map((c) => {
                    const info = pod.containers_versions?.[c.name]
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
                          {info?.origin_status === 'unresolved' ? (
                            <Dash />
                          ) : info?.latest_tag ? (
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
                        <td>
                          {c.image_id ? (
                            <code style={{ fontSize: '0.75rem' }}>
                              {c.image_id.length > 60 ? c.image_id.slice(0, 60) + '…' : c.image_id}
                            </code>
                          ) : (
                            <Dash />
                          )}
                        </td>
                        <td>{c.init ? 'yes' : <Dash />}</td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            )}
          </>
          );
        }}
      </AsyncView>
      <ImpactSection entityType="pods" entityId={id} />
    </>
  );
}
