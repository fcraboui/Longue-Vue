import type { PortRange } from '../../api/flows';

// formatPort renders a single PortRange as `tcp/443`, `tcp/443-444`, or
// `tcp/any` when no port bound is set. A nil `from` means "any port"
// (matches the server's PortRange semantics).
export function formatPort(p: PortRange): string {
  const proto = p.protocol || 'any';
  if (p.from === undefined || p.from === null) {
    return `${proto}/any`;
  }
  if (p.to !== undefined && p.to !== null && p.to !== p.from) {
    return `${proto}/${p.from}-${p.to}`;
  }
  return `${proto}/${p.from}`;
}

// formatPorts joins a flow's port list for display, collapsing the empty
// list to "any".
export function formatPorts(ports: PortRange[]): string {
  if (!ports || ports.length === 0) return 'any';
  return ports.map(formatPort).join(', ');
}
