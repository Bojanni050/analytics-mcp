package main

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// De SDK bouwt bij AddTool het JSON-schema van elke tool-input. Een schema dat
// hij niet kan bouwen hoort deze test te breken, niet de productieserver bij
// het opstarten: dan liggen ook de leesroutes en het Adlytica-dashboard eruit.
func TestMcpToolSchemas(t *testing.T) {
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "analytics-mcp", Version: "test"}, nil)
	registerTools(server)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	go func() { _ = server.Run(ctx, serverTransport) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "schema-test", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("verbinden met de eigen server faalde: %v", err)
	}
	defer session.Close()

	result, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list faalde: %v", err)
	}

	const wantTools = 16
	if len(result.Tools) != wantTools {
		t.Errorf("tools = %d, want %d", len(result.Tools), wantTools)
	}

	seen := map[string]bool{}
	withProperties := 0
	for _, tool := range result.Tools {
		if tool.Name == "" || tool.Description == "" {
			t.Errorf("tool zonder naam of omschrijving: %#v", tool)
		}
		if seen[tool.Name] {
			t.Errorf("tool %q staat dubbel", tool.Name)
		}
		seen[tool.Name] = true

		schema, ok := tool.InputSchema.(map[string]any)
		if !ok {
			t.Errorf("tool %q heeft geen leesbaar inputschema (%T)", tool.Name, tool.InputSchema)
			continue
		}
		if _, ok := schema["properties"]; ok {
			withProperties++
		}
	}
	// auth_status heeft geen input; alle andere tools wel.
	if withProperties != wantTools-1 {
		t.Errorf("tools met inputvelden = %d, want %d", withProperties, wantTools-1)
	}

	for _, name := range []string{"ads_mutate", "ads_add_keywords", "ads_set_keyword_status", "ads_remove_keyword", "ads_conversion_action_update"} {
		if !seen[name] {
			t.Errorf("tool %q ontbreekt", name)
		}
	}
}
