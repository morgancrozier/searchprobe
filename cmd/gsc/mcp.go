package main

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/morgancrozier/searchprobe/internal/mcpserver"
)

func newMCPCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run a local read-only MCP server over stdio",
		Long: `Run SearchProbe as a Model Context Protocol server over stdin and stdout.

The server exposes the sites, performance, compare, inspect, sitemaps, and
sitemap tools. It uses the existing local SearchProbe credentials and talks
directly to Google's Search Console API with the read-only OAuth scope.

This command does not open a browser or start an authentication flow. Run
gsc setup in a terminal before starting the MCP server. The process lasts only
for the MCP client session and writes only protocol messages to stdout.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// The root JSON error renderer writes to stdout. Once MCP serving
			// begins, even unexpected process-level failures must keep stdout
			// reserved for protocol traffic, regardless of an inherited flag.
			a.json = false
			server, err := mcpserver.New(mcpserver.Options{
				Version: resolveVersion(),
				Now:     a.now,
				Client: func(ctx context.Context) (mcpserver.Client, error) {
					return a.client(ctx)
				},
			})
			if err != nil {
				return err
			}
			return server.Run(cmd.Context(), &mcp.StdioTransport{})
		},
	}
}
