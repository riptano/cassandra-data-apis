package cmd

import (
	"encoding/csv"
	"errors"
	"fmt"
	"github.com/datastax/cassandra-data-apis/config"
	"github.com/datastax/cassandra-data-apis/db"
	"github.com/datastax/cassandra-data-apis/endpoint"
	"github.com/datastax/cassandra-data-apis/graphql"
	"github.com/datastax/cassandra-data-apis/log"
	"github.com/datastax/cassandra-data-apis/types"
	"github.com/julienschmidt/httprouter"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	log2 "log"
	"net/http"
	"os"
	"path"
	"strings"
)

const defaultGraphQLPath = "/graphql"
const defaultGraphQLSchemaPath = "/graphql-schema"
const defaultRESTPath = "/rest"
const defaultGraphQLPlaygroundPath = "/graphql-playground"

// Environment variables prefixed with "DATA_API_" can override settings e.g. "DATA_API_HOSTS"
const envVarPrefix = "data_api"

var cfgFile string
var logger log.Logger
var cfg *endpoint.DataEndpointConfig

var serverCmd = &cobra.Command{
	Use:   os.Args[0] + " --hosts [HOSTS] [--start-graph|--start-rest] [OPTIONS]",
	Short: "GraphQL and REST endpoints for Apache Cassandra",
	Args: func(cmd *cobra.Command, args []string) error {
		hosts := getStringSlice("hosts")
		if len(hosts) == 0 {
			return errors.New("hosts are required")
		}

		startGraphQL := viper.GetBool("start-graphql")
		startREST := viper.GetBool("start-rest")

		if !startGraphQL && !startREST {
			return errors.New("at least one endpoint type should be started")
		}

		isSetSslCertPath := viper.GetString("ssl-client-cert-path") != ""
		isSetSslKeyPath := viper.GetString("ssl-client-key-path") != ""
		if isSetSslCertPath != isSetSslKeyPath {
			return errors.New("both the client certificate and private must be set")
		}

		return nil
	},
	Run: func(cmd *cobra.Command, args []string) {
		endpoint := createEndpoint()

		graphqlPort := viper.GetInt("graphql-port")
		restPort := viper.GetInt("rest-port")

		startGraphQL := viper.GetBool("start-graphql")
		startREST := viper.GetBool("start-rest")

		supportedOps := getStringSlice("operations")
		ops, err := config.Ops(supportedOps...)
		if err != nil {
			logger.Fatal("invalid supported operation", "operations", supportedOps, "error", err)
		}

		if graphqlPort == restPort {
			if startGraphQL && startREST && viper.GetString("graphql-path") == viper.GetString("rest-path") {
				logger.Fatal("graphql and rest paths can not be the same when using the same port")
			}

			router := createRouter()
			endpointNames := ""
			if startGraphQL {
				addGraphQLRoutes(router, endpoint, ops)
				endpointNames += "GraphQL"
			}
			if startREST {
				addRESTRoutes(router, endpoint, ops)
				if endpointNames != "" {
					endpointNames += "/"
				}
				endpointNames += "REST"
			}
			listenAndServe(router, graphqlPort, endpointNames)
		} else {
			finish := make(chan bool)
			if startGraphQL {
				router := createRouter()
				addGraphQLRoutes(router, endpoint, ops)
				go listenAndServe(router, graphqlPort, "GraphQL")
			}
			if startREST {
				router := httprouter.New()
				addRESTRoutes(router, endpoint, ops)
				go listenAndServe(router, restPort, "REST")
			}
			<-finish
		}
	},
}

