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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// threeSectionConfig is the canonical three-section example used across the
// provenance tests:
//
//	[server]      defines host and port
//	[server.api]  overrides port, inherits host
//	[server.api.v2] overrides nothing, inherits both
const threeSectionConfig = `
[server]
host     = 127.0.0.1
port     = 8080
password = s3cret

[server.api]
port = 9090

[server.api.v2]
debug = true
`

func loadThreeSections(t *testing.T) *File {
	t.Helper()
	f, err := LoadSources(LoadOptions{
		SensitiveKeys: []string{"password"},
	}, []byte(threeSectionConfig))
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	return f
}

// TestProvenanceChildOverridesParent verifies that when a child overrides a
// parent key the final source is the child section, and the override fact is
// retained in the chain.
func TestProvenanceChildOverridesParent(t *testing.T) {
	f := loadThreeSections(t)
	api := f.Section("server.api")

	p := api.Provenance("port")
	assert.Equal(t, "server.api", p.EffectiveSection)
	assert.Equal(t, "9090", p.EffectiveValue)
	assert.Equal(t, "server.api", p.Section)
	assert.Equal(t, "port", p.Key)

	// The chain keeps the parent value as an overridden first link and the
	// child value as the winning final link.
	assert.Len(t, p.Chain, 2)
	assert.Equal(t, "server", p.Chain[0].Section)
	assert.Equal(t, "8080", p.Chain[0].Value)
	assert.True(t, p.Chain[0].Overridden)
	win := p.Chain[len(p.Chain)-1]
	assert.Equal(t, "server.api", win.Section)
	assert.Equal(t, "port", win.Key)
	assert.Equal(t, "9090", win.Value)
	assert.Equal(t, "<byte-data>", win.Source)
	assert.Equal(t, 0, win.SourceIndex)
	assert.Equal(t, 8, win.Line)
	assert.False(t, win.Overridden)
	assert.False(t, win.Shadow)
	assert.Empty(t, p.InheritanceCycle)
}

// TestProvenanceInheritedPointsToParent verifies a non-overridden key points
// back to its parent section.
func TestProvenanceInheritedPointsToParent(t *testing.T) {
	f := loadThreeSections(t)
	api := f.Section("server.api")

	p := api.Provenance("host")
	assert.Equal(t, "server", p.EffectiveSection)
	assert.Equal(t, "127.0.0.1", p.EffectiveValue)
	assert.Len(t, p.Chain, 1)
	assert.Equal(t, "server", p.Chain[0].Section)
	assert.Equal(t, 3, p.Chain[0].Line)

	// Grandchild inherits from grandparent through the dotted chain.
	v2 := f.Section("server.api.v2")
	p2 := v2.Provenance("host")
	assert.Equal(t, "server", p2.EffectiveSection)
	p3 := v2.Provenance("port")
	assert.Equal(t, "server.api", p3.EffectiveSection)
	assert.Equal(t, "9090", p3.EffectiveValue)
	// The grandparent value remains the oldest, overridden link.
	assert.Len(t, p3.Chain, 2)
	assert.Equal(t, "server", p3.Chain[0].Section)
	assert.Equal(t, "8080", p3.Chain[0].Value)
	assert.True(t, p3.Chain[0].Overridden)
	assert.Equal(t, "server.api", p3.Chain[1].Section)
	assert.False(t, p3.Chain[1].Overridden)

	// A key defined in the section itself stays local.
	p4 := v2.Provenance("debug")
	assert.Equal(t, "server.api.v2", p4.EffectiveSection)
	assert.Equal(t, "true", p4.EffectiveValue)
}

