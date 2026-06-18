package snapshotcache

import (
	"strconv"
	"strings"
)

type FilterKind string

const (
	FilterEq     FilterKind = "eq"
	FilterIn     FilterKind = "in"
	FilterPrefix FilterKind = "prefix"
	FilterRange  FilterKind = "range"
	FilterFunc   FilterKind = "func"
)

type Query[T any] struct {
	Filters []Filter[T]
}

type Filter[T any] struct {
	Kind      FilterKind
	IndexName string
	Key       []string
	Keys      [][]string
	Prefix    string
	Min       string
	Max       string
	Predicate func(T) bool
}

func Eq[T any](indexName string, key ...string) Filter[T] {
	return Filter[T]{Kind: FilterEq, IndexName: indexName, Key: key}
}

func In[T any](indexName string, keys ...[]string) Filter[T] {
	return Filter[T]{Kind: FilterIn, IndexName: indexName, Keys: keys}
}

func Prefix[T any](indexName string, prefix string) Filter[T] {
	return Filter[T]{Kind: FilterPrefix, IndexName: indexName, Prefix: prefix}
}

func Range[T any](indexName string, min string, max string) Filter[T] {
	return Filter[T]{Kind: FilterRange, IndexName: indexName, Min: min, Max: max}
}

func Where[T any](predicate func(T) bool) Filter[T] {
	return Filter[T]{Kind: FilterFunc, Predicate: predicate}
}

func joinKey(parts []string) string {
	var builder strings.Builder
	for _, part := range parts {
		builder.WriteString(strconv.Itoa(len(part)))
		builder.WriteByte(':')
		builder.WriteString(part)
	}
	return builder.String()
}

func compareKey(parts []string) string {
	return strings.Join(parts, "\x00")
}
