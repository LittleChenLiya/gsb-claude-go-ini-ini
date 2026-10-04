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
	"sort"
	"strings"
)

// ReferenceNode identifies a key in the merged configuration. It is the
// vertex type of the variable reference graph built by CheckValueReferences.
type ReferenceNode struct {
	// Section is the name of the section that effectively owns the key.
	// It is an empty string for keys of the default section.
	Section string
	// Key is the effective key name (already lowercased when the load was
	// configured to be case-insensitive).
	Key string
}

// String renders the node as "section.key", or just the key name for keys of
// the default section.
func (n ReferenceNode) String() string {
	if n.Section == "" {
		return n.Key
	}
	return n.Section + "." + n.Key
}

// ReferenceEdge represents a single variable reference from one effective
// key to another key.
type ReferenceEdge struct {
	// From is the key whose value contains the reference.
	From ReferenceNode
	// To is the key the reference resolves to, following the same section
	// inheritance, default-section fallback and case rules as Key.String.
	To ReferenceNode
	// Variable is the raw variable name as written in the value, i.e. the
	// "name" part of a "%(name)s" expression.
	Variable string
	// SourceFile is the data source file from which the effective value of
	// From was loaded. It is empty when the value came from an in-memory
	// data source.
	SourceFile string
	// SourceKey is the key name as written in the source file.
	SourceKey string
}

// MissingReference describes a variable reference that cannot be resolved to
// any existing key after all data sources have been merged.
type MissingReference struct {
	// Edge carries the referencing key, the raw variable name and the
	// provenance (source file and key) of the effective value. Its To field
	// is the zero value.
	Edge ReferenceEdge
}

// ReferenceCycle describes a cycle in the variable reference graph.
type ReferenceCycle struct {
	// Chain is the list of nodes that form the cycle, starting and ending
	// at the same node, e.g. [a b a] for the cycle a -> b -> a.
	Chain []ReferenceNode
	// Edges are the references that form the cycle, in the same order as
	// the chain transitions. Each edge retains its source file and key.
	Edges []ReferenceEdge
}

// ReferenceReport is the result of a reference precheck. It only inspects
// effective values, so cycles hidden by a later data source overwriting one
// of the values are not reported.
type ReferenceReport struct {
	// Edges lists every resolved reference between effective keys.
	Edges []ReferenceEdge
	// Missing lists references that resolve to no key at all.
	Missing []MissingReference
	// Cycles lists detected reference cycles.
	Cycles []ReferenceCycle
}

// ReferenceCheckError implements the error interface for a precheck that
// found missing references and/or cycles.
type ReferenceCheckError struct {
	Report *ReferenceReport
}

// Error returns a human readable summary of the detected problems.
func (err *ReferenceCheckError) Error() string {
	if err == nil || err.Report == nil {
		return "ini: value reference precheck failed"
	}
	r := err.Report
	var b strings.Builder
	if len(r.Cycles) > 0 {
		fmt.Fprintf(&b, "%d circular variable reference(s):", len(r.Cycles))
		for _, c := range r.Cycles {
			names := make([]string, len(c.Chain))
			for i, n := range c.Chain {
				names[i] = n.String()
			}
			b.WriteString("\n  " + strings.Join(names, " -> "))
		}
	}
	if len(r.Missing) > 0 {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%d undefined variable reference(s):", len(r.Missing))
		for _, m := range r.Missing {
			b.WriteString("\n  key " + m.Edge.From.String() + " references undefined variable %(" +
				m.Edge.Variable + ")s")
			if m.Edge.SourceFile != "" {
				b.WriteString(" (from " + m.Edge.SourceFile + "#" + m.Edge.SourceKey + ")")
			}
		}
	}
	return b.String()
}

type refNodeID struct {
	section string
	key     string
}

func nodeID(n ReferenceNode) refNodeID {
	return refNodeID{section: n.Section, key: n.Key}
}

// canonicalSection normalizes the internal name of the default section
// (DefaultSection, or its lowercase form in case-insensitive loads) to the
// empty string, which is how the default section is addressed publicly.
func (f *File) canonicalSection(name string) string {
	defName := DefaultSection
	if f.options.Insensitive || f.options.InsensitiveSections {
		defName = strings.ToLower(defName)
	}
	if name == defName {
		return ""
	}
	return name
}

