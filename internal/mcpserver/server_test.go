package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/morgancrozier/searchprobe/internal/gscerr"
	"github.com/morgancrozier/searchprobe/internal/searchconsole"
)

type fakeClient struct {
	mu           sync.Mutex
	calls        []string
	performance  searchconsole.PerformanceRequest
	comparison   searchconsole.CompareRequest
	inspection   searchconsole.InspectionRequest
	sitemapSite  string
	sitemapIndex string
	sitemapURL   string
}

func (f *fakeClient) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeClient) ListSites(context.Context) ([]searchconsole.Site, error) {
	f.record("sites")
	return []searchconsole.Site{{SiteURL: "sc-domain:example.com", Type: "domain", PermissionLevel: "siteOwner"}}, nil
}

func performanceResult(req searchconsole.PerformanceRequest) *searchconsole.PerformanceResult {
	limit := req.RowLimit
	if limit == 0 {
		limit = searchconsole.MaxRowLimit
	}
	return &searchconsole.PerformanceResult{
		Site: req.Site, StartDate: req.StartDate, EndDate: req.EndDate,
		Dimensions: req.Dimensions, SearchType: defaultString(req.SearchType, searchconsole.SearchTypeWeb),
		DataState: defaultString(req.DataState, searchconsole.DataStateFinal), Filters: req.Filters,
		AggregationType: req.AggregationType, RowLimit: limit, StartRow: req.StartRow,
		Rows:         []searchconsole.Row{{Dimensions: req.Dimensions, Keys: map[string]string{"query": "example"}, Clicks: 2, Impressions: 20, CTR: 0.1, Position: 3}},
		PagesFetched: 1, PaginationExhausted: true,
	}
}

func (f *fakeClient) QueryPerformance(_ context.Context, req searchconsole.PerformanceRequest) (*searchconsole.PerformanceResult, error) {
	f.record("performance")
	f.performance = req
	return performanceResult(req), nil
}

func (f *fakeClient) QueryPerformanceAll(_ context.Context, req searchconsole.PerformanceRequest) (*searchconsole.PerformanceResult, error) {
	f.record("performance-all")
	f.performance = req
	return performanceResult(req), nil
}

func (f *fakeClient) ComparePerformance(_ context.Context, req searchconsole.CompareRequest) (*searchconsole.CompareResult, error) {
	f.record("compare")
	f.comparison = req
	return &searchconsole.CompareResult{
		Site: req.Base.Site, Dimensions: req.Base.Dimensions, SearchType: req.Base.SearchType,
		DataState: req.Base.DataState, Filters: req.Base.Filters,
		Current:  searchconsole.Window{StartDate: req.Base.StartDate, EndDate: req.Base.EndDate, Days: 7, PagesFetched: 1},
		Previous: searchconsole.Window{StartDate: req.PreviousStartDate, EndDate: req.PreviousEndDate, Days: 7, PagesFetched: 1},
		Rows:     []searchconsole.CompareRow{}, CurrentTotals: searchconsole.Totals{}, PrevTotals: searchconsole.Totals{}, TotalsDelta: searchconsole.TotalsDelta{},
	}, nil
}

func (f *fakeClient) InspectURL(_ context.Context, req searchconsole.InspectionRequest) (*searchconsole.InspectionResult, error) {
	f.record("inspect")
	f.inspection = req
	return &searchconsole.InspectionResult{Site: req.Site, InspectionURL: req.InspectionURL, IndexStatus: &searchconsole.IndexStatus{Verdict: "PASS", Sitemaps: []string{}, ReferringURLs: []string{}}}, nil
}

func (f *fakeClient) ListSitemaps(_ context.Context, site, index string) ([]searchconsole.Sitemap, error) {
	f.record("sitemaps")
	f.sitemapSite, f.sitemapIndex = site, index
	return []searchconsole.Sitemap{{Path: "https://example.com/sitemap.xml", Type: "sitemap", Contents: []searchconsole.SitemapContent{}}}, nil
}

func (f *fakeClient) GetSitemap(_ context.Context, site, sitemapURL string) (*searchconsole.Sitemap, error) {
	f.record("sitemap")
	f.sitemapSite, f.sitemapURL = site, sitemapURL
	return &searchconsole.Sitemap{Path: sitemapURL, Type: "sitemap", Contents: []searchconsole.SitemapContent{}}, nil
}

type testSession struct {
	client *mcp.ClientSession
	cancel context.CancelFunc
	done   <-chan error
}

