module github.com/petal-labs/petalflow/irisadapter

go 1.25.0

require (
	github.com/petal-labs/iris v1.0.0
	github.com/petal-labs/petalflow v0.1.0
)

require (
	github.com/google/uuid v1.6.0 // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2 // indirect
	golang.org/x/text v0.41.0 // indirect
)

// Development replace directive - remove once petalflow is published
replace github.com/petal-labs/petalflow => ../
