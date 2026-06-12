// Detail pages for the drill-down chain:
//   Cluster    → namespaces + nodes + PVs in the cluster
//   Namespace  → workloads + pods + services + ingresses + PVCs in the NS
//                (serves "application = namespace" view)
//   Workload   → its pods (via workload_id) + nodes they run on + containers
//                (serves "application = workload" view)
//   Pod        → containers + backlinks to parent workload / namespace
//   Node       → pods on this node grouped by workload (impact analysis)
//
// The general pattern: a header with key/value facts, then sections of
// related assets. Each section uses the list-page table shape so the UX
// feels consistent across the app — most via components/ListSection.
//
// This file is a thin entry point: each page lives under pages/details/.

export { ClusterDetail } from './details/ClusterDetail';
export { NamespaceDetail } from './details/NamespaceDetail';
export { WorkloadDetail } from './details/WorkloadDetail';
export { PodDetail } from './details/PodDetail';
export { NodeDetail } from './details/NodeDetail';
export { IngressDetail } from './details/IngressDetail';
export { ServiceDetail } from './details/ServiceDetail';
export {
  PersistentVolumeDetail,
  PersistentVolumeClaimDetail,
} from './details/PersistentVolumes';
