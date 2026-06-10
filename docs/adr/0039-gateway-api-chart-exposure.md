# 39. Gateway API chart exposure (HTTPRoute + Envoy Gateway SecurityPolicy)

Date: 2026-06-10

Status: Accepted

## Context

`ingress-nginx` is heading to end-of-life and the project is standardising on
Gateway API. The `longue-vue` Helm chart previously exposed the daemon's
Service with a `networking.k8s.io/v1 Ingress` (`templates/ingress.yaml`,
toggled by `ingress.enabled`). We need a Gateway API equivalent for the
chart's *self-exposure*.

This ADR covers only how longue-vue exposes itself via its Helm chart. How the
CMDB *collects and models* Gateway API resources from monitored clusters is a
separate, later effort and is out of scope here.

## Decision

- The chart ships an **`HTTPRoute`** (`gateway.networking.k8s.io/v1`,
  `templates/httproute.yaml`, gated on `httpRoute.enabled`) that attaches to an
  **existing, operator-owned shared Gateway** via `parentRefs`. The chart never
  templates a `Gateway` — the shared Gateway typically fronts multiple tenants
  and owns listener/TLS configuration. `httpRoute.parentRefs[].name` is
  `required` when the route is enabled, so a forgotten Gateway name fails the
  render loudly instead of producing an invalid null reference.
- TLS termination therefore lives on the **Gateway listener** (operator-owned),
  not in the chart. The HTTPRoute binds to a specific listener via
  `parentRefs[].sectionName`. There is no Gateway-path equivalent of
  `ingress.tls`.
- An optional **Envoy Gateway `SecurityPolicy`** (`gateway.envoyproxy.io/v1alpha1`,
  `templates/securitypolicy.yaml`, gated on `securityPolicy.enabled`, default
  off) attaches **route-level** via `targetRefs` to our own HTTPRoute — never to
  the shared Gateway, which would affect every tenant on it. It is a scaffold
  for edge CORS / IP-allowlist (`authorization`); longue-vue keeps doing its own
  session/PAT/OIDC auth, so no edge auth is enabled by default.
- A route-level `SecurityPolicy` **replaces, not merges with**, any
  Gateway-level policy for overlapping settings on this route. Operators with a
  Gateway-level policy should know their route-level policy overrides it for
  this route.
- MCP is **not** exposed at the edge by default (parity with the old Ingress,
  which only routed `http`); the HTTPRoute template documents an opt-in MCP rule.
- The legacy `ingress.yaml` is **deprecated for one release** (kept behind its
  toggle, flagged in `values.yaml` and `NOTES.txt`) and is removed in the next
  major chart version.

## Consequences

- Gateway API CRDs and an Envoy Gateway (or compatible Gateway) must be
  installed in-cluster; Helm does not install them. `NOTES.txt` states this when
  `httpRoute.enabled` is set.
- Operators move exposure-TLS configuration from `ingress.tls` to the shared
  Gateway listener.
- `SecurityPolicy` is Envoy-Gateway-specific. Other Gateway API implementations
  render the `HTTPRoute` fine but ignore the `SecurityPolicy`; those operators
  apply equivalent edge policy by their own means.
- CI renders the chart with `httpRoute.enabled=true` and with
  `securityPolicy.enabled=true` (`helm template`, no cluster needed) so both new
  templates stay covered.

## Alternatives considered

- **Chart templates its own Gateway.** Rejected: the Gateway is shared
  infrastructure owned by the cluster operator, fronts multiple tenants, and
  owns listener/TLS config the chart should not assume. Shipping only the
  HTTPRoute matches Gateway API's role separation (operator owns Gateway, app
  owns Route).
- **SecurityPolicy attached to the Gateway.** Rejected: a Gateway-level policy
  applies to every route on the shared Gateway, not just longue-vue's. Route-level
  attachment keeps the policy self-contained.
- **Hard-remove the Ingress immediately.** Rejected in favour of a one-release
  deprecation so existing installs have an upgrade path.

## References

- ADR-0016, ADR-0017 — public/ingest listeners and TLS posture. Unaffected;
  this ADR concerns only public self-exposure of the daemon Service via the chart.
- ADR-0018 — one Helm chart per binary.
- Gateway API: https://gateway-api.sigs.k8s.io/
- Envoy Gateway SecurityPolicy: https://gateway.envoyproxy.io/docs/api/extension_types/#securitypolicy
