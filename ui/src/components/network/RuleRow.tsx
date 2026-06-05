interface Port { protocol?: string; port?: number | string; endPort?: number }

interface Props {
  direction: "ingress" | "egress";
  protocol?: string;
  fromPort?: number | null;
  toPort?: number | null;
  ports?: Port[];
  peer: string;
  description?: string;
}

export function RuleRow(p: Props) {
  const ports = p.ports?.length
    ? p.ports.map((pt) => `${pt.protocol ?? "any"} ${pt.port ?? "*"}${pt.endPort ? `-${pt.endPort}` : ""}`).join(", ")
    : p.protocol
      ? p.fromPort != null
        ? `${p.protocol} ${p.fromPort}${p.toPort != null && p.toPort !== p.fromPort ? `-${p.toPort}` : ""}`
        : `${p.protocol} any`
      : "any";
  const arrow = p.direction === "ingress" ? "← from" : "→ to";
  return (
    <div className="rule-row">
      <span className="rule-dir">{p.direction}</span>
      <span className="rule-ports">{ports}</span>
      <span className="rule-arrow">{arrow}</span>
      <span className="rule-peer">{p.peer}</span>
      {p.description ? <span className="rule-desc">— {p.description}</span> : null}
    </div>
  );
}
