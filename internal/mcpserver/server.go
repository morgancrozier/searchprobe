// Package mcpserver exposes SearchProbe's read-only Search Console client as
// an MCP server. It contains no credential or Google API implementation of its
// own; callers inject the same authenticated client used by the CLI.
package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/morgancrozier/searchprobe/internal/searchconsole"
)

// Client is the Search Console functionality used by the MCP tools.
// *searchconsole.Client implements this interface.
type Client interface {
	ListSites(context.Context) ([]searchconsole.Site, error)
	QueryPerformance(context.Context, searchconsole.PerformanceRequest) (*searchconsole.PerformanceResult, error)
	QueryPerformanceAll(context.Context, searchconsole.PerformanceRequest) (*searchconsole.PerformanceResult, error)
	ComparePerformance(context.Context, searchconsole.CompareRequest) (*searchconsole.CompareResult, error)
	InspectURL(context.Context, searchconsole.InspectionRequest) (*searchconsole.InspectionResult, error)
	ListSitemaps(context.Context, string, string) ([]searchconsole.Sitemap, error)
	GetSitemap(context.Context, string, string) (*searchconsole.Sitemap, error)
}

// ClientFactory loads local credentials and returns an authenticated Search
// Console client. It must not start an interactive authentication flow.
type ClientFactory func(context.Context) (Client, error)

// Options configures a SearchProbe MCP server.
type Options struct {
	Version string
	Now     func() time.Time
	Client  ClientFactory
	Logger  *slog.Logger
}

// Server is one local MCP server instance.
type Server struct {
	protocol *mcp.Server
}

// New creates a SearchProbe server and registers its fixed read-only tool set.
func New(opts Options) (*Server, error) {
	if opts.Client == nil {
		return nil, fmt.Errorf("mcpserver: client factory is required")
	}
	if opts.Version == "" {
		opts.Version = "dev"
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	serverOpts := &mcp.ServerOptions{
		// SearchProbe does not expose MCP logging, prompts, or resources. The
		// tools capability is inferred when tools are registered below.
		Capabilities: &mcp.ServerCapabilities{},
		Logger:       opts.Logger,
	}
	protocol := mcp.NewServer(&mcp.Implementation{
		Name:        "searchprobe",
		Title:       "SearchProbe",
		Description: "Local, read-only Google Search Console tools using the user's SearchProbe credentials.",
		Version:     opts.Version,
		WebsiteURL:  "https://searchprobe.com",
	}, serverOpts)

	s := &Server{protocol: protocol}
	registerTools(protocol, opts)
	return s, nil
}

// Run serves one MCP client session over transport. It returns nil for normal
// client disconnect and context cancellation so the stdio process exits cleanly.
func (s *Server) Run(ctx context.Context, transport mcp.Transport) error {
	if transport == nil {
		return fmt.Errorf("mcpserver: transport is required")
	}
	err := s.protocol.Run(ctx, transport)
	if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) || errors.Is(err, mcp.ErrConnectionClosed) {
		return nil
	}
	return err
}
