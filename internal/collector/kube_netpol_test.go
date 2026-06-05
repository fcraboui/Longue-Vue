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

func TestKubeSourceListNetworkPolicies_FakeClient(t *testing.T) {
	ctx := context.Background()
	port := intstr.FromInt(8080)
	proto := corev1.ProtocolTCP
	cs := fake.NewSimpleClientset(&netv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "api-allow", Namespace: "prod"},
		Spec: netv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
			PolicyTypes: []netv1.PolicyType{netv1.PolicyTypeIngress},
			Ingress: []netv1.NetworkPolicyIngressRule{{
				From:  []netv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}}},
				Ports: []netv1.NetworkPolicyPort{{Protocol: &proto, Port: &port}},
			}},
		},
	})
	src := &KubeClient{clientset: cs}
	got, err := src.ListNetworkPolicies(ctx, "prod")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].Name != "api-allow" {
		t.Fatalf("want 1 netpol api-allow, got %+v", got)
	}
	if len(got[0].PolicyTypes) != 1 || got[0].PolicyTypes[0] != "Ingress" {
		t.Fatalf("policy types: %+v", got[0].PolicyTypes)
	}
	if len(got[0].Ingress) != 1 || len(got[0].Ingress[0].Peers) != 1 {
		t.Fatalf("ingress rules: %+v", got[0].Ingress)
	}
	if got[0].Ingress[0].Peers[0].Kind != "selector" {
		t.Fatalf("peer kind: %q", got[0].Ingress[0].Peers[0].Kind)
	}
}

func TestKubeSourceListNetworkPolicies_IPBlock(t *testing.T) {
	ctx := context.Background()
	cs := fake.NewSimpleClientset(&netv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "egress-external", Namespace: "prod"},
		Spec: netv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []netv1.PolicyType{netv1.PolicyTypeEgress},
			Egress: []netv1.NetworkPolicyEgressRule{{
				To: []netv1.NetworkPolicyPeer{{
					IPBlock: &netv1.IPBlock{CIDR: "10.0.0.0/8", Except: []string{"10.1.0.0/16"}},
				}},
			}},
		},
	})
	src := &KubeClient{clientset: cs}
	got, err := src.ListNetworkPolicies(ctx, "prod")
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
	if peer.Kind != "ip_block" {
		t.Fatalf("peer kind: %q", peer.Kind)
	}
	if peer.IPBlockCIDR != "10.0.0.0/8" {
		t.Fatalf("CIDR: %q", peer.IPBlockCIDR)
	}
}

func TestKubeSourceListNetworkPolicies_EmptyNamespace(t *testing.T) {
	ctx := context.Background()
	cs := fake.NewSimpleClientset()
	src := &KubeClient{clientset: cs}
	got, err := src.ListNetworkPolicies(ctx, "prod")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want 0 netpols, got %d", len(got))
	}
}
