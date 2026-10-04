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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	preflightCycleConf    = "testdata/preflight/cycle.ini"
	preflightOverrideConf = "testdata/preflight/override.ini"
	preflightMissingConf  = "testdata/preflight/missing.ini"
)

func preflightIssues(t *testing.T, f *File) []PreflightIssue {
	t.Helper()
	err := f.Preflight()
	if err == nil {
		return nil
	}
	pe, ok := err.(*PreflightError)
	require.True(t, ok, "Preflight must return *PreflightError, got %T", err)
	return pe.Issues
}

func TestPreflight_Cycle(t *testing.T) {
	f, err := Load(preflightCycleConf)
	require.NoError(t, err)

	issues := preflightIssues(t, f)
	require.Len(t, issues, 1)
	issue := issues[0]
	assert.Equal(t, PreflightIssueCycle, issue.Kind)

	// The chain must be a -> b -> a, with every edge attributed to the
	// file the effective value came from.
	require.Len(t, issue.Edges, 2)
	assert.Equal(t, "a", issue.Edges[0].Key)
	assert.Equal(t, "b", issue.Edges[0].Ref)
	assert.Equal(t, "b", issue.Edges[1].Key)
	assert.Equal(t, "a", issue.Edges[1].Ref)
	for _, e := range issue.Edges {
		assert.Equal(t, preflightCycleConf, e.Source)
		assert.Equal(t, DefaultSection, e.Section)
	}
	chain := issue.Error()
	assert.Contains(t, chain, "[DEFAULT] a")
	assert.Contains(t, chain, "[DEFAULT] b")
}

func TestPreflight_OverwrittenCycle(t *testing.T) {
	// The first layer defines a <-> b, the second layer overwrites b
	// with a literal. The cycle must not be reported, and the default
	// read behavior must stay unchanged.
	f, err := Load(preflightCycleConf, preflightOverrideConf)
	require.NoError(t, err)

	assert.NoError(t, f.Preflight())
	assert.Equal(t, "ok", f.Section(DefaultSection).Key("a").String())
	assert.Equal(t, "ok", f.Section(DefaultSection).Key("b").String())
}

func TestPreflight_Undefined(t *testing.T) {
	f, err := Load(preflightMissingConf)
	require.NoError(t, err)

	issues := preflightIssues(t, f)
	require.Len(t, issues, 1)
	issue := issues[0]
	assert.Equal(t, PreflightIssueUndefined, issue.Kind)
	require.Len(t, issue.Edges, 1)
	edge := issue.Edges[0]
	assert.Equal(t, "a", edge.Key)
	assert.Equal(t, "c", edge.Ref)
	assert.Equal(t, preflightMissingConf, edge.Source)
	assert.Contains(t, issue.Error(), "%(c)s")
}

func TestPreflight_UndefinedAndCycleSeparated(t *testing.T) {
	// One file with a cycle and one dangling reference: the two kinds
	// must be reported separately.
	f, err := Load([]byte(`
a=%(b)s
b=%(a)s
x=%(y)s
`))
	require.NoError(t, err)

	issues := preflightIssues(t, f)
	var cycles, undefined int
	for _, issue := range issues {
		switch issue.Kind {
		case PreflightIssueCycle:
			cycles++
		case PreflightIssueUndefined:
			undefined++
			require.Len(t, issue.Edges, 1)
			assert.Equal(t, "x", issue.Edges[0].Key)
			assert.Equal(t, "y", issue.Edges[0].Ref)
		}
	}
	assert.Equal(t, 1, cycles)
	assert.Equal(t, 1, undefined)
}

func TestPreflight_Insensitive(t *testing.T) {
	t.Run("case-insensitive resolution", func(t *testing.T) {
		f, err := InsensitiveLoad([]byte("a=%(B)s\nb=ok\n"))
		require.NoError(t, err)
		assert.NoError(t, f.Preflight())
		// Read behavior is unchanged.
		assert.Equal(t, "ok", f.Section(DefaultSection).Key("a").String())
	})

	t.Run("case-sensitive resolution leaves dangling reference", func(t *testing.T) {
		f, err := Load([]byte("a=%(B)s\nb=ok\n"))
		require.NoError(t, err)
		issues := preflightIssues(t, f)
		require.Len(t, issues, 1)
		assert.Equal(t, PreflightIssueUndefined, issues[0].Kind)
		assert.Equal(t, "B", issues[0].Edges[0].Ref)
	})

	t.Run("insensitive sections and keys", func(t *testing.T) {
		f, err := LoadSources(LoadOptions{Insensitive: true}, []byte(`
[Env]
Home=%(HOME)s
[env]
home=/tmp
`))
		require.NoError(t, err)
		assert.NoError(t, f.Preflight())
		assert.Equal(t, "/tmp", f.Section("env").Key("home").String())
	})
}

