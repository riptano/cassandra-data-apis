module github.com/riptano/cassandra-data-apis

go 1.13

require (
	github.com/gocql/gocql v0.0.0-20200228163523-cd4b606dd2fb
	github.com/graphql-go/graphql v0.7.9
	github.com/iancoleman/strcase v0.0.0-20191112232945-16388991a334
	github.com/julienschmidt/httprouter v1.3.0
	github.com/mitchellh/mapstructure v1.2.2
	github.com/onsi/ginkgo v1.12.0
	github.com/onsi/gomega v1.9.0
	github.com/spf13/cobra v0.0.7
	github.com/spf13/pflag v1.0.5
	github.com/spf13/viper v1.6.2
	github.com/stretchr/testify v1.5.1
	go.uber.org/zap v1.14.1
	gopkg.in/inf.v0 v0.9.1
)

replace github.com/graphql-go/graphql => github.com/riptano/graphql-go v0.7.9-null
