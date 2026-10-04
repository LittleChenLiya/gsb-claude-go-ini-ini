// Copyright 2026 Unknwon
//
// Licensed under the Apache License, Version 2.0 (the "License"): you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
// WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
// License for the specific language governing permissions and limitations
// under the License.

package ini

import (
	"fmt"
	"strings"
)

// PreflightEdge is one directed edge in the variable reference graph.
// It describes an effective key that refers to another key via a
// "%(name)s" substitution, and records where the effective value came from.
type PreflightEdge struct {
	// Section is the name of the section that owns the referring key.
	Section string
	// Key is the name of the referring key.
	Key string
	// Source is the name of the data source the effective value came from,
	// e.g. the file path for file sources. It is "<data>" for in-memory
	// sources and "<unknown>" when the value was set programmatically.
	Source string
	// Ref is the referenced key name exactly as written in the value.
	Ref string
}

func (e PreflightEdge) node() string {
	return fmt.Sprintf("[%s] %s (%s)", e.Section, e.Key, e.Source)
}

// PreflightIssueKind is the kind of problem detected by Preflight.
type PreflightIssueKind uint8

const (
	// PreflightIssueUndefined means a key refers to a name that is not
	// defined anywhere in the effective configuration.
	PreflightIssueUndefined PreflightIssueKind = iota
	// PreflightIssueCycle means the reference graph contains a cycle,
	// i.e. expanding the values would never terminate.
	PreflightIssueCycle
)

// PreflightIssue describes a single problem found during preflight.
type PreflightIssue struct {
	// Kind is the kind of the issue.
	Kind PreflightIssueKind
	// Edges are the edges involved in the issue.
	// For PreflightIssueUndefined it holds the single dangling edge.
	// For PreflightIssueCycle it holds the edges that form the cycle,
	// ordered so that Edges[i] leads to Edges[i+1] and the last edge
	// leads back to the first one.
	Edges []PreflightEdge
}

func (i PreflightIssue) Error() string {
	switch i.Kind {
	case PreflightIssueCycle:
		var b strings.Builder
		b.WriteString("variable reference cycle: ")
		for idx, e := range i.Edges {
			if idx > 0 {
				b.WriteString(" -> ")
			}
			b.WriteString(e.node())
		}
		if len(i.Edges) > 0 {
			b.WriteString(" -> ")
			b.WriteString(i.Edges[0].node())
		}
		return b.String()
	default:
		e := i.Edges[0]
		return fmt.Sprintf("undefined variable reference: %s refers to %%(%s)s, which is not defined", e.node(), e.Ref)
	}
}

// PreflightError is the error returned by Preflight when it found one or
// more problems in the effective configuration.
type PreflightError struct {
	Issues []PreflightIssue
}

func (e *PreflightError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "ini: %d preflight issue(s):", len(e.Issues))
	for _, issue := range e.Issues {
		b.WriteString("\n  - ")
		b.WriteString(issue.Error())
	}
	return b.String()
}

// preflightNode is an effective key together with the data needed to
// build its outgoing edges.
type preflightNode struct {
	sec     *Section
	key     *Key
	secName string
	keyName string
	raw     string
	source  int
}

type preflightRefEdge struct {
	to     *Key
	detail PreflightEdge
}

// Preflight performs an explicit, read-only check of the variable
// reference graph over the effective (post-overlay) key values.
//
// Multiple data sources are overlaid in order, so only the final value
// of each key is considered: references that only existed in earlier
// layers but have been overwritten are not reported. The resolution of
// references follows the exact same rules as Key.String(): the same
// section first (including child-section inheritance), then the default
// section, and section/key name casing follows the configured
// Insensitive options.
//
// It returns nil when every reference resolves and the graph is acyclic.
// Otherwise it returns a *PreflightError describing undefined references
// and cycles separately. File contents are never modified and no
// pseudo-expanded values are produced.
func (f *File) Preflight() error {
	adj, missing := f.buildReferenceGraph()

	issues := make([]PreflightIssue, 0, len(missing))
	for _, e := range missing {
		issues = append(issues, PreflightIssue{Kind: PreflightIssueUndefined, Edges: []PreflightEdge{e}})
	}
	for _, edges := range findReferenceCycles(adj) {
		issues = append(issues, PreflightIssue{Kind: PreflightIssueCycle, Edges: edges})
	}
	if len(issues) == 0 {
		return nil
	}
	return &PreflightError{Issues: issues}
}

