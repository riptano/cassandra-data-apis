package config

import (
	"time"

	"github.com/riptano/cassandra-data-apis/log"
)

type Config interface {
	ExcludedKeyspaces() []string
	SchemaUpdateInterval() time.Duration
	Naming() NamingConventionFn
	UseUserOrRoleAuth() bool
	Logger() log.Logger
	UrlPattern() UrlPattern
}

type UrlPattern int

const (
	UrlPatternColon UrlPattern = iota
	UrlPatternBrackets
)
