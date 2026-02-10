module github.com/petal-labs/petalflow/examples

go 1.24.0

require (
	github.com/petal-labs/iris v0.11.0
	github.com/petal-labs/petalflow v0.1.0
	github.com/petal-labs/petalflow/irisadapter v0.0.0-00010101000000-000000000000
)

require github.com/google/uuid v1.6.0 // indirect

// Development replace directives - remove once packages are published
replace github.com/petal-labs/petalflow => ../

replace github.com/petal-labs/petalflow/irisadapter => ../irisadapter

replace github.com/petal-labs/iris => /Users/erikhoward/src/github/petal-labs/iris
