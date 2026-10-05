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

// ValueOrigin describes where a single value of a key was defined. It is
// captured while parsing and never triggers a file reload or an environment
// read.
type ValueOrigin struct {
	// Section is the section in which the value was literally defined.
	Section string
	// Key is the (normalized) name of the key.
	Key string
	// Source is the name of the data source that supplied the value: the
	// file name for file sources, "<byte-data>" for in-memory data and
	// "<reader>" for streamed data.
	Source string
	// SourceIndex is the zero-based index of the data source, i.e. the load
	// order passed to Load/LoadSources. Later sources override earlier ones.
	SourceIndex int
	// Line is the 1-based physical line number of the value inside its data
	// source, or 0 when the value was not parsed from a data source (e.g. it
	// was created programmatically).
	Line int
	// Value is the raw value as defined at this origin. It is always empty
	// for sensitive keys so that secrets cannot be leaked through the
	// provenance API.
	Value string
	// Sensitive reports whether the key was marked as sensitive.
	Sensitive bool
	// Shadow reports whether this origin belongs to a shadow (duplicate)
	// value. Effective values never come from shadow origins.
	Shadow bool
	// Overridden reports whether a later same-key definition replaced this
	// value, either in a later data source or by a later line in the same
	// source.
	Overridden bool
}

// KeyProvenance explains where the effective value of a key comes from.
//
// The chain lists the necessary same-key origins in definition order, from
// the first definition to the winning one. Duplicate keys overridden within
// the same section are reported as overridden, while shadow values are exposed
// separately and never mixed into the effective value.
type KeyProvenance struct {
	// Section is the section from which the explanation was requested.
	Section string
	// Key is the (normalized) name of the key.
	Key string
	// EffectiveSection is the section that supplies the effective value:
	// either the queried section itself or an ancestor section.
	EffectiveSection string
	// EffectiveValue is the final raw value. It is empty for sensitive keys.
	EffectiveValue string
	// Sensitive reports whether the key was marked as sensitive.
	Sensitive bool
	// HasDefault reports whether the effective value comes from the caller
	// supplied default instead of a parsed definition.
	HasDefault bool
	// Chain is the necessary source chain of the final value, oldest first.
	// The last entry describes the winning origin. Values of sensitive keys
	// are redacted everywhere in the chain.
	Chain []ValueOrigin
	// Shadows lists shadow (duplicate) value origins when shadows are
	// allowed. They are queryable but never part of the effective value.
	Shadows []ValueOrigin
	// InheritanceCycle reports a cyclic inheritance chain, if any, that was
	// encountered while resolving the section.
	InheritanceCycle []InheritanceLink
}

// InheritanceLink is one edge of a cyclic section inheritance chain.
type InheritanceLink struct {
	// Section is the section declaring Parent as its parent.
	Section string
	// Parent is the declared parent section.
	Parent string
}

// currentSourceName returns a stable display name for the data source that is
// being parsed.
func (f *File) currentSourceName() string {
	if f.curSource < 0 || f.curSource >= len(f.dataSources) {
		return ""
	}
	switch s := f.dataSources[f.curSource].(type) {
	case sourceFile:
		return s.name
	case *sourceData:
		return "<byte-data>"
	case *sourceReadCloser:
		return "<reader>"
	}
	return ""
}

// currentOrigin builds an origin for a value that is being parsed right now.
func (f *File) currentOrigin(section, key string) ValueOrigin {
	return ValueOrigin{
		Section:     section,
		Key:         key,
		Source:      f.currentSourceName(),
		SourceIndex: f.curSource,
		Line:        f.curLine,
	}
}

// normalizeKey applies the same key case rules used while parsing.
func (f *File) normalizeKey(name string) string {
	if f.options.Insensitive || f.options.InsensitiveKeys {
		return strings.ToLower(name)
	}
	return name
}

