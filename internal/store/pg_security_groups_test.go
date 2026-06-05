package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
)

func TestUpsertSecurityGroup_InsertThenUpdate(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	acc, err := pg.UpsertCloudAccount(ctx, api.CloudAccountUpsert{
		Provider: "outscale", Name: "sg-test-acc",
	})
	if err != nil {
		t.Fatalf("upsert cloud account: %v", err)
	}

	sg := SecurityGroup{
		CloudAccountID: acc.ID,
		ProviderSGID:   "sg-1",
		Name:           "pg",
		VPCID:          "vpc-aaa",
		Tags:           json.RawMessage(`{"env":"prod"}`),
	}
	id1, err := pg.UpsertSecurityGroup(ctx, sg)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	sg.Name = "pg-renamed"
	id2, err := pg.UpsertSecurityGroup(ctx, sg)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("upsert keyed on (account, provider_sg_id) should keep id stable")
	}
	got, err := pg.GetSecurityGroup(ctx, id1)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "pg-renamed" {
		t.Fatalf("expected rename, got %q", got.Name)
	}
}

func TestGetSecurityGroup_NotFound(t *testing.T) {
	pg := newTestPG(t)
	_, err := pg.GetSecurityGroup(context.Background(), uuid.New())
	if !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
