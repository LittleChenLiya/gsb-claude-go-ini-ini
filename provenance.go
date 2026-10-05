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

// Provenance describes the source chain of an effective configuration value.
// It explains where a value came from without reloading files or reading the
// environment. Shadow (duplicate) values are recorded separately and are never
// mixed into the effective value.
type Provenance struct {
	// Section is the name of the section that provides the effective value.
	Section string
	// Key is the name of the key.
	Key string
	// Value is the effective raw value. It is empty when Sensitive is true.
	Value string
	// Sensitive reports whether the key is marked as sensitive.
	Sensitive bool
	// Overridden reports whether a nearer section overrode an inherited value.
	Overridden bool
	// Shadows holds shadow (duplicate) values that were not mixed into the
	// effective value. Their order reflects the order in which they were added.
	Shadows []*Provenance
	// Chain is the ordered list of section names consulted while resolving the
	// value, starting from the target section and ending at the root section
	// that provided the value.
	Chain []string
}

// SectionProvenance describes the provenance of all effective keys in a section.
type SectionProvenance struct {
	// Section is the name of the section.
	Section string
	// Keys is the provenance of each effective key, in key order.
	Keys []*Provenance
	// Circular reports a circular inheritance chain if one was detected.
	// When non-empty, the chain lists the section names that form the cycle.
	Circular []string
}

// isSensitive reports whether the given key name matches any sensitive pattern.
// Matching is case-insensitive.
func isSensitive(patterns []string, name string) bool {
	for _, p := range patterns {
		if strings.EqualFold(p, name) {
			return true
		}
	}
	return false
}

// inheritanceChain returns the chain of sections from s up to the root
// (DEFAULT section), following parent-child section inheritance. It also
// reports whether a circular inheritance chain was detected.
func (s *Section) inheritanceChain() ([]*Section, bool) {
	chain := []*Section{s}
	visited := map[string]bool{s.name: true}

	sname := s.name
	for {
		if i := strings.LastIndex(sname, s.f.options.ChildSectionDelimiter); i > -1 {
			sname = sname[:i]
			if sname == "" {
				break
			}
			sec, err := s.f.GetSection(sname)
			if err != nil {
				continue
			}
			if visited[sname] {
				return chain, true
			}
			visited[sname] = true
			chain = append(chain, sec)
		} else {
			break
		}
	}

	// Add DEFAULT section as the root if it exists and has not been visited.
	if s.f.HasSection(DefaultSection) {
		def, _ := s.f.GetSection(DefaultSection)
		if def != nil && !visited[def.name] {
			chain = append(chain, def)
		}
	}

	return chain, false
}

// chainNames returns the names of the sections in the chain.
func chainNames(chain []*Section) []string {
	names := make([]string, len(chain))
	for i, sec := range chain {
		names[i] = sec.name
	}
	return names
}

// keyProvenance computes the provenance of a single effective key given the
// full inheritance chain.
func (s *Section) keyProvenance(key *Key, chain []*Section) *Provenance {
	p := &Provenance{
		Key:   key.Name(),
		Chain: chainNames(chain),
	}

	// Find the first section in the chain that has this key in its own keys.
	var source *Section
	count := 0
	for _, sec := range chain {
		if _, ok := sec.keys[key.Name()]; ok {
			if source == nil {
				source = sec
			}
			count++
		}
	}

	if source != nil {
		p.Section = source.name
		p.Overridden = count > 1
		p.Sensitive = isSensitive(s.f.options.SensitiveKeys, key.Name())
		if p.Sensitive {
			p.Value = ""
		} else {
			p.Value = source.Key(key.Name()).Value()
		}
	}

	// Record shadow values, preserving their order.
	for _, sh := range key.shadows {
		sp := &Provenance{
			Section:   p.Section,
			Key:       sh.Name(),
			Sensitive: p.Sensitive,
			Chain:     p.Chain,
		}
		if sp.Sensitive {
			sp.Value = ""
		} else {
			sp.Value = sh.Value()
		}
		p.Shadows = append(p.Shadows, sp)
	}

	return p
}

// Provenance returns the source explanation for every effective key in the
// section. It only reads the parsed in-memory state; it does not reload files
// or read the environment. Shadow values are queryable but are never mixed
// into the effective value.
func (s *Section) Provenance() *SectionProvenance {
	sp := &SectionProvenance{
		Section: s.name,
		Keys:    make([]*Provenance, 0, len(s.keyList)),
	}

	// Build the full inheritance chain.
	chain, circular := s.inheritanceChain()
	if circular {
		sp.Circular = chainNames(chain)
	}

	// Collect all effective keys in order: the section's own keys first,
	// then inherited keys from parent sections, then DEFAULT keys.
	seen := make(map[string]bool)
	var effectiveKeys []*Key
	for _, sec := range chain {
		for _, k := range sec.Keys() {
			if !seen[k.Name()] {
				seen[k.Name()] = true
				effectiveKeys = append(effectiveKeys, k)
			}
		}
	}

	// Compute provenance for each effective key.
	for _, key := range effectiveKeys {
		p := s.keyProvenance(key, chain)
		sp.Keys = append(sp.Keys, p)
	}

	return sp
}

// Provenance returns the source explanation for every effective key in the
// file. It only reads the parsed in-memory state; it does not reload files or
// read the environment.
func (f *File) Provenance() []*SectionProvenance {
	result := make([]*SectionProvenance, 0, len(f.sectionList))
	for i, name := range f.sectionList {
		sec := f.SectionWithIndex(name, f.sectionIndexes[i])
		result = append(result, sec.Provenance())
	}
	return result
}

// String returns a human-readable representation of the provenance.
func (p *Provenance) String() string {
	var sb strings.Builder
	sb.WriteString("  ")
	sb.WriteString(p.Key)
	if p.Sensitive {
		sb.WriteString(" = <redacted>")
	} else {
		sb.WriteString(fmt.Sprintf(" = %q", p.Value))
	}
	sb.WriteString(fmt.Sprintf("  [source: %s]", p.Section))
	if p.Overridden {
		sb.WriteString(" (overridden)")
	}
	if len(p.Chain) > 1 {
		sb.WriteString(fmt.Sprintf("  chain: %s", strings.Join(p.Chain, " -> ")))
	}
	for _, sh := range p.Shadows {
		sb.WriteString("\n    shadow: ")
		sb.WriteString(sh.Key)
		if sh.Sensitive {
			sb.WriteString(" = <redacted>")
		} else {
			sb.WriteString(fmt.Sprintf(" = %q", sh.Value))
		}
	}
	return sb.String()
}

// String returns a human-readable representation of the section provenance.
func (sp *SectionProvenance) String() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[%s]\n", sp.Section))
	for _, p := range sp.Keys {
		sb.WriteString(p.String())
		sb.WriteString("\n")
	}
	if len(sp.Circular) > 0 {
		sb.WriteString(fmt.Sprintf("  circular inheritance: %s\n", strings.Join(sp.Circular, " -> ")))
	}
	return sb.String()
}