// MarkSensitive marks one or more key names as sensitive. Sensitive keys only
// expose their source and override facts, never their values.
func (f *File) MarkSensitive(names ...string) {
	if f.sensitiveKeys == nil {
		f.sensitiveKeys = make(map[string]struct{}, len(names))
	}
	for _, name := range names {
		f.sensitiveKeys[f.normalizeKey(name)] = struct{}{}
	}
}

// IsSensitive reports whether the key name is marked sensitive.
func (f *File) IsSensitive(name string) bool {
	_, ok := f.sensitiveKeys[f.normalizeKey(name)]
	return ok
}

// directParents returns the explicitly declared parents of a section via the
// configured inheritance key (if any), followed by dot-separated ancestors.
func (f *File) directParents(name string) []string {
	seen := map[string]bool{}
	parents := make([]string, 0, 2)

	add := func(parent string) {
		parent = strings.TrimSpace(parent)
		if parent == "" || parent == name || seen[parent] {
			return
		}
		// Only follow parents that actually exist; an unknown parent name is
		// ignored the same way GetKey ignores missing ancestor sections.
		if _, err := f.GetSection(parent); err != nil {
			return
		}
		seen[parent] = true
		parents = append(parents, parent)
	}

	if inheritKey := f.options.ParentSectionKey; inheritKey != "" {
		if sec, err := f.GetSection(name); err == nil {
			if k, err := sec.getKeyDirect(inheritKey); err == nil {
				add(k.value)
			}
		}
	}

	delim := f.options.ChildSectionDelimiter
	if delim == "" {
		delim = "."
	}
	sname := name
	for {
		i := strings.LastIndex(sname, delim)
		if i < 0 {
			break
		}
		sname = sname[:i]
		add(sname)
	}
	return parents
}

// inheritanceDepthLimit guards against pathological inheritance graphs.
const inheritanceDepthLimit = 10000

// resolveInheritance returns sections in nearest-first resolution order
// starting with name itself, plus any detected inheritance cycle.
func (f *File) resolveInheritance(name string) (chain []*Section, cycle []InheritanceLink) {
	if start, err := f.GetSection(name); err == nil {
		chain = append(chain, start)
	}
	visited := map[string]bool{name: true}

	// Stack holds sections still to expand, each with the next parent index.
	type frame struct {
		name    string
		parents []string
		idx     int
	}
	stack := []frame{{name: name, parents: f.directParents(name)}}

	for len(stack) > 0 && len(chain) <= inheritanceDepthLimit {
		top := &stack[len(stack)-1]
		if top.idx >= len(top.parents) {
			stack = stack[:len(stack)-1]
			continue
		}
		parent := top.parents[top.idx]
		top.idx++

		if visited[parent] {
			cycle = append(cycle, InheritanceLink{Section: top.name, Parent: parent})
			continue
		}
		visited[parent] = true
		if sec, err := f.GetSection(parent); err == nil {
			chain = append(chain, sec)
		}
		stack = append(stack, frame{name: parent, parents: f.directParents(parent)})
	}
	return chain, cycle
}

// getKeyDirect returns a key literally defined in the section without walking
// parent sections.
func (s *Section) getKeyDirect(name string) (*Key, error) {
	if s.f.options.Insensitive || s.f.options.InsensitiveKeys {
		name = strings.ToLower(name)
	}
	if k := s.keys[name]; k != nil {
		return k, nil
	}
	return nil, fmt.Errorf("error when getting key of section %q: key %q not exists", s.name, name)
}

// originForKey converts the parsed history of a key into provenance origins.
func originForKey(k *Key, sensitive, winning bool) []ValueOrigin {
	if k == nil {
		return nil
	}
	// A key created programmatically (never parsed) has no recorded origin;
	// still surface a single, sourceless link so the chain is not empty.
	if len(k.origins) == 0 {
		o := ValueOrigin{Section: k.s.name, Key: k.name}
		if !sensitive {
			o.Value = k.value
		}
		o.Sensitive = sensitive
		o.Shadow = k.isShadow
		return []ValueOrigin{o}
	}
	out := make([]ValueOrigin, 0, len(k.origins))
	for i := range k.origins {
		o := k.origins[i]
		o.Key = k.name
		o.Sensitive = sensitive
		o.Shadow = k.isShadow
		// Within a winning key's history every entry except the final one
		// was overridden by a later same-key definition.
		o.Overridden = winning && i < len(k.origins)-1
		if sensitive {
			o.Value = ""
		}
		out = append(out, o)
	}
	return out
}

