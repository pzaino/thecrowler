package main

import (
	"context"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mutationInput struct {
	SourceID uint64 `json:"source_id,omitempty" jsonschema:"approved CROWler source identifier"`
}

func mutationHandler(_ context.Context, _ *mcp.CallToolRequest, in mutationInput) (*mcp.CallToolResult, any, error) {
	path := os.Getenv("CROWLER_MCP_MUTATION_COUNTER")
	if path != "" {
		file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(file, "executed source_id=%d\n", in.SourceID)
			_ = file.Close()
		}
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "mutation fixture executed"}}}, nil, nil
}

func main() {
	server := mcp.NewServer(&mcp.Implementation{Name: "crowler-mcp-e2e", Version: "1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "Read-only fixture echo."}, func(_ context.Context, _ *mcp.CallToolRequest, in struct {
		Text string `json:"text,omitempty"`
	}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "echo: " + in.Text}}}, nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "mutate_counter", Description: "Increment the isolated mutation counter if executed."}, mutationHandler)
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
