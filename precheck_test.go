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
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func asCheckErr(t *testing.T, err error) *ReferenceCheckError {
	t.Helper()
	var checkErr *ReferenceCheckError
	require.Error(t, err)
	require.True(t, errors.As(err, &checkErr), "expected *ReferenceCheckError, got %v", err)
	return checkErr
}

func chainNames(c ReferenceCycle) []string {
	names := make([]string, len(c.Chain))
	for i, n := range c.Chain {
		names[i] = n.String()
	}
	return names
}

func TestCheckValueReferences_OverrideBreaksCycle(t *testing.T) {
	// The first source defines a <-> b, the second source overwrites b with
	// a literal value, so the precheck must pass.
	f, err := Load("testdata/precheck/base.ini", "testdata/precheck/override.ini")
	require.NoError(t, err)

	report, err := f.CheckValueReferences()
	require.NoError(t, err)
	require.NotNil(t, report)
	assert.Empty(t, report.Cycles)
	assert.Empty(t, report.Missing)

	// The effective value of b really comes from the override source, and
	// the precheck neither expanded it nor changed it.
	assert.Equal(t, "ok", f.Section("").Key("b").Value())
	assert.Equal(t, "testdata/precheck/override.ini", f.Section("").keys["b"].source)
	assert.Equal(t, "testdata/precheck/base.ini", f.Section("").keys["a"].source)
}

func TestCheckValueReferences_CycleReported(t *testing.T) {
	// Without the override, a -> b -> a is reported as a cycle.
	f, err := Load("testdata/precheck/base.ini")
	require.NoError(t, err)

	report, err := f.CheckValueReferences()
	checkErr := asCheckErr(t, err)
	assert.Same(t, report, checkErr.Report)
	assert.Empty(t, report.Missing)

	require.Len(t, report.Cycles, 1)
	assert.Equal(t, []string{"a", "b", "a"}, chainNames(report.Cycles[0]))

	// Every edge retains the source file and the key name.
	require.Len(t, report.Cycles[0].Edges, 2)
	for _, e := range report.Cycles[0].Edges {
		assert.Equal(t, "testdata/precheck/base.ini", e.SourceFile)
		assert.NotEmpty(t, e.SourceKey)
		assert.Equal(t, e.SourceKey, e.From.Key)
	}
	assert.Equal(t, "a", report.Cycles[0].Edges[0].SourceKey)
	assert.Equal(t, "b", report.Cycles[0].Edges[1].SourceKey)

	// The precheck must not mutate values.
	assert.Equal(t, "%(b)s", f.Section("").Key("a").Value())
	assert.Equal(t, "%(a)s", f.Section("").Key("b").Value())
}

func TestCheckValueReferences_MissingReportedSeparately(t *testing.T) {
	// A missing reference on its own is diagnosed as undefined.
	f, err := Load("testdata/precheck/missing.ini")
	require.NoError(t, err)

	report, err := f.CheckValueReferences()
	checkErr := asCheckErr(t, err)
	assert.Empty(t, checkErr.Report.Cycles)
	require.Len(t, report.Missing, 1)

	miss := report.Missing[0].Edge
	assert.Equal(t, "a", miss.From.String())
	assert.Equal(t, "c", miss.Variable)
	assert.Equal(t, "testdata/precheck/missing.ini", miss.SourceFile)
	assert.Equal(t, "a", miss.SourceKey)

	// Cycles and missing references are diagnosed separately even when they
	// coexist in the same merged configuration.
	f, err = Load("testdata/precheck/base.ini", []byte("d = %(c)s\n"))
	require.NoError(t, err)

	report, err = f.CheckValueReferences()
	require.Error(t, err)
	require.NotNil(t, report)
	require.Len(t, report.Cycles, 1)
	assert.Equal(t, []string{"a", "b", "a"}, chainNames(report.Cycles[0]))
	require.Len(t, report.Missing, 1)
	assert.Equal(t, "d", report.Missing[0].Edge.From.String())
	assert.Equal(t, "c", report.Missing[0].Edge.Variable)
}

func TestCheckValueReferences_SectionInheritance(t *testing.T) {
	// Resolution must match real Get: a child section inherits keys from its
	// parent sections, and missing keys fall back to the default section.
	src := []byte(`
root = top
[parent]
parent_only = %(root)s
[parent.child]
child_only = %(parent_only)s
[parent.child.grandchild]
deep = %(child_only)s
[standalone]
lonely = %(root)s
broken = %(nowhere)s
`)
	f, err := Load(src)
	require.NoError(t, err)

	// First confirm the real expansion results.
	assert.Equal(t, "top", f.Section("parent").Key("parent_only").String())
	assert.Equal(t, "top", f.Section("parent.child").Key("child_only").String())
	assert.Equal(t, "top", f.Section("parent.child.grandchild").Key("deep").String())
	assert.Equal(t, "top", f.Section("standalone").Key("lonely").String())

	report, err := f.CheckValueReferences()
	checkErr := asCheckErr(t, err)
	assert.Empty(t, checkErr.Report.Cycles)
	require.Len(t, report.Missing, 1)
	miss := report.Missing[0].Edge
	assert.Equal(t, "standalone.broken", miss.From.String())
	assert.Equal(t, "nowhere", miss.Variable)

	// The inherited edges must point at the section that effectively owns
	// the resolved key.
	edgeByFrom := make(map[string]ReferenceEdge, len(report.Edges))
	for _, e := range report.Edges {
		edgeByFrom[e.From.String()] = e
	}
	assert.Equal(t, "root", edgeByFrom["parent.parent_only"].To.String())
	assert.Equal(t, "parent.parent_only",
		edgeByFrom["parent.child.child_only"].To.String())
	assert.Equal(t, "parent.child.child_only",
		edgeByFrom["parent.child.grandchild.deep"].To.String())
	assert.Equal(t, "root", edgeByFrom["standalone.lonely"].To.String())
}

func TestCheckValueReferences_CaseInsensitive(t *testing.T) {
	// Case-insensitive loads fold section and key names, and reference
	// resolution must follow the same rules as real Get.
	src := []byte(`
AppName = %(APPNAME_REF)s
appname_ref = hi
[UI]
Title = %(appname)s
`)
	f, err := InsensitiveLoad(src)
	require.NoError(t, err)

	assert.Equal(t, "hi", f.Section("ui").Key("title").String())

	report, err := f.CheckValueReferences()
	require.NoError(t, err)
	require.NotNil(t, report)
	assert.Empty(t, report.Cycles)
	assert.Empty(t, report.Missing)

	edgeByFrom := make(map[string]ReferenceEdge, len(report.Edges))
	for _, e := range report.Edges {
		edgeByFrom[e.From.String()] = e
	}
	assert.Equal(t, "appname_ref", edgeByFrom["appname"].To.Key)
	assert.Equal(t, "appname", edgeByFrom["ui.title"].To.String())
}

func TestCheckValueReferences_DefaultLoadUnchanged(t *testing.T) {
	// Loading must keep its historical behavior: cycles and missing
	// references do not produce load or read errors, values stay raw until
	// expanded and unresolved references are left as-is.
	f, err := Load("testdata/precheck/missing.ini")
	require.NoError(t, err)

	assert.Equal(t, "%(c)s", f.Section("").Key("a").String())
}