// TestProvenanceSensitiveKeyRedacted verifies a sensitive key exposes its
// source and override facts but never its value.
func TestProvenanceSensitiveKeyRedacted(t *testing.T) {
	f := loadThreeSections(t)

	// Normal Get behavior is unchanged: the real value is still readable.
	assert.Equal(t, "s3cret", f.Section("server").Key("password").String())

	p := f.Section("server").Provenance("password")
	assert.True(t, p.Sensitive)
	assert.Equal(t, "server", p.EffectiveSection)
	assert.Equal(t, "", p.EffectiveValue)
	assert.Len(t, p.Chain, 1)
	assert.Equal(t, "server", p.Chain[0].Section)
	assert.True(t, p.Chain[0].Sensitive)
	assert.Equal(t, "", p.Chain[0].Value)
	assert.Equal(t, 5, p.Chain[0].Line)

	// Inherited sensitive key stays redacted.
	p2 := f.Section("server.api").Provenance("password")
	assert.True(t, p2.Sensitive)
	assert.Equal(t, "server", p2.EffectiveSection)
	assert.Equal(t, "", p2.EffectiveValue)

	// Runtime marking works too.
	f.MarkSensitive("host")
	p3 := f.Section("server.api").Provenance("host")
	assert.True(t, p3.Sensitive)
	assert.Equal(t, "", p3.EffectiveValue)
	assert.Equal(t, "", p3.Chain[0].Value)
	assert.Equal(t, "127.0.0.1", f.Section("server.api").Key("host").String())
}

// TestProvenanceDuplicateKeyOrder verifies repeated same-key definitions keep
// their override order and mark earlier values as overridden.
func TestProvenanceDuplicateKeyOrder(t *testing.T) {
	cfg := `
[db]
port = 1
port = 2
port = 3
`
	f, err := Load([]byte(cfg))
	if err != nil {
		t.Fatalf("failed to load: %v", err)
	}

	// Default Get behavior keeps the last value.
	assert.Equal(t, "3", f.Section("db").Key("port").String())

	p := f.Section("db").Provenance("port")
	assert.Equal(t, "3", p.EffectiveValue)
	assert.Len(t, p.Chain, 3)
	assert.Equal(t, "1", p.Chain[0].Value)
	assert.True(t, p.Chain[0].Overridden)
	assert.Equal(t, 3, p.Chain[0].Line)
	assert.Equal(t, "2", p.Chain[1].Value)
	assert.True(t, p.Chain[1].Overridden)
	assert.Equal(t, "3", p.Chain[2].Value)
	assert.False(t, p.Chain[2].Overridden)
}

// TestProvenanceMultipleSources verifies later data sources override earlier
// ones and the whole override history is retained.
func TestProvenanceMultipleSources(t *testing.T) {
	base := []byte("[app]\nport = 8000\nhost = localhost\n")
	override := []byte("[app]\nport = 9000\n")

	f, err := Load(base, override)
	if err != nil {
		t.Fatalf("failed to load: %v", err)
	}

	p := f.Section("app").Provenance("port")
	assert.Equal(t, "9000", p.EffectiveValue)
	assert.Len(t, p.Chain, 2)
	assert.Equal(t, "8000", p.Chain[0].Value)
	assert.Equal(t, 0, p.Chain[0].SourceIndex)
	assert.True(t, p.Chain[0].Overridden)
	assert.Equal(t, "9000", p.Chain[1].Value)
	assert.Equal(t, 1, p.Chain[1].SourceIndex)
	assert.False(t, p.Chain[1].Overridden)

	// Un-overridden key only has its single source.
	p2 := f.Section("app").Provenance("host")
	assert.Len(t, p2.Chain, 1)
	assert.Equal(t, "localhost", p2.Chain[0].Value)
}

// TestProvenanceShadows verifies shadow values are queryable separately and
// are never mixed into the effective value.
func TestProvenanceShadows(t *testing.T) {
	cfg := `
[cache]
server = a
server = b
server = c
`
	f, err := ShadowLoad([]byte(cfg))
	if err != nil {
		t.Fatalf("failed to load: %v", err)
	}

	// Effective value is still the first one; shadows do not override.
	assert.Equal(t, "a", f.Section("cache").Key("server").String())

	p := f.Section("cache").Provenance("server")
	assert.Equal(t, "a", p.EffectiveValue)
	assert.Len(t, p.Chain, 1)
	assert.False(t, p.Chain[0].Shadow)
	assert.Len(t, p.Shadows, 2)
	assert.Equal(t, "b", p.Shadows[0].Value)
	assert.True(t, p.Shadows[0].Shadow)
	assert.Equal(t, 4, p.Shadows[0].Line)
	assert.Equal(t, "c", p.Shadows[1].Value)
	assert.True(t, p.Shadows[1].Shadow)
}

