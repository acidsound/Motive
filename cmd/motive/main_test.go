package main

import (
	"net/http"
	"testing"

	"github.com/acidsound/Motive/internal/config"
	"github.com/acidsound/Motive/internal/model"
)

// TestProductionClientHasNoTotalTimeout verifies the actual production wiring:
// the model client built by the production entrypoint (newModelClient, used by
// main()) must not set a total http.Client.Timeout. The runtime/request context
// deadline is the sole lifetime authority for streaming responses. This is the
// regression test for the production bug where main.go set a 10-minute total
// timeout while NewFromEnv (the test-protected policy) correctly omitted it.
func TestProductionClientHasNoTotalTimeout(t *testing.T) {
	t.Setenv("MOTIVE_TEMPERATURE", "0.6")
	t.Setenv("MOTIVE_MAX_TOKENS", "0")
	t.Setenv("MOTIVE_HEADER_TIMEOUT", "")

	cfg := &config.Config{
		Default: &config.Provider{
			BaseURL:         "http://127.0.0.1:8080/v1",
			Model:           "test-model",
			APIKey:          "",
			ReasoningEffort: "low",
		},
	}

	client := newModelClient(cfg)
	if client.HTTP == nil {
		t.Fatal("expected HTTP client, got nil")
	}
	if client.HTTP.Timeout != 0 {
		t.Fatalf("production client http.Client.Timeout = %v, want 0 (context deadline is the sole authority)", client.HTTP.Timeout)
	}
	tr, ok := client.HTTP.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", client.HTTP.Transport)
	}
	// The header timeout stays as a hung-server safety net, but must default
	// to the generous value so long reasoning prefill is not killed.
	if tr.ResponseHeaderTimeout != model.DefaultResponseHeaderTimeout {
		t.Fatalf("production client Transport.ResponseHeaderTimeout = %v, want default %v", tr.ResponseHeaderTimeout, model.DefaultResponseHeaderTimeout)
	}
}