// buildReferenceGraph snapshots the effective keys without mutating the
// file, then builds the reference graph.
func (f *File) buildReferenceGraph() (map[*Key][]preflightRefEdge, []PreflightEdge) {
	if f.BlockMode {
		f.lock.RLock()
	}
	nodes := make([]preflightNode, 0, len(f.sections))
	for _, sec := range f.Sections() {
		for _, keyName := range sec.keyList {
			key := sec.keys[keyName]
			if key == nil {
				continue
			}
			nodes = append(nodes, preflightNode{
				sec:     sec,
				key:     key,
				secName: sec.name,
				keyName: keyName,
				raw:     key.value,
				source:  key.source,
			})
		}
	}
	if f.BlockMode {
		f.lock.RUnlock()
	}

	adj := make(map[*Key][]preflightRefEdge, len(nodes))
	var missing []PreflightEdge

	for _, n := range nodes {
		raw := n.raw
		if n.sec.f.ValueMapper != nil {
			raw = n.sec.f.ValueMapper(raw)
		}
		// Fail-fast if no indicate char found for recursive value,
		// mirroring Key.transformValue.
		if !strings.Contains(raw, "%") {
			continue
		}

		seen := make(map[string]bool)
		for _, match := range varPattern.FindAllStringSubmatch(raw, -1) {
			ref := match[1]
			if seen[ref] {
				continue
			}
			seen[ref] = true

			edge := PreflightEdge{
				Section: n.secName,
				Key:     n.keyName,
				Source:  f.sourceName(n.source),
				Ref:     ref,
			}

			target := f.resolveReference(n.key, ref)
			if target == nil {
				missing = append(missing, edge)
				continue
			}
			adj[n.key] = append(adj[n.key], preflightRefEdge{to: target, detail: edge})
		}
	}
	return adj, missing
}

// resolveReference resolves a variable reference the same way
// Key.transformValue does: search the owning section (including parent
// sections through child-section inheritance), then fall back to the
// default section. It never creates missing sections or keys.
func (f *File) resolveReference(owner *Key, name string) *Key {
	nk, err := owner.s.GetKey(name)
	if err != nil {
		// Not found in the same section (nor its parent sections):
		// try the default section, mirroring transformValue.
		def, derr := f.GetSection(DefaultSection)
		if derr != nil {
			return nil
		}
		nk, err = def.GetKey(name)
		if err != nil {
			return nil
		}
		return nk
	}
	// GetKey resolved the name to the owner itself. transformValue falls
	// back to the default section in that case; if the default section
	// does not define the name either, the reference forms a self-cycle.
	if owner == nk {
		if def, derr := f.GetSection(DefaultSection); derr == nil {
			if dk, err := def.GetKey(name); err == nil {
				return dk
			}
		}
		return owner
	}
	return nk
}

// sourceName returns a human-readable name for the data source at idx.
func (f *File) sourceName(idx int) string {
	if idx >= 0 && idx < len(f.dataSources) {
		return f.dataSources[idx].Name()
	}
	return "<unknown>"
}

// findReferenceCycles runs a depth-first search over the reference graph
// and returns one ordered list of edges per cycle found.
func findReferenceCycles(adj map[*Key][]preflightRefEdge) [][]PreflightEdge {
	const (
		unvisited = 0
		onStack   = 1
		visited   = 2
	)
	color := make(map[*Key]int, len(adj))
	var stack []*Key
	var cycles [][]PreflightEdge

	var dfs func(n *Key)
	dfs = func(n *Key) {
		color[n] = onStack
		stack = append(stack, n)

		for _, e := range adj[n] {
			switch color[e.to] {
			case onStack:
				// Back edge: the path on the stack from e.to to n plus
				// this edge closes a cycle.
				start := 0
				for i, k := range stack {
					if k == e.to {
						start = i
						break
					}
				}
				path := stack[start:]
				edges := make([]PreflightEdge, 0, len(path))
				for i := range path {
					from := path[i]
					to := path[(i+1)%len(path)]
					for _, fe := range adj[from] {
						if fe.to == to {
							edges = append(edges, fe.detail)
							break
						}
					}
				}
				cycles = append(cycles, edges)
			case unvisited:
				dfs(e.to)
			}
		}

		stack = stack[:len(stack)-1]
		color[n] = visited
	}

	for n := range adj {
		if color[n] == unvisited {
			dfs(n)
		}
	}
	return cycles
}