// TestProvenanceDefault verifies the default value is recorded as the final
// source only when the key is undefined.
func TestProvenanceDefault(t *testing.T) {
	f := loadThreeSections(t)

	p := f.Section("server").ProvenanceWithDefault("timeout", "30s")
	assert.True(t, p.HasDefault)
	assert.Equal(t, "30s", p.EffectiveValue)
	assert.Len(t, p.Chain, 1)
	assert.Equal(t, "30s", p.Chain[0].Value)
	assert.Equal(t, 0, p.Chain[0].Line)

	// Provenance without default reports no chain for missing keys.
	p2 := f.Section("server").Provenance("timeout")
	assert.False(t, p2.HasDefault)
	assert.Empty(t, p2.Chain)
	assert.Equal(t, "", p2.EffectiveSection)

	// Defined key ignores the default.
	p3 := f.Section("server").ProvenanceWithDefault("port", "1")
	assert.False(t, p3.HasDefault)
	assert.Equal(t, "8080", p3.EffectiveValue)

	// Sensitive + default never leaks the default value.
	f.MarkSensitive("password-token")
	p4 := f.Section("server").ProvenanceWithDefault("password-token", "hidden-default")
	assert.True(t, p4.HasDefault)
	assert.True(t, p4.Sensitive)
	assert.Equal(t, "", p4.EffectiveValue)
	assert.Equal(t, "", p4.Chain[0].Value)
}

// TestProvenanceExplicitParentAndCycle verifies explicit parent declarations
// and that a cyclic inheritance is reported as a separate chain.
func TestProvenanceExplicitParentAndCycle(t *testing.T) {
	cfg := `
[base]
port = 7000
parent = alpha

[alpha]
parent = beta

[beta]
parent = base
`
	f, err := LoadSources(LoadOptions{
		ParentSectionKey: "parent",
	}, []byte(cfg))
	if err != nil {
		t.Fatalf("failed to load: %v", err)
	}

	p := f.Section("alpha").Provenance("port")
	// Value is inherited through the explicit parent graph
	// alpha -> beta -> base, while the graph closes a cycle.
	assert.Equal(t, "base", p.EffectiveSection)
	assert.Equal(t, "7000", p.EffectiveValue)
	assert.NotEmpty(t, p.InheritanceCycle)

	// The cycle is reported as section->parent links.
	found := false
	for _, link := range p.InheritanceCycle {
		if link.Section == "base" && link.Parent == "alpha" {
			found = true
		}
	}
	assert.True(t, found)

	// A non-cyclic explicit parent inherits normally with no cycle.
	cfg2 := []byte("[base]\nport = 7000\n\n[alpha]\nparent = base\n")
	f2, err := LoadSources(LoadOptions{ParentSectionKey: "parent"}, cfg2)
	if err != nil {
		t.Fatalf("failed to load: %v", err)
	}
	p2 := f2.Section("alpha").Provenance("port")
	assert.Equal(t, "base", p2.EffectiveSection)
	assert.Empty(t, p2.InheritanceCycle)
}

// TestProvenanceFileSource verifies origins loaded from a real file carry the
// file name and the correct physical line number.
func TestProvenanceFileSource(t *testing.T) {
	f, err := Load("testdata/full.ini")
	if err != nil {
		t.Fatalf("failed to load file: %v", err)
	}

	p := f.Section("author").Provenance("NAME")
	assert.Len(t, p.Chain, 1)
	o := p.Chain[0]
	assert.Equal(t, "testdata/full.ini", o.Source)
	assert.Equal(t, 0, o.SourceIndex)
	assert.Equal(t, 11, o.Line)
	assert.Equal(t, "Unknwon", o.Value)
	assert.False(t, o.Overridden)
}

