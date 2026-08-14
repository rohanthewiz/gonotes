// Standalone module for the one-off DuckDB → bytdb migration tool.
//
// It lives in its own module (with a replace onto the parent) so that the
// heavy, cgo-based go-duckdb driver is a dependency of THIS tool only and
// never enters the main gonotes binary's build graph.
module gonotes-migrate

go 1.26.1

require (
	github.com/marcboeker/go-duckdb v1.8.3
	gonotes v0.0.0
)

require (
	github.com/apache/arrow-go/v18 v18.0.0 // indirect
	github.com/goccy/go-json v0.10.3 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.0 // indirect
	github.com/google/flatbuffers v24.3.25+incompatible // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/klauspost/compress v1.17.11 // indirect
	github.com/klauspost/cpuid/v2 v2.2.8 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/pierrec/lz4/v4 v4.1.21 // indirect
	github.com/rohanthewiz/btypedb v0.6.2 // indirect
	github.com/rohanthewiz/bytdb v0.6.4 // indirect
	github.com/rohanthewiz/logger v1.3.0 // indirect
	github.com/rohanthewiz/serr v1.4.0 // indirect
	github.com/sergi/go-diff v1.4.0 // indirect
	github.com/sirupsen/logrus v1.9.4 // indirect
	github.com/tidwall/btype v0.3.0 // indirect
	github.com/vmihailenco/msgpack/v5 v5.4.1 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	github.com/zeebo/xxh3 v1.0.2 // indirect
	golang.org/x/crypto v0.46.0 // indirect
	golang.org/x/exp v0.0.0-20240909161429-701f63a606c0 // indirect
	golang.org/x/mod v0.21.0 // indirect
	golang.org/x/sync v0.8.0 // indirect
	golang.org/x/sys v0.39.0 // indirect
	golang.org/x/tools v0.26.0 // indirect
	golang.org/x/xerrors v0.0.0-20231012003039-104605ab7028 // indirect
)

replace gonotes => ../..