func TestPreflight_SectionInheritance(t *testing.T) {
	t.Run("resolves through parent section like Get", func(t *testing.T) {
		f, err := Load([]byte(`
[parent]
dir=/base
[parent.child]
file=%(dir)s/etc
`))
		require.NoError(t, err)
		assert.NoError(t, f.Preflight())
		assert.Equal(t, "/base/etc", f.Section("parent.child").Key("file").String())
	})

	t.Run("cycle spanning parent and child sections", func(t *testing.T) {
		f, err := Load([]byte(`
[parent]
a=%(b)s
b=%(a)s
[parent.child]
c=%(a)s
`))
		require.NoError(t, err)

		issues := preflightIssues(t, f)
		require.Len(t, issues, 1)
		assert.Equal(t, PreflightIssueCycle, issues[0].Kind)
		// The cycle is a <-> b; the chain may start at either node.
		keys := map[string]bool{}
		for _, e := range issues[0].Edges {
			assert.Equal(t, "parent", e.Section)
			keys[e.Key] = true
		}
		assert.True(t, keys["a"])
		assert.True(t, keys["b"])
		assert.Contains(t, issues[0].Error(), "[parent] a")
		assert.Contains(t, issues[0].Error(), "[parent] b")

		// The child key resolves into the parent section through
		// inheritance, exactly like Key.String does.
		assert.Equal(t, "%(b)s", f.Section("parent").Key("a").Value())
	})

	t.Run("custom child section delimiter", func(t *testing.T) {
		f, err := LoadSources(LoadOptions{ChildSectionDelimiter: ":"}, []byte(`
[node]
a=%(b)s
b=ok
[node:sub]
c=%(a)s
`))
		require.NoError(t, err)
		assert.NoError(t, f.Preflight())
		assert.Equal(t, "ok", f.Section("node:sub").Key("c").String())
	})
}

func TestPreflight_SelfReference(t *testing.T) {
	f, err := Load([]byte("a=%(a)s\n"))
	require.NoError(t, err)

	issues := preflightIssues(t, f)
	require.Len(t, issues, 1)
	assert.Equal(t, PreflightIssueCycle, issues[0].Kind)
	require.Len(t, issues[0].Edges, 1)
	assert.Equal(t, "a", issues[0].Edges[0].Key)
	assert.Equal(t, "a", issues[0].Edges[0].Ref)
	assert.Contains(t, issues[0].Error(), "[DEFAULT] a")
}

func TestPreflight_MultipleReferencesInValue(t *testing.T) {
	f, err := Load([]byte("a=%(b)s and %(c)s\nb=ok\n"))
	require.NoError(t, err)

	issues := preflightIssues(t, f)
	require.Len(t, issues, 1)
	assert.Equal(t, PreflightIssueUndefined, issues[0].Kind)
	assert.Equal(t, "c", issues[0].Edges[0].Ref)
}

func TestPreflight_InMemorySourceName(t *testing.T) {
	f, err := Load([]byte("a=%(b)s\nb=%(a)s\n"))
	require.NoError(t, err)

	issues := preflightIssues(t, f)
	require.Len(t, issues, 1)
	for _, e := range issues[0].Edges {
		assert.Equal(t, "<data>", e.Source)
	}
}

func TestPreflight_DoesNotMutate(t *testing.T) {
	f, err := Load(preflightMissingConf)
	require.NoError(t, err)

	// Preflight must not create sections or keys, and raw values must
	// be identical before and after: nothing is written back or expanded.
	beforeSections := f.SectionStrings()
	beforeA := f.Section(DefaultSection).Key("a").Value()
	beforeB := f.Section(DefaultSection).Key("b").Value()
	_ = f.Preflight()
	assert.Equal(t, beforeSections, f.SectionStrings())
	assert.Equal(t, beforeA, f.Section(DefaultSection).Key("a").Value())
	assert.Equal(t, beforeB, f.Section(DefaultSection).Key("b").Value())
	_, err = f.GetSection("named")
	assert.Error(t, err)
}

func TestPreflight_Empty(t *testing.T) {
	assert.NoError(t, Empty().Preflight())
}
