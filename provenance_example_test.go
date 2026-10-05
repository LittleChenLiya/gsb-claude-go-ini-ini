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

package ini_test

import (
	"fmt"

	"gopkg.in/ini.v1"
)

// ExampleSection_Provenance shows how to explain the effective value of a
// key with three sections, section inheritance, same-key overrides and a
// sensitive key.
func ExampleSection_Provenance() {
	cfg := `
[server]
host     = 127.0.0.1
port     = 8080
password = s3cret

[server.api]
port = 9090

[server.api.v2]
debug = true
`
	f, err := ini.LoadSources(ini.LoadOptions{
		// Values of these keys are never returned by the provenance API.
		SensitiveKeys: []string{"password"},
	}, []byte(cfg))
	if err != nil {
		panic(err)
	}

	explain := func(section, key string) {
		p := f.Section(section).Provenance(key)

		win := p.Chain[len(p.Chain)-1]
		value := win.Value
		if p.Sensitive {
			value = "<redacted>"
		}

		fmt.Printf("%s.%s <- section=%s value=%q", section, key, p.EffectiveSection, value)
		if len(p.Chain) > 1 && p.Chain[0].Overridden {
			fmt.Printf(" (overrode %q from %s)", p.Chain[0].Value, p.Chain[0].Section)
		}
		if p.Sensitive {
			fmt.Print(" [sensitive: source shown, value hidden]")
		}
		fmt.Println()
	}

	// Child overrides the parent port: the final source is the child.
	explain("server.api", "port")
	// The child does not define host, so it points at the parent section.
	explain("server.api", "host")
	// Grandchild inherits port from the nearer parent section.
	explain("server.api.v2", "port")
	// Sensitive key exposes its source but never its value.
	explain("server", "password")

	// Default value becomes the final source only for an undefined key.
	p := f.Section("server.api.v2").ProvenanceWithDefault("timeout", "30s")
	fmt.Printf("server.api.v2.timeout default=%q has-default=%v\n",
		p.EffectiveValue, p.HasDefault)

	// Output:
	// server.api.port <- section=server.api value="9090" (overrode "8080" from server)
	// server.api.host <- section=server value="127.0.0.1"
	// server.api.v2.port <- section=server.api value="9090" (overrode "8080" from server)
	// server.password <- section=server value="<redacted>" [sensitive: source shown, value hidden]
	// server.api.v2.timeout default="30s" has-default=true
}
