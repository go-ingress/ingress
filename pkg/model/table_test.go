package model

import "testing"

func TestMatch_ExactHost_PrefixRoot(t *testing.T) {
	table := &RoutingTable{
		Hosts: map[string]*HostRules{
			"foo.local": {Host: "foo.local", Paths: []*PathRule{
				{PathType: PathTypePrefix, Path: "/", Backend: &BackendRef{ServiceName: "svc-a"}},
			}},
		},
	}
	rule, ok := table.Match("foo.local", "/", "GET", nil)
	if !ok || rule.Backend.ServiceName != "svc-a" {
		t.Fatalf("expected svc-a, got ok=%v rule=%+v", ok, rule)
	}
}

func TestMatch_PrefixBoundary(t *testing.T) {
	// K8s Prefix 语义：/api 匹配 /api、/api/...，但不匹配 /apis。
	table := &RoutingTable{
		Hosts: map[string]*HostRules{
			"foo.local": {Host: "foo.local", Paths: []*PathRule{
				{PathType: PathTypePrefix, Path: "/api", Backend: &BackendRef{ServiceName: "api"}},
			}},
		},
	}
	if _, ok := table.Match("foo.local", "/api", "GET", nil); !ok {
		t.Fatal("/api should match /api")
	}
	if _, ok := table.Match("foo.local", "/api/x", "GET", nil); !ok {
		t.Fatal("/api should match /api/x")
	}
	if _, ok := table.Match("foo.local", "/apis", "GET", nil); ok {
		t.Fatal("/api should NOT match /apis")
	}
}

func TestMatch_ExactOverPrefix(t *testing.T) {
	// Exact 优先级高于 Prefix。
	table := &RoutingTable{
		Hosts: map[string]*HostRules{
			"foo.local": {Host: "foo.local", Paths: []*PathRule{
				{PathType: PathTypePrefix, Path: "/", Backend: &BackendRef{ServiceName: "fallback"}},
				{PathType: PathTypeExact, Path: "/health", Backend: &BackendRef{ServiceName: "exact"}},
			}},
		},
	}
	if rule, _ := table.Match("foo.local", "/health", "GET", nil); rule.Backend.ServiceName != "exact" {
		t.Fatal("Exact should win over Prefix")
	}
	if rule, _ := table.Match("foo.local", "/healthz", "GET", nil); rule.Backend.ServiceName != "fallback" {
		t.Fatal("/healthz should fall back to Prefix /")
	}
}

func TestMatch_WildcardHost(t *testing.T) {
	wild := []*HostRules{{Host: "*.example.com", Paths: []*PathRule{
		{PathType: PathTypePrefix, Path: "/", Backend: &BackendRef{ServiceName: "wild"}}}}}
	SortWildcards(wild)
	table := &RoutingTable{Wildcards: wild}
	rule, ok := table.Match("a.example.com", "/", "GET", nil)
	if !ok || rule.Backend.ServiceName != "wild" {
		t.Fatalf("wildcard should match a.example.com, ok=%v", ok)
	}
	if _, ok := table.Match("example.com", "/", "GET", nil); ok {
		t.Fatal("wildcard should NOT match bare example.com")
	}
}

func TestMatch_HostWithPort(t *testing.T) {
	table := &RoutingTable{
		Hosts: map[string]*HostRules{
			"foo.local": {Host: "foo.local", Paths: []*PathRule{
				{PathType: PathTypePrefix, Path: "/", Backend: &BackendRef{ServiceName: "svc"}},
			}},
		},
	}
	rule, ok := table.Match("foo.local:8080", "/", "GET", nil)
	if !ok || rule.Backend.ServiceName != "svc" {
		t.Fatalf("should strip port, ok=%v", ok)
	}
}

func TestMatch_NoMatch(t *testing.T) {
	table := &RoutingTable{Hosts: map[string]*HostRules{}}
	if _, ok := table.Match("foo.local", "/", "GET", nil); ok {
		t.Fatal("should not match empty table")
	}
}

func TestMatch_MethodFilter(t *testing.T) {
	table := &RoutingTable{
		Hosts: map[string]*HostRules{
			"foo.local": {Host: "foo.local", Paths: []*PathRule{
				{PathType: PathTypePrefix, Path: "/webhook", Method: "POST", Backend: &BackendRef{ServiceName: "hook"}}},
			},
		},
	}
	if _, ok := table.Match("foo.local", "/webhook", "GET", nil); ok {
		t.Fatal("GET should not match POST-only rule")
	}
	rule, ok := table.Match("foo.local", "/webhook", "POST", nil)
	if !ok || rule.Backend.ServiceName != "hook" {
		t.Fatal("POST should match")
	}
}
