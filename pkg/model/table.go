package model

import (
	"net"
	"net/http"
	"sort"
	"strings"
)

// Match 按 host/path/method 匹配，返回命中的最高优先级 PathRule。
//
// 匹配顺序：精确 host → 通配 host（按精度降序）。host 不含端口（自动剥离）。
// 同一 host 内多路径命中时，按 pathScore 取最高分（Exact > Prefix > ImplSpec，同类按长度降序）。
func (t *RoutingTable) Match(host, path, method string, _ http.Header) (*PathRule, bool) {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(host)

	if rules, ok := t.Hosts[host]; ok {
		if r := matchPaths(rules.Paths, path, method); r != nil {
			return r, true
		}
	}
	for _, rules := range t.Wildcards {
		if matchWildcard(rules.Host, host) {
			if r := matchPaths(rules.Paths, path, method); r != nil {
				return r, true
			}
		}
	}
	return nil, false
}

// matchPaths 在路径规则中找最佳匹配。
func matchPaths(paths []*PathRule, reqPath, method string) *PathRule {
	var best *PathRule
	bestScore := -1
	for _, p := range paths {
		if p.Method != "" && !strings.EqualFold(p.Method, method) {
			continue
		}
		if !matchPath(p, reqPath) {
			continue
		}
		if s := pathScore(p); s > bestScore {
			best = p
			bestScore = s
		}
	}
	return best
}

// matchPath 判断单条路径规则是否匹配当前请求路径。
// Prefix 语义对齐 K8s Ingress：/api 匹配 /api、/api/...，但不匹配 /apis。
func matchPath(p *PathRule, reqPath string) bool {
	switch p.PathType {
	case PathTypeExact:
		return p.Path == reqPath
	case PathTypeImplementationSpecific:
		return strings.HasPrefix(reqPath, p.Path)
	default: // Prefix
		if p.Path == "/" {
			return true
		}
		if !strings.HasPrefix(reqPath, p.Path) {
			return false
		}
		if len(reqPath) > len(p.Path) && reqPath[len(p.Path)] != '/' {
			return false
		}
		return true
	}
}

// pathScore 路径优先级：Exact > Prefix > ImplementationSpecific，同类按长度降序。
func pathScore(p *PathRule) int {
	score := len(p.Path) * 2
	switch p.PathType {
	case PathTypeExact:
		score += 10000
	case PathTypePrefix:
		score += 5000
	}
	return score
}

// matchWildcard 通配 host 匹配：*.example.com 匹配 a.example.com（不匹配 example.com）。
func matchWildcard(pattern, host string) bool {
	if !strings.HasPrefix(pattern, "*.") {
		return false
	}
	suffix := pattern[1:] // .example.com
	return strings.HasSuffix(host, suffix) && len(host) > len(suffix)
}

// SortWildcards 按精度降序排序通配规则（更长的后缀优先匹配）。
// 控制面构建 RoutingTable 时调用。
func SortWildcards(rules []*HostRules) {
	sort.Slice(rules, func(i, j int) bool {
		return len(rules[i].Host) > len(rules[j].Host)
	})
}