// Execute start GraphQL/REST endpoints
func Execute() {
	zapLogger, err := zap.NewProduction()
	if err != nil {
		log2.Fatalf("unable to initialize logger: %v", err)
	}

	logger = log.NewZapLogger(zapLogger)

	flags := serverCmd.PersistentFlags()

	// General endpoint flags
	flags.StringVarP(&cfgFile, "config", "c", "", "config file")
	flags.StringSliceP("hosts", "t", nil, "hosts for connecting to the database")
	flags.StringP("username", "u", "", "connect with database username")
	flags.StringP("password", "p", "", "database user's password")

	flags.String("keyspace", "", "only allow access to a single keyspace")
	flags.Bool("request-logging", false, "enable request logging")
	flags.StringSlice("excluded-keyspaces", nil, "keyspaces to exclude from the endpoint")
	flags.Duration("schema-update-interval", endpoint.DefaultSchemaUpdateDuration, "interval in seconds used to update the graphql schema")
	flags.StringSlice("operations", []string{
		"TableCreate",
		"KeyspaceCreate",
	}, "list of supported table and keyspace management operations. options: TableCreate,TableDrop,TableAlterAdd,TableAlterDrop,KeyspaceCreate,KeyspaceDrop")
	flags.String("access-control-allow-origin", "", "Access-Control-Allow-Origin header value")

	// SSL
	flags.Bool("ssl-enabled", false, "enable SSL (client-to-node encryption)?")
	flags.String("ssl-ca-cert-path", "", "SSL CA certificate path")
	flags.String("ssl-client-cert-path", "", "SSL client certificate path")
	flags.String("ssl-client-key-path", "", "SSL client private key path")
	flags.Bool("ssl-host-verification", true, "verify the peer certificate? It is highly insecure to disable host verification")

	// GraphQL specific flags
	flags.Bool("start-graphql", true, "start the GraphQL endpoint")
	flags.String("graphql-path", defaultGraphQLPath, "GraphQL endpoint path")
	flags.String("graphql-schema-path", defaultGraphQLSchemaPath, "GraphQL schema management path")
	flags.Bool("graphql-playground", true, "expose a GraphQL playground route")
	flags.String("graphql-playground-path", defaultGraphQLPlaygroundPath, "path for the GraphQL playground static file")
	flags.Int("graphql-port", 8080, "GraphQL endpoint port")

	// REST specific flags
	flags.Bool("start-rest", true, "start the REST endpoint")
	flags.String("rest-path", defaultRESTPath, "REST endpoint path")
	flags.Int("rest-port", 8080, "REST endpoint port")

	flags.VisitAll(func(flag *pflag.Flag) {
		if flag.Name != "config" {
			err := viper.BindPFlag(flag.Name, flags.Lookup(flag.Name))

			if err != nil {
				log2.Fatalf("unable to initialize flags: %v", err)
			}
		}
	})

	cobra.OnInitialize(initialize)

	viper.SetEnvPrefix(envVarPrefix)
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.AutomaticEnv()

	if err := serverCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func createEndpoint() *endpoint.DataEndpoint {
	cfg = endpoint.NewEndpointConfigWithLogger(logger, getStringSlice("hosts")...)

	updateInterval := viper.GetDuration("schema-update-interval")
	if updateInterval <= 0 {
		updateInterval = endpoint.DefaultSchemaUpdateDuration
	}

	var sslOptions *db.SslOptions
	if viper.GetBool("ssl-enabled") {
		sslOptions = &db.SslOptions{
			CaPath:           viper.GetString("ssl-ca-cert-path"),
			CertPath:         viper.GetString("ssl-client-cert-path"),
			KeyPath:          viper.GetString("ssl-client-key-path"),
			HostVerification: viper.GetBool("ssl-host-verification"),
		}
	}

	cfg.
		WithDbConfig(db.Config{
			Username:   viper.GetString("username"),
			Password:   viper.GetString("password"),
			SslOptions: sslOptions,
		}).
		WithExcludedKeyspaces(getStringSlice("excluded-keyspaces")).
		WithSchemaUpdateInterval(updateInterval)

	dataEndpoint, err := cfg.NewEndpoint()
	if err != nil {
		logger.Fatal("unable create new dataEndpoint",
			"error", err)
	}

	return dataEndpoint
}

func addGraphQLRoutes(router *httprouter.Router, endpoint *endpoint.DataEndpoint, ops config.SchemaOperations) {
	var routes []types.Route
	var err error

	singleKeyspace := viper.GetString("keyspace")
	rootPath := viper.GetString("graphql-path")

	if singleKeyspace != "" {
		routes, err = endpoint.RoutesKeyspaceGraphQL(rootPath, singleKeyspace)
	} else {
		routes, err = endpoint.RoutesGraphQL(rootPath)
	}

	if err != nil {
		logger.Fatal("unable to generate graphql routes",
			"error", err)
	}

	for _, route := range routes {
		router.Handler(route.Method, route.Pattern, route.Handler)
	}

	if singleKeyspace != "" {
		routes, err = endpoint.RoutesSchemaManagementKeyspaceGraphQL(viper.GetString("graphql-schema-path"), singleKeyspace,  ops)
	} else {
		routes, err = endpoint.RoutesSchemaManagementGraphQL(viper.GetString("graphql-schema-path"), ops)
	}

	if err != nil {
		logger.Fatal("unable to generate graphql schema routes",
			"error", err)
	}

	if viper.GetBool("graphql-playground") {
		playgroundPath := viper.GetString("graphql-playground-path")
		hostAndPort := fmt.Sprintf("http://localhost:%d", viper.GetInt("graphql-port"))
		defaultPath := rootPath
		if singleKeyspace == "" {
			// For multi-keyspace mode, use /graphql/<any_keyspace> as default playground endpoint url
			keyspaces, err := endpoint.Keyspaces()
			if err != nil {
				logger.Fatal("could not retrieve keyspaces", "error", err)
			}

			if len(keyspaces) > 0 {
				defaultPath = path.Join(rootPath, keyspaces[0])
			}
		}
		defaultEndpointUrl := fmt.Sprintf("%s%s", hostAndPort, defaultPath)
		logger.Info("get started by visiting the GraphQL playground",
			"url", fmt.Sprintf("%s%s", hostAndPort, playgroundPath))
		router.GET(playgroundPath, graphql.GetPlaygroundHandle(defaultEndpointUrl))
	}

	for _, route := range routes {
		router.Handler(route.Method, route.Pattern, route.Handler)
	}
}

func addRESTRoutes(router *httprouter.Router, endpoint *endpoint.DataEndpoint, ops config.SchemaOperations) {
	singleKeyspace := viper.GetString("keyspace")
	rootPath := viper.GetString("rest-path")
	routes := endpoint.RoutesRest(rootPath, ops, singleKeyspace)

	for _, route := range routes {
		router.Handler(route.Method, route.Pattern, route.Handler)
	}
}

func maybeAddRequestLogging(handler http.Handler) http.Handler {
	if viper.GetBool("request-logging") {
		handler = log.NewLoggingHandler(handler, logger)
	}
	return handler
}

func maybeAddCORS(handler http.Handler) http.Handler {
	if value := viper.GetString("access-control-allow-origin"); value != "" {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", value)
			handler.ServeHTTP(w, r)
		})
	}
	return handler
}

