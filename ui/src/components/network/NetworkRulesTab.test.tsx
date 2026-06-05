import { describe, expect, it } from "vitest";
import { http, HttpResponse } from "msw";
import { render, screen, waitFor } from "@testing-library/react";
import { NetworkRulesTab } from "./NetworkRulesTab";
import { server } from "../../test/server";

describe("NetworkRulesTab — workload", () => {
  it("renders day-one wide-open banner when no policies select workload", async () => {
    server.use(
      http.get("/v1/workloads/:id/network-rules", () =>
        HttpResponse.json({
          workload_id: "w1",
          policy_count: 0,
          k8s_default_allow: true,
          matching_policies: [],
        }),
      ),
    );
    render(<NetworkRulesTab kind="workload" id="w1" />);
    await waitFor(() =>
      expect(screen.getByText(/K8s default-allow applies/)).toBeInTheDocument(),
    );
  });

  it("renders policy sections when policies exist", async () => {
    server.use(
      http.get("/v1/workloads/:id/network-rules", () =>
        HttpResponse.json({
          workload_id: "w2",
          policy_count: 1,
          k8s_default_allow: false,
          matching_policies: [
            {
              id: "p1",
              name: "allow-ingress",
              policy_types: ["Ingress"],
              rules: [
                {
                  id: "r1",
                  direction: "ingress",
                  peer_kind: "ip_block",
                  peer_pod_selector: null,
                  peer_namespace_selector: null,
                  peer_ip_block_cidr: "10.0.0.0/8",
                  peer_ip_block_except: null,
                  ports: [{ protocol: "tcp", port: 443 }],
                },
              ],
            },
          ],
        }),
      ),
    );
    render(<NetworkRulesTab kind="workload" id="w2" />);
    await waitFor(() =>
      expect(screen.getByText("allow-ingress")).toBeInTheDocument(),
    );
    expect(screen.getByText("10.0.0.0/8")).toBeInTheDocument();
  });
});

describe("NetworkRulesTab — vm", () => {
  it("renders stale banner when data is stale", async () => {
    server.use(
      http.get("/v1/virtual-machines/:id/network-rules", () =>
        HttpResponse.json({
          vm_id: "vm1",
          stale: true,
          no_security_groups: false,
          attached_security_groups: [],
        }),
      ),
    );
    render(<NetworkRulesTab kind="vm" id="vm1" />);
    await waitFor(() =>
      expect(screen.getByText(/Stale data/)).toBeInTheDocument(),
    );
  });

  it("renders no-security-groups banner", async () => {
    server.use(
      http.get("/v1/virtual-machines/:id/network-rules", () =>
        HttpResponse.json({
          vm_id: "vm2",
          stale: false,
          no_security_groups: true,
          attached_security_groups: [],
        }),
      ),
    );
    render(<NetworkRulesTab kind="vm" id="vm2" />);
    await waitFor(() =>
      expect(screen.getByText(/No security groups on this VM/)).toBeInTheDocument(),
    );
  });
});