func connectTestServer(t *testing.T, factory ClientFactory) testSession {
	t.Helper()
	server, err := New(Options{
		Version: "v-test",
		Now:     func() time.Time { return time.Date(2026, 9, 12, 3, 0, 0, 0, time.UTC) },
		Client:  factory,
	})
	if err != nil {
		t.Fatal(err)
	}
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx, serverTransport) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "searchprobe-test", Version: "v-test"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = session.Close()
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("server shutdown: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("server did not stop")
		}
	})
	return testSession{client: session, cancel: cancel, done: done}
}

func callTool(t *testing.T, session *mcp.ClientSession, name string, arguments map[string]any) (*mcp.CallToolResult, map[string]any) {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	b, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var structured map[string]any
	if err := json.Unmarshal(b, &structured); err != nil {
		t.Fatalf("decode %s result: %v (%s)", name, err, b)
	}
	return result, structured
}

func TestServerExposesExpectedToolsAndUsefulSchemas(t *testing.T) {
	fake := &fakeClient{}
	test := connectTestServer(t, func(context.Context) (Client, error) { return fake, nil })
	listed, err := test.client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	info := test.client.InitializeResult().ServerInfo
	if info == nil || info.Name != "searchprobe" || info.Title != "SearchProbe" || info.Version != "v-test" {
		t.Fatalf("server info = %#v", info)
	}
	want := []string{"compare", "inspect", "performance", "sitemap", "sitemaps", "sites"}
	got := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		got = append(got, tool.Name)
		if tool.Description == "" {
			t.Errorf("%s has no description", tool.Name)
		}
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s is not marked read-only", tool.Name)
		}
		schema, ok := tool.InputSchema.(map[string]any)
		if !ok || schema["type"] != "object" {
			t.Errorf("%s input schema = %#v", tool.Name, tool.InputSchema)
			continue
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok || len(properties) == 0 && tool.Name != "sites" {
			t.Errorf("%s schema properties = %#v", tool.Name, schema["properties"])
		}
		if tool.Name != "sites" {
			if _, ok := properties["site"]; !ok {
				t.Errorf("%s schema has no site property", tool.Name)
			}
			required, _ := schema["required"].([]any)
			if !contains(required, "site") {
				t.Errorf("%s schema does not require site: %#v", tool.Name, schema["required"])
			}
		}
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("tools = %v, want %v", got, want)
	}
}

