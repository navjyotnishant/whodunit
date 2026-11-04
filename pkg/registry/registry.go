// Author: Navjyot Nishant
// Created: 2026-11-04
// Last updated: 2026-11-04
// Description: Public v0 loader for the metric registry (deploy/devlake/catalog.json).

// Package registry loads whodunit's metric registry: every reviewed DevLake
// query behind the Grafana dashboards, with its parameters, chart type and
// caveats. It is the same catalog the whodunit MCP server publishes.
//
// Not to be confused with internal/registry, which lists instrumented
// repositories on one machine.
//
// API stability: v0. Names may still change before whodunit 1.0.
package registry

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/navjyotnishant/whodunit/deploy/devlake"
)

// Catalog is the whole metric registry.
type Catalog struct {
	SourceSHA256   string               `json:"source_sha256"`
	DashboardCount int                  `json:"dashboard_count"`
	MetricCount    int                  `json:"metric_count"`
	Metrics        []Metric             `json:"metrics"`
	Dimensions     map[string]Dimension `json:"dimensions"`

	byID map[string]int
}

// Metric is one reviewed query.
type Metric struct {
	ID               string   `json:"id"`
	CanonicalID      string   `json:"canonical_id"`
	AlsoKnownAs      []string `json:"also_known_as"`
	Dashboard        string   `json:"dashboard"`
	Dashboards       []string `json:"dashboards"`
	Title            string   `json:"title"`
	Description      string   `json:"description"`
	ChartType        string   `json:"chart_type"`
	SQL              string   `json:"sql"`
	SQLHash          string   `json:"sql_hash"`
	Params           []string `json:"params"` // placeholder names, in bind order
	ParamsSchema     []Param  `json:"params_schema"`
	ParamsSchemaHash string   `json:"params_schema_hash"`
	MinN             *int     `json:"min_n"` // smallest sample worth showing; nil when unset
}

// Param describes one named parameter of a metric.
type Param struct {
	Name         string   `json:"name"`
	Type         string   `json:"type"` // "string" or "number"
	Required     bool     `json:"required"`
	Default      string   `json:"default,omitempty"`
	Format       string   `json:"format,omitempty"`
	Example      string   `json:"example,omitempty"`
	Allowed      []string `json:"allowed,omitempty"`
	RequiresWith []string `json:"requires_with,omitempty"`
	Description  string   `json:"description"`
}

// Dimension is a lookup query that lists the values a filter can take.
type Dimension struct {
	Query       string `json:"query"`
	LabelColumn string `json:"label_column"`
	ValueColumn string `json:"value_column"`
}

// Load parses the embedded registry. Each call returns a fresh Catalog.
func Load() (*Catalog, error) {
	return Parse(devlake.CatalogJSON)
}

// Parse parses a registry document. Unknown fields are rejected so a format
// change in the generator is caught here rather than silently dropped.
func Parse(data []byte) (*Catalog, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var c Catalog
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("registry: parse: %w", err)
	}
	if c.MetricCount != len(c.Metrics) {
		return nil, fmt.Errorf("registry: metric_count %d but %d metrics", c.MetricCount, len(c.Metrics))
	}
	c.byID = make(map[string]int, len(c.Metrics))
	for i, m := range c.Metrics {
		if m.ID == "" {
			return nil, fmt.Errorf("registry: metric %d has no id", i)
		}
		if _, dup := c.byID[m.ID]; dup {
			return nil, fmt.Errorf("registry: duplicate metric id %q", m.ID)
		}
		c.byID[m.ID] = i
	}
	return &c, nil
}

// Metric returns the metric with this id. Only the exact id matches, since
// a canonical id or alias can name several metrics; use ByCanonicalID for those.
func (c *Catalog) Metric(id string) (Metric, bool) {
	i, ok := c.byID[id]
	if !ok {
		return Metric{}, false
	}
	return c.Metrics[i], true
}

// ByCanonicalID returns every metric whose canonical id or alias is name, in
// catalog order.
func (c *Catalog) ByCanonicalID(name string) []Metric {
	var out []Metric
	for _, m := range c.Metrics {
		if m.CanonicalID == name {
			out = append(out, m)
			continue
		}
		for _, a := range m.AlsoKnownAs {
			if a == name {
				out = append(out, m)
				break
			}
		}
	}
	return out
}