// buildProvenance resolves the effective key of a section and explains it.
func (s *Section) buildProvenance(keyName string, hasDefault bool) *KeyProvenance {
	normName := s.f.normalizeKey(keyName)
	sensitive := s.f.IsSensitive(normName)

	p := &KeyProvenance{
		Section:   s.name,
		Key:       normName,
		Sensitive: sensitive,
	}

	sections, cycle := s.f.resolveInheritance(s.name)
	p.InheritanceCycle = cycle

	for idx, sec := range sections {
		k := sec.keys[normName]
		if k == nil || k.isSynthetic {
			continue
		}

		// The first section in resolution order that literally defines the
		// key wins. Its same-key history is the core of the chain.
		p.EffectiveSection = sec.name
		p.Chain = originForKey(k, sensitive, true)
		if len(p.Chain) > 0 {
			p.Chain[len(p.Chain)-1].Overridden = false
		}
		if !sensitive {
			p.EffectiveValue = k.value
		}

		// Same-key definitions in farther ancestors were overridden by the
		// winning section, so prepend them as overridden links to form the
		// complete necessary source chain.
		var inherited []ValueOrigin
		for j := idx + 1; j < len(sections); j++ {
			if ak := sections[j].keys[normName]; ak != nil && !ak.isSynthetic {
				links := originForKey(ak, sensitive, false)
				for i := range links {
					links[i].Overridden = true
				}
				inherited = append(inherited, links...)
			}
		}
		if len(inherited) > 0 {
			p.Chain = append(inherited, p.Chain...)
		}

		// Shadow values of the winning key are queryable but stay separate.
		for _, sh := range k.shadows {
			p.Shadows = append(p.Shadows, originForKey(sh, sensitive, false)...)
		}
		return p
	}

	// The key is not defined anywhere along the inheritance chain.
	if hasDefault {
		p.HasDefault = true
		p.EffectiveSection = s.name
		p.Chain = []ValueOrigin{{
			Section:   s.name,
			Key:       normName,
			Sensitive: sensitive,
		}}
	}
	return p
}

// Provenance returns the provenance explanation of a key resolved by the
// section. It only inspects the already parsed state: it never reloads files,
// reads environment variables or changes Get behavior.
func (s *Section) Provenance(keyName string) *KeyProvenance {
	return s.buildProvenance(keyName, false)
}

// ProvenanceWithDefault is like Provenance but records defaultVal as the
// final source when the key has no parsed definition. Sensitive keys never
// expose the default value.
func (s *Section) ProvenanceWithDefault(keyName, defaultVal string) *KeyProvenance {
	p := s.buildProvenance(keyName, true)
	if p.HasDefault && len(p.Chain) > 0 && !p.Sensitive {
		p.Chain[0].Value = defaultVal
		p.EffectiveValue = defaultVal
	}
	return p
}

// KeyProvenance returns the provenance explanation of a key of the file by
// section and key name. It never creates a missing section or key in the file.
func (f *File) KeyProvenance(section, key string) *KeyProvenance {
	sec, err := f.GetSection(section)
	if err != nil {
		// Resolve against a transient section so the parsed state is not
		// mutated: the only possible outcome here is "not defined".
		name := section
		if name == "" {
			name = DefaultSection
		}
		if f.options.Insensitive || f.options.InsensitiveSections {
			name = strings.ToLower(name)
		}
		sec = newSection(f, name)
	}
	return sec.Provenance(key)
}
