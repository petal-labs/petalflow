module github.com/petal-labs/petalflow/irisadapter

go 1.24.0

require (
	github.com/petal-labs/iris v0.15.0
	github.com/petal-labs/petalflow v0.1.0
)

require (
	github.com/google/uuid v1.6.0 // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2 // indirect
	golang.org/x/text v0.34.0 // indirect
)

// Development replace directive - remove once petalflow is published
replace github.com/petal-labs/petalflow => ../
