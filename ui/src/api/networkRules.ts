// Typed client for the flow-matrix network-rules endpoints.
// Uses the same fetch pattern as api.ts (credentials: 'same-origin',
// throws on non-ok, parses JSON).

export interface NetworkPolicyRule {
  id: string;
  direction: "ingress" | "egress";
  peer_kind: "selector" | "ip_block";
  peer_pod_selector: Record<string, unknown> | null;
  peer_namespace_selector: Record<string, unknown> | null;
  peer_ip_block_cidr: string | null;
  peer_ip_block_except: string[] | null;
  ports: Array<{ protocol?: string; port?: number | string; endPort?: number }>;
}

export interface MatchingPolicy {
  id: string;
  name: string;
  policy_types: string[];
  rules: NetworkPolicyRule[];
}

export interface WorkloadNetworkRulesResponse {
  workload_id: string;
  policy_count: number;
  k8s_default_allow: boolean;
  matching_policies: MatchingPolicy[];
}

export interface SecurityGroupRule {
  id: string;
  direction: "ingress" | "egress";
  protocol: "tcp" | "udp" | "icmp" | "any";
  from_port: number | null;
  to_port: number | null;
  peer_kind: "cidr" | "sg_ref" | "prefix_list_ref" | "self";
  peer_cidr: string | null;
  peer_sg_provider_id: string | null;
  peer_prefix_id: string | null;
  description: string;
}

export interface AttachedSecurityGroup {
  id: string;
  name: string;
  vpc_id: string;
  rules: SecurityGroupRule[];
}

export interface VMNetworkRulesResponse {
  vm_id: string;
  stale: boolean;
  no_security_groups: boolean;
  attached_security_groups: AttachedSecurityGroup[];
}

async function apiFetch(path: string): Promise<Response> {
  return fetch(path, { credentials: "same-origin" });
}

export async function getWorkloadNetworkRules(id: string): Promise<WorkloadNetworkRulesResponse> {
  const r = await apiFetch(`/v1/workloads/${encodeURIComponent(id)}/network-rules`);
  if (!r.ok) throw new Error(`getWorkloadNetworkRules: ${r.status}`);
  return r.json();
}

export async function getVMNetworkRules(id: string): Promise<VMNetworkRulesResponse> {
  const r = await apiFetch(`/v1/virtual-machines/${encodeURIComponent(id)}/network-rules`);
  if (!r.ok) throw new Error(`getVMNetworkRules: ${r.status}`);
  return r.json();
}