// TestProvenanceLineAfterContinuation verifies the recorded line number for a
// value spanning continuation lines reflects its last physical line, while
// ordinary keys on their own single line are exact.
func TestProvenanceLineAfterContinuation(t *testing.T) {
	cfg := "[a]\nk1 = v1\nk2 = part1 \\\n  part2 \\\n  part3\n"
	f, err := Load([]byte(cfg))
	if err != nil {
		t.Fatalf("failed to load: %v", err)
	}
	assert.Equal(t, 2, f.Section("a").Provenance("k1").Chain[0].Line)
	assert.Equal(t, 5, f.Section("a").Provenance("k2").Chain[0].Line)
	assert.Equal(t, "part1 part2 part3", f.Section("a").Key("k2").String())
}

// TestProvenanceInsensitive verifies sensitive key names and lookups honor
// case-insensitive options and that a streamed source is named "<reader>".
func TestProvenanceInsensitive(t *testing.T) {
	cfg := `
[s]
Password = hush
PORT = 42
[s.c]
port = 43
`
	f, err := LoadSources(LoadOptions{
		Insensitive:   true,
		SensitiveKeys: []string{"PASSWORD"},
	}, strings.NewReader(cfg))
	if err != nil {
		t.Fatalf("failed to load: %v", err)
	}

	p := f.Section("s.c").Provenance("Port")
	assert.Equal(t, "s.c", p.EffectiveSection)
	assert.Equal(t, "43", p.EffectiveValue)
	assert.Equal(t, "<reader>", p.Chain[0].Source)
	assert.Equal(t, 0, p.Chain[0].SourceIndex)

	sp := f.Section("s").Provenance("password")
	assert.True(t, sp.Sensitive)
	assert.Equal(t, "", sp.EffectiveValue)
	// Normal Get still returns the secret.
	assert.Equal(t, "hush", f.Section("s").Key("password").String())
}

// TestFileKeyProvenance verifies the file-level helper resolves keys and does
// not mutate parsed state for missing sections or keys.
func TestFileKeyProvenance(t *testing.T) {
	f := loadThreeSections(t)

	p := f.KeyProvenance("server.api", "port")
	assert.Equal(t, "server.api", p.EffectiveSection)
	assert.Equal(t, "9090", p.EffectiveValue)

	before := f.SectionStrings()
	missing := f.KeyProvenance("does.not.exist", "port")
	assert.Empty(t, missing.Chain)
	assert.Equal(t, "", missing.EffectiveSection)
	// No new section was created as a side effect.
	assert.Equal(t, before, f.SectionStrings())

	// Requesting a missing key does not insert it.
	apiBefore := f.Section("server.api").KeyStrings()
	_ = f.KeyProvenance("server.api", "nope")
	assert.Equal(t, apiBefore, f.Section("server.api").KeyStrings())

	// Empty section name resolves to the default section.
	f2, err := Load([]byte("name = ini\n"))
	if err != nil {
		t.Fatalf("failed to load: %v", err)
	}
	d := f2.KeyProvenance("", "name")
	assert.Equal(t, DefaultSection, d.EffectiveSection)
	assert.Equal(t, "ini", d.EffectiveValue)
}

// TestProvenanceDoesNotReload verifies provenance is computed from parsed
// state only and that Get semantics are untouched.
func TestProvenanceDoesNotReload(t *testing.T) {
	f := loadThreeSections(t)

	// Fetching provenance repeatedly is stable and side-effect free.
	first := f.Section("server.api").Provenance("port")
	second := f.Section("server.api").Provenance("port")
	assert.Equal(t, first.EffectiveValue, second.EffectiveValue)
	assert.Equal(t, first.Chain[0].Line, second.Chain[0].Line)

	// Get/Key behavior is unchanged.
	assert.Equal(t, 9090, f.Section("server.api").Key("port").MustInt())
	assert.Equal(t, "127.0.0.1", f.Section("server.api").Key("host").String())
}
