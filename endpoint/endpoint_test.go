package endpoint

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
	"testing"

	"github.com/gocql/gocql"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/riptano/cassandra-data-apis/auth"
	"github.com/riptano/cassandra-data-apis/db"
	"github.com/riptano/cassandra-data-apis/graphql"
	"github.com/riptano/cassandra-data-apis/internal/testutil"
	"github.com/riptano/cassandra-data-apis/internal/testutil/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

const (
	getIndex  = 0
	postIndex = 1
)

const host = "127.0.0.1"

func TestDataEndpoint_Query(t *testing.T) {
	session, routes := createRoutes(t, createConfig(t), "/graphql", "store")

	title := "book1"
	pages := 42
	resultMock := &db.ResultMock{}
	resultMock.
		On("PageState").Return([]byte{}).
		On("Values").Return([]map[string]interface{}{
		map[string]interface{}{"title": &title, "pages": &pages},
	}, nil)

	session.
		On("ExecuteIter", `SELECT * FROM "store"."books" WHERE "title" = ?`, mock.Anything, mock.Anything).
		Return(resultMock, nil)

	body := graphql.RequestBody{
		Query: `query {
  books(value:{title:"abc"}) {
    values {
      pages
      title
    }
  }
}`,
	}

	expected := schemas.ResponseBody{
		Data: map[string]interface{}{
			"books": map[string]interface{}{
				"values": []interface{}{
					map[string]interface{}{
						"pages": float64(pages),
						"title": title,
					},
				},
			},
		},
	}

	buffer, err := executePost(routes, "/graphql", body, nil)
	assert.NoError(t, err, "error executing query")

	var resp schemas.ResponseBody
	err = json.NewDecoder(buffer).Decode(&resp)
	assert.NoError(t, err, "error decoding response")
	assert.Equal(t, expected, resp)
}

func TestDataEndpoint_Auth(t *testing.T) {
	session, routes := createRoutes(t,
		createConfig(t).WithUseUserOrRoleAuth(true),
		"/graphql", "store")

	title := "book1"
	pages := 42
	resultMock := &db.ResultMock{}
	resultMock.
		On("PageState").Return([]byte{}).
		On("Values").Return([]map[string]interface{}{
		map[string]interface{}{"title": &title, "pages": &pages},
	}, nil)

	authTokens := map[string]string{"token1": "user1"}

	session.
		On("ExecuteIter", `SELECT * FROM "store"."books" WHERE "title" = ?`,
			db.
				NewQueryOptions().
				WithUserOrRole("user1").
				WithPageState([]byte{}).
				WithPageSize(graphql.DefaultPageSize).
				WithConsistency(graphql.DefaultConsistencyLevel),
			mock.Anything).
		Return(resultMock, nil)

	body := graphql.RequestBody{
		Query: `query {
  books(value:{title:"abc"}) {
    values {
      pages
      title
    }
  }
}`,
	}

	expected := schemas.ResponseBody{
		Data: map[string]interface{}{
			"books": map[string]interface{}{
				"values": []interface{}{
					map[string]interface{}{
						"pages": float64(pages),
						"title": title,
					},
				},
			},
		},
	}

	buffer, err := executePost(withAuth(t, routes, authTokens), "/graphql", body,
		http.Header{"X-Cassandra-Token": []string{"token1"}})
	assert.NoError(t, err, "error executing query")

	var resp schemas.ResponseBody
	err = json.NewDecoder(buffer).Decode(&resp)
	assert.NoError(t, err, "error decoding response")
	assert.Equal(t, expected, resp)
}

func TestDataEndpoint_PageSize(t *testing.T) {
	session, routes := createRoutes(t, createConfig(t), "/graphql", "store")
	resultMock := &db.ResultMock{}
	resultMock.
		On("PageState").Return([]byte{}).
		On("Values").Return([]map[string]interface{}{
		{"title": "book1", "pages": 1},
	}, nil)
	session.On("ExecuteIter", mock.Anything, mock.Anything, mock.Anything).Return(resultMock, nil)

	query := `SELECT * FROM "store"."books" WHERE "title" = ?`
	graphqlQuery := `query {
	  books(value:{title:"abc"}%s) {
		values {
		  pages
		  title
		}
	  }
	}`

	var resp schemas.ResponseBody
	dbQueryOptions := db.NewQueryOptions().
		WithPageState([]byte{}).
		WithPageSize(graphql.DefaultPageSize).
		WithConsistency(graphql.DefaultConsistencyLevel)

	// Query with no options
	buffer, err := executePost(routes, "/graphql", graphql.RequestBody{
		Query: fmt.Sprintf(graphqlQuery, ""),
	}, nil)
	assert.NoError(t, err, "error executing query")
	err = json.NewDecoder(buffer).Decode(&resp)
	assert.NoError(t, err)
	session.AssertCalled(t, "ExecuteIter", query, dbQueryOptions, mock.Anything)

	// Query with consistency
	buffer, err = executePost(routes, "/graphql", graphql.RequestBody{
		Query: fmt.Sprintf(graphqlQuery, ", options: {consistency: LOCAL_ONE}"),
	}, nil)
	assert.NoError(t, err, "error executing query")
	err = json.NewDecoder(buffer).Decode(&resp)
	assert.NoError(t, err)
	// Page size is still default (100)
	dbQueryOptions.WithConsistency(gocql.LocalOne)
	session.AssertCalled(t, "ExecuteIter", query, dbQueryOptions, mock.Anything)

	// Query with limit
	buffer, err = executePost(routes, "/graphql", graphql.RequestBody{
		Query: fmt.Sprintf(graphqlQuery, ", options: {limit: 3}"),
	}, nil)
	assert.NoError(t, err, "error executing query")
	err = json.NewDecoder(buffer).Decode(&resp)
	assert.NoError(t, err)
	// Page size is still default (100)
	dbQueryOptions.WithConsistency(graphql.DefaultConsistencyLevel)
	session.AssertCalled(t, "ExecuteIter", query+" LIMIT ?", dbQueryOptions, mock.Anything)
}