// lookupKeyInSectionChain mirrors Section.GetKey: look the key up in the
// section itself, then successively in parent sections split by the child
// section delimiter. Unlike Section.GetKey it also returns the section that
// actually owns the key, which is needed to attribute graph edges.
func (f *File) lookupKeyInSectionChain(sec *Section, name string) (*Section, *Key) {
	keyName := name
	if f.options.Insensitive || f.options.InsensitiveKeys {
		keyName = strings.ToLower(keyName)
	}
	if key := sec.keys[keyName]; key != nil {
		return sec, key
	}

	sname := sec.name
	for {
		i := strings.LastIndex(sname, f.options.ChildSectionDelimiter)
		if i < 0 {
			break
		}
		sname = sname[:i]
		parent, err := f.GetSection(sname)
		if err != nil {
			continue
		}
		if key := parent.keys[keyName]; key != nil {
			return parent, key
		}
	}
	return nil, nil
}

// resolveRefKey mirrors the reference resolution performed while expanding
// values in Key.transformValue: first look up the variable in the same
// section (which walks parent/child sections via Section.GetKey), and when
// the key is missing or the key itself then fall back to the default
// section. Case-insensitive rules are inherited from the load options.
func (f *File) resolveRefKey(fromSec *Section, fromKey *Key, variable string) (*Section, *Key) {
	owner, nk := f.lookupKeyInSectionChain(fromSec, variable)
	if nk == nil || fromKey == nk {
		defSec, derr := f.GetSection("")
		if derr != nil {
			return nil, nil
		}
		owner, nk = f.lookupKeyInSectionChain(defSec, variable)
		if nk == nil {
			return nil, nil
		}
	}
	return owner, nk
}

