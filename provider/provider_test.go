package provider

import (
	"context"
	"testing"

	"github.com/manish96170/videofactory/domain"
)

// fakeProvider exists only to prove the Provider interface is satisfiable
// and ergonomic to implement; real providers arrive in later phases.
type fakeProvider struct {
	id   string
	caps domain.ProviderCapabilities
}

func (f fakeProvider) ID() string { return f.id }

func (f fakeProvider) Capabilities(ctx context.Context) (domain.ProviderCapabilities, error) {
	return f.caps, nil
}

var _ Provider = fakeProvider{}

func TestFakeProviderCapabilities(t *testing.T) {
	p := fakeProvider{
		id: "qwen-local",
		caps: domain.ProviderCapabilities{
			ProviderID: "qwen-local",
			Tags:       []string{"semantic-decision"},
			Local:      true,
		},
	}

	caps, err := p.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if !caps.Supports("semantic-decision") {
		t.Fatalf("expected provider to support semantic-decision")
	}
	if p.ID() != "qwen-local" {
		t.Fatalf("unexpected provider ID: %s", p.ID())
	}
}
