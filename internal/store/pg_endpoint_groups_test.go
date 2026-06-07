package store

import (
	"errors"
	"testing"

	"github.com/sthalbert/longue-vue/internal/api"
)

//nolint:gocyclo // integration test exercises multiple CRUD branches
func TestEndpointGroups_CRUD(t *testing.T) {
	pg := newTestPG(t)
	ctx := t.Context()

	created, err := pg.CreateEndpointGroup(ctx, api.EndpointGroupInput{
		Name:  "corp-vpn",
		Notes: "corporate VPN egress range",
		CIDRs: []string{"10.10.0.0/16"},
	}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Name != "corp-vpn" {
		t.Errorf("name=%q", created.Name)
	}
	if len(created.CIDRs) != 1 || created.CIDRs[0] != "10.10.0.0/16" {
		t.Errorf("cidrs=%v, want [10.10.0.0/16]", created.CIDRs)
	}

	got, err := pg.GetEndpointGroup(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("id mismatch")
	}

	updated, err := pg.UpdateEndpointGroup(ctx, created.ID, api.EndpointGroupInput{
		Name:  "corp-vpn",
		Notes: "updated",
		CIDRs: []string{"192.168.0.0/24", "172.16.0.0/12"},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Notes != "updated" {
		t.Errorf("notes=%q", updated.Notes)
	}
	if len(updated.CIDRs) != 2 {
		t.Errorf("cidrs after update=%v, want 2", updated.CIDRs)
	}
	// The old CIDR must be gone.
	for _, c := range updated.CIDRs {
		if c == "10.10.0.0/16" {
			t.Errorf("old cidr still present: %v", updated.CIDRs)
		}
	}

	list, err := pg.ListEndpointGroups(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list len=%d, want 1", len(list))
	}
	if len(list[0].CIDRs) != 2 {
		t.Errorf("listed cidrs=%v, want 2", list[0].CIDRs)
	}

	if err := pg.DeleteEndpointGroup(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := pg.GetEndpointGroup(ctx, created.ID); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("get after delete should be ErrNotFound, got %v", err)
	}
	if err := pg.DeleteEndpointGroup(ctx, created.ID); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("second delete should be ErrNotFound, got %v", err)
	}
	if _, err := pg.UpdateEndpointGroup(ctx, created.ID, api.EndpointGroupInput{Name: "x"}); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("update after delete should be ErrNotFound, got %v", err)
	}
}
