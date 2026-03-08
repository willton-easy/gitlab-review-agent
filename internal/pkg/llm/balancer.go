package llm

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"ai-review-agent/internal/shared"
)

// BalancedClient distributes LLM calls across multiple clients of the same
// provider/model using least-connections strategy for fair load distribution.
type BalancedClient struct {
	clients []clientEntry
	mu      sync.Mutex
}

type clientEntry struct {
	client   shared.LLMClient
	inFlight atomic.Int64
	total    atomic.Int64
}

// NewBalancedClient creates a load-balanced wrapper around multiple LLM clients.
// All clients must use the same model — the balancer only distributes API key load.
func NewBalancedClient(clients []shared.LLMClient) (*BalancedClient, error) {
	if len(clients) == 0 {
		return nil, fmt.Errorf("at least one LLM client is required")
	}

	entries := make([]clientEntry, len(clients))
	for i, c := range clients {
		entries[i] = clientEntry{client: c}
	}

	return &BalancedClient{clients: entries}, nil
}

// pick selects the client with the fewest in-flight requests.
// On tie, the client with fewer total calls is preferred.
func (b *BalancedClient) pick() *clientEntry {
	b.mu.Lock()
	defer b.mu.Unlock()

	best := 0
	bestFlight := b.clients[0].inFlight.Load()
	bestTotal := b.clients[0].total.Load()

	for i := 1; i < len(b.clients); i++ {
		flight := b.clients[i].inFlight.Load()
		total := b.clients[i].total.Load()
		if flight < bestFlight || (flight == bestFlight && total < bestTotal) {
			best = i
			bestFlight = flight
			bestTotal = total
		}
	}

	return &b.clients[best]
}

func (b *BalancedClient) Chat(ctx context.Context, req shared.ChatRequest) (*shared.ChatResponse, error) {
	entry := b.pick()
	entry.inFlight.Add(1)
	entry.total.Add(1)
	defer entry.inFlight.Add(-1)

	slog.Debug("balancer: routing request",
		"model", entry.client.ModelName(),
		"in_flight", entry.inFlight.Load(),
		"total", entry.total.Load(),
		"pool_size", len(b.clients),
	)

	req.Model = entry.client.ModelName()
	return entry.client.Chat(ctx, req)
}

func (b *BalancedClient) ModelName() string {
	return b.clients[0].client.ModelName()
}

func (b *BalancedClient) ContextWindowSize() int {
	return b.clients[0].client.ContextWindowSize()
}

// ClientCount returns the number of underlying clients.
func (b *BalancedClient) ClientCount() int {
	return len(b.clients)
}
