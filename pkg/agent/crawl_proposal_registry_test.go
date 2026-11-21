package agent

import (
	"context"
	"testing"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func TestMemoryCrawlProposalRegistryEvictsAtCapacity(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	registry := NewMemoryCrawlProposalRegistryWithOptions(2, time.Hour, func() time.Time { return now })
	makeProposal := func(id string, ttl time.Duration) func() (CrawlRequestProposal, error) {
		return func() (CrawlRequestProposal, error) {
			return CrawlRequestProposal{
				RequestID: id, SourceID: 42, OperationType: cdb.CrawlOperationType,
				ExpiresAt: now.Add(ttl),
			}, nil
		}
	}

	first, err := registry.ResolveCrawlProposal(context.Background(), "first", makeProposal("first", time.Hour))
	if err != nil {
		t.Fatalf("first proposal: %v", err)
	}
	if _, err := registry.ResolveCrawlProposal(context.Background(), "second", makeProposal("second", 2*time.Hour)); err != nil {
		t.Fatalf("second proposal: %v", err)
	}
	if registry.Len() != 2 {
		t.Fatalf("registry length = %d, want 2", registry.Len())
	}
	if _, err := registry.ResolveCrawlProposal(context.Background(), "third", makeProposal("third", 3*time.Hour)); err != nil {
		t.Fatalf("third proposal: %v", err)
	}
	if registry.Len() != 2 {
		t.Fatalf("registry length after eviction = %d, want 2", registry.Len())
	}
	recreated, err := registry.ResolveCrawlProposal(context.Background(), "first", makeProposal("first-recreated", time.Hour))
	if err != nil {
		t.Fatalf("resolve evicted proposal: %v", err)
	}
	if recreated.RequestID == first.RequestID {
		t.Fatal("earliest-expiring proposal was not evicted")
	}
}

func TestMemoryCrawlProposalRegistryExpiresProposals(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	registry := NewMemoryCrawlProposalRegistryWithOptions(8, time.Minute, func() time.Time { return now })
	proposal := CrawlRequestProposal{
		RequestID: "expiring", SourceID: 42, OperationType: cdb.CrawlOperationType,
		ExpiresAt: now.Add(time.Minute),
	}
	if _, err := registry.ResolveCrawlProposal(context.Background(), "expiring", func() (CrawlRequestProposal, error) {
		return proposal, nil
	}); err != nil {
		t.Fatalf("create expiring proposal: %v", err)
	}
	now = now.Add(2 * time.Minute)
	recreated, err := registry.ResolveCrawlProposal(context.Background(), "expiring", func() (CrawlRequestProposal, error) {
		return CrawlRequestProposal{
			RequestID: "replacement", SourceID: 42, OperationType: cdb.CrawlOperationType,
			ExpiresAt: now.Add(time.Minute),
		}, nil
	})
	if err != nil {
		t.Fatalf("replace expired proposal: %v", err)
	}
	if recreated.RequestID != "replacement" {
		t.Fatalf("expired proposal was retained: %+v", recreated)
	}
	if registry.Len() != 1 {
		t.Fatalf("registry length = %d, want 1", registry.Len())
	}
}

func TestMemoryCrawlProposalRegistryDefaultsAndContext(t *testing.T) {
	if capacity := NewMemoryCrawlProposalRegistry().capacity; capacity != DefaultCrawlProposalRegistryCapacity {
		t.Fatalf("default capacity = %d", capacity)
	}
	registry := NewMemoryCrawlProposalRegistryWithOptions(0, 0, nil)
	if registry.capacity != DefaultCrawlProposalRegistryCapacity || registry.ttl != maxCrawlProposalLifetime {
		t.Fatalf("invalid defaults were not normalized: capacity=%d ttl=%v", registry.capacity, registry.ttl)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := registry.ResolveCrawlProposal(ctx, "key", func() (CrawlRequestProposal, error) {
		t.Fatal("create called for cancelled context")
		return CrawlRequestProposal{}, nil
	}); err == nil {
		t.Fatal("cancelled context was accepted")
	}
}
