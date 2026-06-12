// Service detail.

import { Link, useParams } from 'react-router-dom';
import * as api from '../../api';
import { useResource } from '../../hooks';
import { ImpactSection } from '../ImpactGraph';
import { ServiceIcon } from '../../icons';
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

export function ServiceDetail() {
  const { id = '' } = useParams();
  const service = useResource(() => api.getService(id), [id]);

  return (
    <>
      <div className="breadcrumb">
        <Link to="/services">Services</Link> /{' '}
        {service.status === 'ready' && service.data.cluster_id && (
          <>
            <ClusterLink
              clusterId={service.data.cluster_id}
              clusterName={service.data.cluster_name}
            />
            {' / '}
          </>
        )}
        {service.status === 'ready' && (
          <>
            <Link to={`/namespaces/${service.data.namespace_id}`}>
              {service.data.namespace_name ?? (
                <span title="namespace row missing">(orphan)</span>
              )}
            </Link>
            {' / '}
          </>
        )}
        <span>this service</span>
      </div>
      <AsyncView state={service}>
        {(s) => (
          <>
            <h2>
              <ServiceIcon size={20} /> {s.name} <LayerPill layer={s.layer} />
            </h2>
            <dl className="kv-list">
              <KV k="Type" v={<span className="pill">{s.type || 'ClusterIP'}</span>} />
              <KV k="ClusterIP" v={s.cluster_ip ? <code>{s.cluster_ip}</code> : <Dash />} />
              <KV
                k="Namespace"
                v={
                  <Link to={`/namespaces/${s.namespace_id}`}>
                    {s.namespace_name ?? (
                      <span title="namespace row missing">(orphan)</span>
                    )}
                  </Link>
                }
              />
              <KV
                k="Cluster"
                v={<ClusterLink clusterId={s.cluster_id} clusterName={s.cluster_name} />}
              />
              <KV k="Labels" v={<Labels labels={s.labels} />} />
            </dl>

            <SectionTitle count={s.ports?.length || 0}>Ports</SectionTitle>
            {!s.ports?.length ? (
              <Empty message="No ports defined." />
            ) : (
              <table className="entities">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Port</th>
                    <th>Target</th>
                    <th>Protocol</th>
                    <th>NodePort</th>
                  </tr>
                </thead>
                <tbody>
                  {s.ports.map((p, idx) => (
                    <tr key={idx}>
                      <td>{p.name || <Dash />}</td>
                      <td><code>{p.port}</code></td>
                      <td>{p.target_port ? <code>{p.target_port}</code> : <Dash />}</td>
                      <td>{p.protocol || 'TCP'}</td>
                      <td>{p.node_port ? <code>{p.node_port}</code> : <Dash />}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}

            <SectionTitle count={s.load_balancer?.length || 0}>Load balancer</SectionTitle>
            {!s.load_balancer?.length ? (
              <Empty message="No external address — only Services of type LoadBalancer typically carry entries." />
            ) : (
              <table className="entities">
                <thead>
                  <tr>
                    <th>IP</th>
                    <th>Hostname</th>
                    <th>Ports</th>
                  </tr>
                </thead>
                <tbody>
                  {s.load_balancer.map((lb, idx) => (
                    <tr key={idx}>
                      <td>{lb.ip ? <code>{lb.ip}</code> : <Dash />}</td>
                      <td>
                        {lb.hostname ? <span className="lb-host">{lb.hostname}</span> : <Dash />}
                      </td>
                      <td>
                        {lb.ports?.length ? (
                          <code>
                            {lb.ports
                              .map((p) => `${p.port}/${p.protocol || 'TCP'}`)
                              .join(', ')}
                          </code>
                        ) : (
                          <Dash />
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}

            <SectionTitle count={s.selector ? Object.keys(s.selector).length : 0}>Selector</SectionTitle>
            {!s.selector || Object.keys(s.selector).length === 0 ? (
              <Empty message="No selector — Service is headless / ExternalName, or backed by manually-managed Endpoints." />
            ) : (
              <Labels labels={s.selector} />
            )}
          </>
        )}
      </AsyncView>
      <ImpactSection entityType="services" entityId={id} />
    </>
  );
}