func initialize() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
		if err := viper.ReadInConfig(); err == nil {
			logger.Info("using config file",
				"file", viper.ConfigFileUsed())
		}
	}
}

func createRouter() *httprouter.Router {
	router := httprouter.New()
	if value := viper.GetString("access-control-allow-origin"); value != "" {
		router.GlobalOPTIONS = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Access-Control-Request-Method") != "" {
				header := w.Header()
				header.Set("Access-Control-Allow-Method", r.Header.Get("Access-Control-Request-Method"))
				header.Set("Access-Control-Allow-Headers", r.Header.Get("Access-Control-Request-Headers"))
				header.Set("Access-Control-Allow-Origin", value)
			}

			w.WriteHeader(http.StatusNoContent)
		})
	}
	return router
}

func listenAndServe(handler http.Handler, port int, endpointNames string) {
	logger.Info("server listening",
		"port", port,
		"type", endpointNames)
	handler = maybeAddCORS(maybeAddRequestLogging(handler))
	err := http.ListenAndServe(fmt.Sprintf(":%d", port), handler)
	if err != nil {
		logger.Fatal("unable to start server",
			"port", port,
			"error", err)
	}
}

func getStringSlice(key string) []string {
	value := viper.GetStringSlice(key)
	slice, err := toStringSlice(value)
	if err != nil {
		logger.Fatal("invalid string slice value for setting",
			"error", err,
			"key", key,
			"value", value)
	}
	return slice
}

func toStringSlice(slice []string) ([]string, error) {
	result := make([]string, 0)
	for _, entry := range slice {
		stringReader := strings.NewReader(entry)
		csvReader := csv.NewReader(stringReader)
		split, err := csvReader.Read()
		if err != nil {
			return nil, err
		}
		for _, part := range split {
			if part != "" { // Don't add empty values
				result = append(result, part)
			}
		}
	}
	return result, nil
}
