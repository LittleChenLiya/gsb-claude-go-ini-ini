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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProvenance_SectionInheritance(t *testing.T) {
	// Three-section example: DEFAULT, parent, parent.child.
	// Child overrides parent port; uncovered items point to parent.
	f, err := LoadSources(LoadOptions{
		SensitiveKeys: []string{"password"},
	}, []byte(`
[DEFAULT]
timeout = 30
retries = 3

[parent]
port = 8080
host = localhost
user = admin

[parent.child]
port = 9090
password = secret123
debug = true
`))
	require.NoError(t, err)
	require.NotNil(t, f)

	child := f.Section("parent.child")
	sp := child.Provenance()
	require.NotNil(t, sp)
	assert.Equal(t, "parent.child", sp.Section)

	// Build a map for easy lookup.
	prov := make(map[string]*Provenance)
	for _, p := range sp.Keys {
		prov[p.Key] = p
	}

	// port: child overrides parent -> source is child, overridden = true.
	port := prov["port"]
	require.NotNil(t, port)
	assert.Equal(t, "parent.child", port.Section)
	assert.Equal(t, "9090", port.Value)
	assert.True(t, port.Overridden)
	assert.False(t, port.Sensitive)

	// host: not overridden by child -> source is parent.
	host := prov["host"]
	require.NotNil(t, host)
	assert.Equal(t, "parent", host.Section)
	assert.Equal(t, "localhost", host.Value)
	assert.False(t, host.Overridden)

	// user: not overridden by child -> source is parent.
	user := prov["user"]
	require.NotNil(t, user)
	assert.Equal(t, "parent", user.Section)
	assert.Equal(t, "admin", user.Value)

	// password: sensitive -> value is empty, source is child.
	pass := prov["password"]
	require.NotNil(t, pass)
	assert.Equal(t, "parent.child", pass.Section)
	assert.Equal(t, "", pass.Value)
	assert.True(t, pass.Sensitive)

	// debug: child's own key -> source is child.
	debug := prov["debug"]
	require.NotNil(t, debug)
	assert.Equal(t, "parent.child", debug.Section)
	assert.Equal(t, "true", debug.Value)

	// timeout: from DEFAULT -> source is DEFAULT.
	timeout := prov["timeout"]
	require.NotNil(t, timeout)
	assert.Equal(t, DefaultSection, timeout.Section)
	assert.Equal(t, "30", timeout.Value)

	// retries: from DEFAULT -> source is DEFAULT.
	retries := prov["retries"]
	require.NotNil(t, retries)
	assert.Equal(t, DefaultSection, retries.Section)
	assert.Equal(t, "3", retries.Value)
}

func TestProvenance_ShadowValues(t *testing.T) {
	// Duplicate key override order preserved.
	f, err := LoadSources(LoadOptions{
		AllowShadows: true,
	}, []byte(`
[server]
port = 8080
port = 8081
port = 8082
host = localhost
`))
	require.NoError(t, err)
	require.NotNil(t, f)

	sp := f.Section("server").Provenance()
	require.NotNil(t, sp)

	prov := make(map[string]*Provenance)
	for _, p := range sp.Keys {
		prov[p.Key] = p
	}

	port := prov["port"]
	require.NotNil(t, port)
	assert.Equal(t, "8080", port.Value)
	require.Len(t, port.Shadows, 2)
	// Shadow order preserved.
	assert.Equal(t, "8081", port.Shadows[0].Value)
	assert.Equal(t, "8082", port.Shadows[1].Value)
	// Shadows are not mixed into the effective value.
	assert.Equal(t, "8080", port.Value)
}

func TestProvenance_SensitiveDoesNotLeak(t *testing.T) {
	f, err := LoadSources(LoadOptions{
		SensitiveKeys: []string{"password", "secret"},
	}, []byte(`
[db]
host = localhost
password = hunter2
secret = s3cr3t
user = admin
`))
	require.NoError(t, err)
	require.NotNil(t, f)

	sp := f.Section("db").Provenance()
	for _, p := range sp.Keys {
		if p.Key == "password" || p.Key == "secret" {
			assert.True(t, p.Sensitive)
			assert.Empty(t, p.Value)
		} else {
			assert.False(t, p.Sensitive)
			assert.NotEmpty(t, p.Value)
		}
	}
}

func TestProvenance_FileLevel(t *testing.T) {
	f, err := Load([]byte(`
[DEFAULT]
timeout = 30

[parent]
port = 8080

[parent.child]
port = 9090
`))
	require.NoError(t, err)
	require.NotNil(t, f)

	all := f.Provenance()
	require.Len(t, all, 3)

	// Check that each section has provenance.
	names := make([]string, len(all))
	for i, sp := range all {
		names[i] = sp.Section
	}
	assert.Contains(t, names, DefaultSection)
	assert.Contains(t, names, "parent")
	assert.Contains(t, names, "parent.child")
}

func TestProvenance_String(t *testing.T) {
	f, err := LoadSources(LoadOptions{
		SensitiveKeys: []string{"password"},
	}, []byte(`
[parent]
port = 8080

[parent.child]
port = 9090
password = secret
`))
	require.NoError(t, err)
	require.NotNil(t, f)

	sp := f.Section("parent.child").Provenance()
	s := sp.String()
	assert.Contains(t, s, "port")
	assert.Contains(t, s, "9090")
	assert.Contains(t, s, "password")
	assert.Contains(t, s, "<redacted>")
	assert.Contains(t, s, "overridden")
}

func ExampleSection_Provenance() {
	// Three-section example: DEFAULT, parent, parent.child.
	// The child overrides the parent's port; uncovered items point to the parent.
	// The password key is sensitive and its value is redacted.
	f, _ := LoadSources(LoadOptions{
		SensitiveKeys: []string{"password"},
	}, []byte(`
[DEFAULT]
timeout = 30

[parent]
port = 8080
host = localhost

[parent.child]
port = 9090
password = secret123
debug = true
`))

	sp := f.Section("parent.child").Provenance()
	for _, p := range sp.Keys {
		if p.Sensitive {
			fmt.Printf("%s: source=%s sensitive=true overridden=%v\n", p.Key, p.Section, p.Overridden)
		} else {
			fmt.Printf("%s: value=%q source=%s overridden=%v\n", p.Key, p.Value, p.Section, p.Overridden)
		}
	}
	// Output:
	// port: value="9090" source=parent.child overridden=true
	// password: source=parent.child sensitive=true overridden=false
	// debug: value="true" source=parent.child overridden=false
	// host: value="localhost" source=parent overridden=false
	// timeout: value="30" source=DEFAULT overridden=false
}
