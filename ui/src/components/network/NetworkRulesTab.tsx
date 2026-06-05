import { useEffect, useState } from "react";
import {
  getWorkloadNetworkRules, getVMNetworkRules,
  WorkloadNetworkRulesResponse, VMNetworkRulesResponse,
} from "../../api/networkRules";
import { RuleRow } from "./RuleRow";

type Props =
  | { kind: "workload"; id: string }
  | { kind: "vm";       id: string };

export function NetworkRulesTab(props: Props) {
  const [data, setData] = useState<WorkloadNetworkRulesResponse | VMNetworkRulesResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    const load = async () => {
      try {
        const d = props.kind === "workload"
          ? await getWorkloadNetworkRules(props.id)
          : await getVMNetworkRules(props.id);
        setData(d);
      } catch (e: unknown) {
        setErr(e instanceof Error ? e.message : String(e));
      }
    };
    void load();
  }, [props.kind, props.id]);

  if (err) return <div className="network-rules-error">Failed to load: {err}</div>;
  if (!data) return <div className="network-rules-loading">Loading…</div>;

  if (props.kind === "workload") {
    const d = data as WorkloadNetworkRulesResponse;
    if (d.k8s_default_allow) {
      return (
        <div className="network-rules">
          <div className="banner banner-warning">
            <strong>No NetworkPolicy selects this workload — K8s default-allow applies.</strong>
            <div>This will surface as a <code>wide_open_no_netpol</code> finding once the flow matrix ships (Phase 2).</div>
          </div>
        </div>
      );
    }
    return (
      <div className="network-rules">
        {d.matching_policies.map((p) => (
          <section key={p.id} className="netpol-block">
            <h4>{p.name} <small>({p.policy_types.join(", ")})</small></h4>
            {p.rules.map((r) => {
              const peer = r.peer_kind === "ip_block"
                ? r.peer_ip_block_cidr ?? "?"
                : JSON.stringify(r.peer_pod_selector ?? {});
              return (
                <RuleRow key={r.id} direction={r.direction}
                         ports={r.ports} peer={peer} />
              );
            })}
          </section>
        ))}
      </div>
    );
  }

  const d = data as VMNetworkRulesResponse;
  if (d.stale) {
    return <div className="banner banner-info">
      <strong>Stale data:</strong> this VM record predates the canonical SG schema; awaiting the next collector tick.
    </div>;
  }
  if (d.no_security_groups) {
    return <div className="banner banner-warning">
      <strong>No security groups on this VM.</strong> Provider defaults apply.
    </div>;
  }
  return (
    <div className="network-rules">
      {d.attached_security_groups.map((sg) => (
        <section key={sg.id} className="sg-block">
          <h4>{sg.name} {sg.vpc_id ? <small>(VPC {sg.vpc_id})</small> : null}</h4>
          {sg.rules.map((r) => {
            const peer = r.peer_kind === "cidr"
              ? r.peer_cidr ?? "?"
              : r.peer_kind === "sg_ref"
                ? `SG ${r.peer_sg_provider_id}`
                : r.peer_kind === "prefix_list_ref"
                  ? `prefix-list ${r.peer_prefix_id}`
                  : "self";
            return (
              <RuleRow key={r.id}
                       direction={r.direction}
                       protocol={r.protocol}
                       fromPort={r.from_port}
                       toPort={r.to_port}
                       peer={peer}
                       description={r.description} />
            );
          })}
        </section>
      ))}
    </div>
  );
}