// CheckValueReferences performs an explicit precheck of the "%(name)s"
// variable references contained in the effective values of the merged
// configuration, without expanding any value or modifying the file.
//
// The reference graph is built from the values that are actually in effect
// after loading all data sources in overwrite order, so a cycle broken by a
// later source is not reported. References are resolved with the same
// section inheritance, default-section fallback and case-insensitivity rules
// as reading the values. Missing references and cycles are diagnosed
// separately, and every edge retains the source file and key name of the
// value it was found in.
//
// It returns a nil error when no missing reference or cycle is detected.
func (f *File) CheckValueReferences() (*ReferenceReport, error) {
	if f.BlockMode {
		f.lock.RLock()
		defer f.lock.RUnlock()
	}

	report := &ReferenceReport{}

	type adjacency struct {
		to   refNodeID
		edge ReferenceEdge
	}
	graph := make(map[refNodeID][]adjacency)
	seenEdges := make(map[refNodeID]map[refNodeID]map[string]bool)
	seenMissing := make(map[refNodeID]map[string]bool)

	addEdge := func(e ReferenceEdge) {
		from := nodeID(e.From)
		targets, ok := seenEdges[from]
		if !ok {
			targets = make(map[refNodeID]map[string]bool)
			seenEdges[from] = targets
		}
		to := nodeID(e.To)
		vars, ok := targets[to]
		if !ok {
			vars = make(map[string]bool)
			targets[to] = vars
		}
		if vars[e.Variable] {
			return
		}
		vars[e.Variable] = true
		graph[from] = append(graph[from], adjacency{to: to, edge: e})
		report.Edges = append(report.Edges, e)
	}
	addMissing := func(e ReferenceEdge) {
		from := nodeID(e.From)
		vars, ok := seenMissing[from]
		if !ok {
			vars = make(map[string]bool)
			seenMissing[from] = vars
		}
		if vars[e.Variable] {
			return
		}
		vars[e.Variable] = true
		report.Missing = append(report.Missing, MissingReference{Edge: e})
	}

	// Iterate sections and their effective keys in declaration order, which
	// is also the order in which values effectively win across sources.
	for _, sec := range f.Sections() {
		for _, k := range sec.keys {
			from := ReferenceNode{Section: f.canonicalSection(sec.name), Key: k.name}
			raw := k.value
			if !strings.Contains(raw, "%") {
				continue
			}
			for _, match := range varPattern.FindAllStringSubmatch(raw, -1) {
				variable := match[1]
				edge := ReferenceEdge{
					From:       from,
					Variable:   variable,
					SourceFile: k.source,
					SourceKey:  k.name,
				}

				toSec, toKey := f.resolveRefKey(sec, k, variable)
				if toKey == nil {
					addMissing(edge)
					continue
				}
				edge.To = ReferenceNode{Section: f.canonicalSection(toSec.name), Key: toKey.name}
				addEdge(edge)
			}
		}
	}

	// Detect cycles with a depth-first traversal, recording the depth-first
	// order so back edges can be turned into cycle chains.
	const (
		colorWhite = 0
		colorGray  = 1
		colorBlack = 2
	)
	color := make(map[refNodeID]int)
	onStack := make(map[refNodeID]int)
	seenCycles := make(map[string]bool)

	var stack []refNodeID
	var stackEdges []ReferenceEdge
	var dfs func(u refNodeID)
	dfs = func(u refNodeID) {
		color[u] = colorGray
		onStack[u] = len(stack)
		stack = append(stack, u)

		for _, a := range graph[u] {
			v := a.to
			switch color[v] {
			case colorWhite:
				stackEdges = append(stackEdges, a.edge)
				dfs(v)
				stackEdges = stackEdges[:len(stackEdges)-1]
			case colorGray:
				start := onStack[v]
				chainNodes := append([]refNodeID(nil), stack[start:]...)
				chainNodes = append(chainNodes, v)
				cycleEdges := append([]ReferenceEdge(nil), stackEdges[start:]...)
				cycleEdges = append(cycleEdges, a.edge)

				key := cycleKey(chainNodes)
				if !seenCycles[key] {
					seenCycles[key] = true
					c := ReferenceCycle{
						Chain: make([]ReferenceNode, len(chainNodes)),
						Edges: cycleEdges,
					}
					for i, id := range chainNodes {
						c.Chain[i] = ReferenceNode{Section: id.section, Key: id.key}
					}
					report.Cycles = append(report.Cycles, c)
				}
			}
		}

		stack = stack[:len(stack)-1]
		delete(onStack, u)
		color[u] = colorBlack
	}

	// Start the traversal from every node so disconnected cycles are found.
	startNodes := make([]refNodeID, 0, len(graph))
	for u := range graph {
		startNodes = append(startNodes, u)
	}
	sort.Slice(startNodes, func(i, j int) bool {
		if startNodes[i].section != startNodes[j].section {
			return startNodes[i].section < startNodes[j].section
		}
		return startNodes[i].key < startNodes[j].key
	})
	for _, u := range startNodes {
		if color[u] == colorWhite {
			dfs(u)
		}
	}

	sort.Slice(report.Cycles, func(i, j int) bool {
		return cycleCompare(report.Cycles[i], report.Cycles[j])
	})
	sort.Slice(report.Missing, func(i, j int) bool {
		a, b := report.Missing[i].Edge, report.Missing[j].Edge
		if as, bs := a.From.String(), b.From.String(); as != bs {
			return as < bs
		}
		return a.Variable < b.Variable
	})
	sort.Slice(report.Edges, func(i, j int) bool {
		a, b := report.Edges[i], report.Edges[j]
		if as, bs := a.From.String(), b.From.String(); as != bs {
			return as < bs
		}
		if as, bs := a.To.String(), b.To.String(); as != bs {
			return as < bs
		}
		return a.Variable < b.Variable
	})

	if len(report.Cycles) > 0 || len(report.Missing) > 0 {
		return report, &ReferenceCheckError{Report: report}
	}
	return report, nil
}

// cycleKey renders a canonical identity of a cycle so the same cycle found
// from different traversal directions is only reported once.
func cycleKey(nodes []refNodeID) string {
	if len(nodes) <= 1 {
		return nodes[0].section + "\x00" + nodes[0].key
	}
	ring := nodes[:len(nodes)-1]

	// Find the lexicographically smallest rotation of the cycle.
	minStart := 0
	for i := 1; i < len(ring); i++ {
		for j := 0; j < len(ring); j++ {
			a, b := ring[(i+j)%len(ring)], ring[(minStart+j)%len(ring)]
			if a.section != b.section {
				if a.section < b.section {
					minStart = i
				}
				break
			}
			if a.key != b.key {
				if a.key < b.key {
					minStart = i
				}
				break
			}
		}
	}

	parts := make([]string, 0, len(ring)+1)
	for j := 0; j < len(ring); j++ {
		n := ring[(minStart+j)%len(ring)]
		parts = append(parts, n.section+"\x00"+n.key)
	}
	first := ring[minStart]
	parts = append(parts, first.section+"\x00"+first.key)
	return strings.Join(parts, "\n")
}

func cycleCompare(a, b ReferenceCycle) bool {
	for i := 0; i < len(a.Chain) && i < len(b.Chain); i++ {
		if as, bs := a.Chain[i].String(), b.Chain[i].String(); as != bs {
			return as < bs
		}
	}
	return len(a.Chain) < len(b.Chain)
}