func contains(values []any, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestEveryToolRoutesToSearchConsoleAndReturnsStructuredOutput(t *testing.T) {
	fake := &fakeClient{}
	test := connectTestServer(t, func(context.Context) (Client, error) { return fake, nil })
	calls := []struct {
		name string
		args map[string]any
	}{
		{"sites", map[string]any{}},
		{"performance", map[string]any{"site": "sc-domain:example.com", "startDate": "2026-09-01", "endDate": "2026-09-07", "dimensions": []string{"query"}}},
		{"compare", map[string]any{"site": "sc-domain:example.com", "startDate": "2026-09-01", "endDate": "2026-09-07", "compareStartDate": "2026-08-25", "compareEndDate": "2026-08-31"}},
		{"inspect", map[string]any{"site": "sc-domain:example.com", "urls": []string{"https://example.com/page"}}},
		{"sitemaps", map[string]any{"site": "sc-domain:example.com", "sitemapIndex": "https://example.com/index.xml"}},
		{"sitemap", map[string]any{"site": "sc-domain:example.com", "sitemapUrl": "https://example.com/sitemap.xml"}},
	}
	for _, call := range calls {
		result, structured := callTool(t, test.client, call.name, call.args)
		if result.IsError || structured["ok"] != true {
			t.Errorf("%s result = %#v, isError=%v", call.name, structured, result.IsError)
		}
		if structured["data"] == nil || structured["meta"] == nil {
			t.Errorf("%s must return data and meta: %#v", call.name, structured)
		}
		if _, ok := structured["warnings"].([]any); !ok {
			t.Errorf("%s warnings must be an array: %#v", call.name, structured["warnings"])
		}
	}
	if got, want := strings.Join(fake.calls, ","), "sites,performance,compare,inspect,sitemaps,sitemap"; got != want {
		t.Fatalf("calls = %s, want %s", got, want)
	}
	if fake.performance.StartDate != "2026-09-01" || fake.performance.EndDate != "2026-09-07" {
		t.Errorf("performance request = %+v", fake.performance)
	}
	if fake.comparison.PreviousStartDate != "2026-08-25" || fake.comparison.PreviousEndDate != "2026-08-31" {
		t.Errorf("compare request = %+v", fake.comparison)
	}
	if fake.inspection.InspectionURL != "https://example.com/page" {
		t.Errorf("inspection request = %+v", fake.inspection)
	}
	if fake.sitemapIndex != "https://example.com/index.xml" || fake.sitemapURL != "https://example.com/sitemap.xml" {
		t.Errorf("sitemap args = index %q url %q", fake.sitemapIndex, fake.sitemapURL)
	}
}

func TestPerformanceAllUsesAutomaticPagination(t *testing.T) {
	fake := &fakeClient{}
	test := connectTestServer(t, func(context.Context) (Client, error) { return fake, nil })
	result, structured := callTool(t, test.client, "performance", map[string]any{
		"site": "sc-domain:example.com", "days": 7, "all": true, "dimensions": []string{},
	})
	if result.IsError || structured["ok"] != true {
		t.Fatalf("result = %#v", structured)
	}
	if len(fake.calls) != 1 || fake.calls[0] != "performance-all" {
		t.Fatalf("calls = %v", fake.calls)
	}
	if fake.performance.RowLimit != 0 || fake.performance.StartRow != 0 || fake.performance.StartDate != "2026-09-04" || fake.performance.EndDate != "2026-09-10" || fake.performance.Dimensions != nil {
		t.Errorf("request = %+v", fake.performance)
	}
	warnings := structured["warnings"].([]any)
	if len(warnings) == 0 || warnings[0].(map[string]any)["code"] != "TOP_ROWS_ONLY" {
		t.Errorf("warnings = %#v", warnings)
	}
}

func TestMissingAuthenticationIsStructuredToolFailure(t *testing.T) {
	test := connectTestServer(t, func(context.Context) (Client, error) { return nil, gscerr.AuthRequired() })
	result, structured := callTool(t, test.client, "sites", map[string]any{})
	if !result.IsError || structured["ok"] != false {
		t.Fatalf("result = %#v isError=%v", structured, result.IsError)
	}
	errValue, ok := structured["error"].(map[string]any)
	if !ok || errValue["code"] != gscerr.CodeAuthRequired || !strings.Contains(errValue["action"].(string), "gsc setup") {
		t.Fatalf("error = %#v", structured["error"])
	}
	if _, ok := structured["warnings"].([]any); !ok {
		t.Fatalf("warnings must be structured: %#v", structured["warnings"])
	}
}

func TestCancellationStopsServerCleanly(t *testing.T) {
	server, err := New(Options{Version: "v-test", Client: func(context.Context) (Client, error) { return &fakeClient{}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx, serverTransport) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v-test"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop after cancellation")
	}
}

func TestClientDisconnectStopsServerCleanly(t *testing.T) {
	server, err := New(Options{Version: "v-test", Client: func(context.Context) (Client, error) { return &fakeClient{}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	done := make(chan error, 1)
	go func() { done <- server.Run(context.Background(), serverTransport) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v-test"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop after client disconnect")
	}
}

type writeCloseBuffer struct {
	io.Writer
	close func() error
}

func (w writeCloseBuffer) Close() error { return w.close() }

func TestProtocolOutputContainsOnlyJSONMessages(t *testing.T) {
	server, err := New(Options{Version: "v-test", Client: func(context.Context) (Client, error) { return &fakeClient{}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	clientToServerReader, clientToServerWriter := io.Pipe()
	serverToClientReader, serverToClientWriter := io.Pipe()
	var stdout bytes.Buffer
	serverTransport := &mcp.IOTransport{
		Reader: clientToServerReader,
		Writer: writeCloseBuffer{Writer: io.MultiWriter(serverToClientWriter, &stdout), close: serverToClientWriter.Close},
	}
	clientTransport := &mcp.IOTransport{Reader: serverToClientReader, Writer: clientToServerWriter}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx, serverTransport) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v-test"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.ListTools(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	_ = session.Close()
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, io.EOF) {
			t.Fatalf("server stopped with %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop")
	}
	lines := bytes.Split(bytes.TrimSpace(stdout.Bytes()), []byte("\n"))
	if len(lines) < 2 {
		t.Fatalf("expected protocol messages, got %q", stdout.String())
	}
	for i, line := range lines {
		if !json.Valid(line) {
			t.Fatalf("stdout line %d is not a protocol JSON message: %q", i+1, line)
		}
	}
}
