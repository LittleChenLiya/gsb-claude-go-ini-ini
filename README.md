# INI

[![GoDoc](https://img.shields.io/badge/GoDoc-Reference-blue?style=for-the-badge&logo=go)](https://pkg.go.dev/github.com/go-ini/ini?tab=doc)

![](https://avatars0.githubusercontent.com/u/10216035?v=3&s=200)

Package ini provides INI file read and write functionality in Go.

## Features

- Load from multiple data sources(file, `[]byte`, `io.Reader` and `io.ReadCloser`) with overwrites.
- Read with recursion values.
- Read with parent-child sections.
- Read with auto-increment key names.
- Read with multiple-line values.
- Read with tons of helper methods.
- Read and convert values to Go types.
- Read and **WRITE** comments of sections and keys.
- Manipulate sections, keys and comments with ease.
- Keep sections and keys in order as you parse and save.

## Installation

The minimum requirement of Go is **1.13**.

```sh
$ go get gopkg.in/ini.v1@latest
```

> [!NOTE]
> If you previously used `github.com/go-ini/ini` as the import path in your project, without updating all of your code, you can use the following command to replace the import path in your `go.mod`:
> ```zsh
> go mod edit -replace github.com/go-ini/ini=gopkg.in/ini.v1@latest
> ```

## Getting Help

- [Getting Started](https://ini.unknwon.io/docs/intro/getting_started)

## Value provenance (where does the effective value come from?)

Besides reading values, you can ask **why** an effective value is what it is.
The provenance API only reads the already parsed in-memory state: it never
reloads a file, reads environment variables or changes normal `Get` behavior.

```go
f, err := ini.LoadSources(ini.LoadOptions{
    // Values of these keys are never exposed by the provenance API.
    SensitiveKeys: []string{"password"},
}, []byte(`
[server]
host     = 127.0.0.1
port     = 8080
password = s3cret

[server.api]
port = 9090
`))
if err != nil {
    // handle error
}

p := f.Section("server.api").Provenance("port")
p.EffectiveSection // "server.api"  – the child override wins
p.EffectiveValue   // "9090"
p.Chain[0]         // server/port=8080, Overridden=true
p.Chain[1]         // server.api/port=9090 (the winning origin)

f.Section("server.api").Provenance("host").EffectiveSection // "server" (inherited)

// Sensitive keys only reveal their source and override facts, never a value.
sp := f.Section("server").Provenance("password")
sp.Sensitive      // true
sp.EffectiveValue // ""

// A default is recorded as the final source only when the key is undefined.
dp := f.Section("server.api").ProvenanceWithDefault("timeout", "30s")
dp.HasDefault     // true
dp.EffectiveValue // "30s"
```

What the explanation covers:

- **Section inheritance** – dotted child sections (`server.api` inherits
  `server`) and, optionally, an explicit parent declared with a key configured
  through `LoadOptions.ParentSectionKey`.
- **Same-key overrides** – repeated keys in one source and later data sources
  keep their order; earlier values stay in the chain with `Overridden=true`.
- **Default values** – `ProvenanceWithDefault` attaches the default only when
  no parsed definition exists.
- **Shadow keys** – with `ShadowLoad`, duplicate values are queryable through
  `KeyProvenance.Shadows` but are never mixed into the effective value.
- **Cyclic inheritance** – cycles are reported separately in
  `InheritanceCycle` instead of causing infinite recursion.

Each `ValueOrigin` carries the source name, source index, physical line
number, section, key and the raw value (redacted for sensitive keys).

Run the focused tests and the runnable three-section example with:

```sh
go test -run 'TestProvenance|ExampleSection_Provenance' -v .
```

## License

This project is under Apache v2 License. See the [LICENSE](LICENSE) file for the full license text.
