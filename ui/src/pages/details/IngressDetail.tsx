// Ingress detail.
// The LB block is the on-prem answer: whichever controller / VIP
// provisioner fulfills the Ingress (cloud CC, MetalLB, Kube-VIP, hardware
// LB) writes its address into status.loadBalancer.ingress[]. Auditors can
// read it directly without bouncing into kubectl.

import { Link, useParams } from 'react-router';
import * as api from '../../api';
import { useResource } from '../../hooks';
import { ImpactSection } from '../ImpactGraph';
import { IngressIcon } from '../../icons';
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

export function IngressDetail() {
  const { id = '' } = useParams();
  const ingress = useResource(() => api.getIngress(id), [id]);

  return (
    <>
      <div className="breadcrumb">
        <Link to="/ingresses">Ingresses</Link> /{' '}
        {ingress.status === 'ready' && ingress.data.cluster_id && (
          <>
            <ClusterLink
              clusterId={ingress.data.cluster_id}
              clusterName={ingress.data.cluster_name}
            />
            {' / '}
          </>
        )}
        {ingress.status === 'ready' && (
          <>
            <Link to={`/namespaces/${ingress.data.namespace_id}`}>
              {ingress.data.namespace_name ?? (
                <span title="namespace row missing">(orphan)</span>
              )}
            </Link>
            {' / '}
          </>
        )}
        <span>this ingress</span>
      </div>
      <AsyncView state={ingress}>
        {(i) => (
          <>
            <h2>
              <IngressIcon size={20} /> {i.name} <LayerPill layer={i.layer} />
            </h2>
            <dl className="kv-list">
              <KV k="Ingress class" v={i.ingress_class_name} />
              <KV
                k="Namespace"
                v={
                  <Link to={`/namespaces/${i.namespace_id}`}>
                    {i.namespace_name ?? (
                      <span title="namespace row missing">(orphan)</span>
                    )}
                  </Link>
                }
              />
              <KV
                k="Cluster"
                v={<ClusterLink clusterId={i.cluster_id} clusterName={i.cluster_name} />}
              />
              <KV k="Labels" v={<Labels labels={i.labels} />} />
            </dl>

            <SectionTitle count={i.load_balancer?.length || 0}>Load balancer</SectionTitle>
            {!i.load_balancer?.length ? (
              <Empty message="No address reported yet — Pending for cloud-provisioned, or the on-prem controller (MetalLB / Kube-VIP / hardware LB) hasn't fulfilled this ingress." />
            ) : (
              <>
                <p className="impact-callout">
                  <strong>External entry point</strong>. Whichever fulfills this ingress
                  (cloud controller, MetalLB, Kube-VIP, hardware LB) writes its address
                  to <code>status.loadBalancer.ingress[]</code>.
                </p>
                <table className="entities">
                  <thead>
                    <tr>
                      <th>IP</th>
                      <th>Hostname</th>
                      <th>Ports</th>
                    </tr>
                  </thead>
                  <tbody>
                    {i.load_balancer.map((lb, idx) => (
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
              </>
            )}

            <SectionTitle count={i.rules?.length || 0}>Routing rules</SectionTitle>
            {!i.rules?.length ? (
              <Empty message="No rules defined." />
            ) : (
              <table className="entities">
                <thead>
                  <tr>
                    <th>Host</th>
                    <th>Paths</th>
                  </tr>
                </thead>
                <tbody>
                  {i.rules.map((r, idx) => (
                    <tr key={idx}>
                      <td>
                        {r.host ? <code>{r.host}</code> : <span className="muted">(catch-all)</span>}
                      </td>
                      <td>
                        {r.paths?.length ? (
                          <ul style={{ margin: 0, paddingLeft: '1.1rem' }}>
                            {r.paths.map((p, pi) => (
                              <li key={pi} style={{ fontSize: '0.85rem' }}>
                                <code>{p.path || '/'}</code>{' '}
                                {p.path_type && (
                                  <span className="muted">({p.path_type})</span>
                                )}
                                {p.backend?.service_name && (
                                  <>
                                    {' → '}
                                    <code>
                                      {p.backend.service_name}:
                                      {p.backend.service_port_number ?? p.backend.service_port_name ?? '?'}
                                    </code>
                                  </>
                                )}
                              </li>
                            ))}
                          </ul>
                        ) : (
                          <Dash />
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}

            <SectionTitle count={i.tls?.length || 0}>TLS</SectionTitle>
            {!i.tls?.length ? (
              <Empty message="No TLS configured — ingress serves plaintext." />
            ) : (
              <table className="entities">
                <thead>
                  <tr>
                    <th>Hosts</th>
                    <th>Secret</th>
                  </tr>
                </thead>
                <tbody>
                  {i.tls.map((t, idx) => (
                    <tr key={idx}>
                      <td>
                        {t.hosts?.length ? <code>{t.hosts.join(', ')}</code> : <Dash />}
                      </td>
                      <td>
                        {t.secret_name ? <code>{t.secret_name}</code> : <Dash />}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </>
        )}
      </AsyncView>
      <ImpactSection entityType="ingresses" entityId={id} />
    </>
  );
}