func TestDataEndpoint_AuthNotProvided(t *testing.T) {
	session, routes := createRoutes(t,
		createConfig(t).WithUseUserOrRoleAuth(true),
		"/graphql", "store")

	title := "book1"
	pages := 42
	resultMock := &db.ResultMock{}
	resultMock.
		On("PageState").Return([]byte{}).
		On("Values").Return([]map[string]interface{}{
		map[string]interface{}{"title": &title, "pages": &pages},
	}, nil)

	session.
		On("ExecuteIter", `SELECT * FROM "store"."books" WHERE "title" = ?`,
			db.
				NewQueryOptions().
				WithUserOrRole("user1").
				WithConsistency(gocql.LocalQuorum),
			mock.Anything).
		Return(resultMock, errors.New("invalid cre"))

	body := graphql.RequestBody{
		Query: `query {
  books(value:{title:"abc"}) {
    values {
      pages
      title
    }
  }
}`,
	}

	buffer, err := executePost(routes, "/graphql", body, nil) // No auth
	assert.NoError(t, err, "error executing query")

	var resp schemas.ResponseBody
	err = json.NewDecoder(buffer).Decode(&resp)
	assert.NoError(t, err, "error decoding response")
	assert.Len(t, resp.Errors, 1)
	assert.Equal(t, "expected user or role for this operation", resp.Errors[0].Message)
}

func executePost(routes []graphql.Route, target string, body graphql.RequestBody, header http.Header) (*bytes.Buffer, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	r := httptest.NewRequest(http.MethodPost, fmt.Sprintf("http://%s", path.Join(host, target)), bytes.NewReader(b))
	if header != nil {
		r.Header = header
	}
	w := httptest.NewRecorder()
	routes[postIndex].Handler.ServeHTTP(w, r)

	return w.Body, nil
}

func createConfig(t *testing.T) *DataEndpointConfig {
	cfg, err := NewEndpointConfig(host)
	assert.NoError(t, err, "error creating endpoint config")
	return cfg
}

func createRoutes(t *testing.T, cfg *DataEndpointConfig, pattern string, ksName string) (*db.SessionMock, []graphql.Route) {
	sessionMock := db.NewSessionMock().Default()

	endpoint := cfg.newEndpointWithDb(db.NewDbWithSession(sessionMock))
	routes, err := endpoint.RoutesKeyspaceGraphQL("/graphql", ksName)

	assert.Len(t, routes, 2, "expected GET and POST routes")
	assert.NoError(t, err, "error getting routes for keyspace")

	return sessionMock, routes
}

func withAuth(t *testing.T, routes []graphql.Route, authTokens map[string]string) []graphql.Route {
	for i, route := range routes {
		routes[i].Handler = &authHandler{t, route.Handler, authTokens}
	}
	return routes
}

type authHandler struct {
	t          *testing.T
	handler    http.Handler
	authTokens map[string]string
}

func (h *authHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("X-Cassandra-Token")
	ctx := r.Context()

	if userOrRole, ok := h.authTokens[token]; ok {
		h.handler.ServeHTTP(w, r.WithContext(auth.WithContextUserOrRole(ctx, userOrRole)))
	} else {
		bytes, err := json.Marshal(schemas.ResponseBody{Errors: []schemas.ErrorEntry{{Message: "authorization failed"}}})
		assert.NoError(h.t, err, "error marshalling error")
		w.Write(bytes)
		return
	}
}

var _ = BeforeSuite(testutil.BeforeTestSuite)

var _ = AfterSuite(testutil.AfterTestSuite)

func TestEndpoint(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Endpoint test suite")
}
