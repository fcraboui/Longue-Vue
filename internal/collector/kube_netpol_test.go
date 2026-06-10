package collector

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
)

const (
	testNSProd      = "prod"
	testNetCIDR10   = "10.0.0.0/8"
	testNetPolName  = "api-allow"
	testNetPolPola  = "pol-a"
	testLabelAppWeb = "web"

	// Test namespace + netpol names shared across collector tests (extracted
	// to satisfy goconst).
	testNetpolNSTeamA  = "team-a"
	testNetpolNSTeamB  = "team-b"
	testNetpolNSGhost  = "ghost-ns"
	testNetpolAllowDNS = "allow-dns"
	testNetpolOrphan   = "orphan"
)

func TestKubeSourceListAllNetworkPolicies_SelectorPeer(t *testing.T) {
	ctx := context.Background()
	port := intstr.FromInt(8080)
	proto := corev1.ProtocolTCP
	cs := fake.NewSimpleClientset(&netv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: testNetPolName, Namespace: testNSProd},
		Spec: netv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
			PolicyTypes: []netv1.PolicyType{netv1.PolicyTypeIngress},
			Ingress: []netv1.NetworkPolicyIngressRule{{
				From:  []netv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": testLabelAppWeb}}}},
				Ports: []netv1.NetworkPolicyPort{{Protocol: &proto, Port: &port}},
			}},
		},
	})
	src := &KubeClient{clientset: cs}
	got, err := src.ListAllNetworkPolicies(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].Name != testNetPolName {
		t.Fatalf("want 1 netpol api-allow, got %+v", got)
	}
	if len(got[0].PolicyTypes) != 1 || got[0].PolicyTypes[0] != "Ingress" {
		t.Fatalf("policy types: %+v", got[0].PolicyTypes)
	}
	if len(got[0].Ingress) != 1 || len(got[0].Ingress[0].Peers) != 1 {
		t.Fatalf("ingress rules: %+v", got[0].Ingress)
	}
	if got[0].Ingress[0].Peers[0].Kind != peerKindSelector {
		t.Fatalf("peer kind: %q", got[0].Ingress[0].Peers[0].Kind)
	}
}

func TestKubeSourceListAllNetworkPolicies_IPBlock(t *testing.T) {
	ctx := context.Background()
	cs := fake.NewSimpleClientset(&netv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "egress-external", Namespace: testNSProd},
		Spec: netv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []netv1.PolicyType{netv1.PolicyTypeEgress},
			Egress: []netv1.NetworkPolicyEgressRule{{
				To: []netv1.NetworkPolicyPeer{{
					IPBlock: &netv1.IPBlock{CIDR: testNetCIDR10, Except: []string{"10.1.0.0/16"}},
				}},
			}},
		},
	})
	src := &KubeClient{clientset: cs}
	got, err := src.ListAllNetworkPolicies(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 netpol, got %d", len(got))
	}
	if len(got[0].Egress) != 1 || len(got[0].Egress[0].Peers) != 1 {
		t.Fatalf("egress rules: %+v", got[0].Egress)
	}
	peer := got[0].Egress[0].Peers[0]
	if peer.Kind != peerKindIPBlock {
		t.Fatalf("peer kind: %q", peer.Kind)
	}
	if peer.IPBlockCIDR != testNetCIDR10 {
		t.Fatalf("CIDR: %q", peer.IPBlockCIDR)
	}
}

func TestKubeSourceListAllNetworkPolicies_Empty(t *testing.T) {
	ctx := context.Background()
	cs := fake.NewSimpleClientset()
	src := &KubeClient{clientset: cs}
	got, err := src.ListAllNetworkPolicies(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want 0 netpols, got %d", len(got))
	}
}

// TestKubeSourceListAllNetworkPolicies_MultiNamespace asserts the cluster-wide
// list returns NetworkPolicies from every namespace in one call, with each
// item carrying the correct Namespace field.
func TestKubeSourceListAllNetworkPolicies_MultiNamespace(t *testing.T) {
	ctx := context.Background()
	cs := fake.NewSimpleClientset(
		&netv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "deny-all", Namespace: testNetpolNSTeamA},
			Spec:       netv1.NetworkPolicySpec{PolicyTypes: []netv1.PolicyType{netv1.PolicyTypeIngress}},
		},
		&netv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: testNetpolAllowDNS, Namespace: testNetpolNSTeamB},
			Spec:       netv1.NetworkPolicySpec{PolicyTypes: []netv1.PolicyType{netv1.PolicyTypeEgress}},
		},
	)
	src := &KubeClient{clientset: cs}

	got, err := src.ListAllNetworkPolicies(ctx)
	if err != nil {
		t.Fatalf("ListAllNetworkPolicies: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 netpols, got %d: %+v", len(got), got)
	}
	nsByName := map[string]string{}
	for _, np := range got {
		nsByName[np.Name] = np.Namespace
	}
	if nsByName["deny-all"] != testNetpolNSTeamA {
		t.Errorf("deny-all namespace: got %q, want %s", nsByName["deny-all"], testNetpolNSTeamA)
	}
	if nsByName[testNetpolAllowDNS] != testNetpolNSTeamB {
		t.Errorf("allow-dns namespace: got %q, want %s", nsByName[testNetpolAllowDNS], testNetpolNSTeamB)
	}
}
