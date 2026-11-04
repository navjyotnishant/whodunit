package registry

import (
	"strings"
	"testing"
)

func TestLoadEmbeddedCatalog(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Metrics) == 0 || len(c.Metrics) != c.MetricCount {
		t.Fatalf("metrics = %d, metric_count = %d", len(c.Metrics), c.MetricCount)
	}
	if len(c.Dimensions) == 0 {
		t.Fatal("no dimensions")
	}
	for _, m := range c.Metrics {
		if m.SQL == "" || m.ChartType == "" || m.Title == "" {
			t.Errorf("metric %s is missing sql, chart_type or title", m.ID)
		}
		declared := map[string]bool{}
		for _, p := range m.ParamsSchema {
			declared[p.Name] = true
		}
		for _, name := range m.Params {
			if !declared[name] {
				t.Errorf("metric %s binds %q with no params_schema entry", m.ID, name)
			}
		}
		if got := strings.Count(m.SQL, "?"); got < len(m.Params) {
			t.Errorf("metric %s has %d params but only %d placeholders", m.ID, len(m.Params), got)
		}
	}
	first := c.Metrics[0]
	if got, ok := c.Metric(first.ID); !ok || got.SQLHash != first.SQLHash {
		t.Fatalf("Metric(%q) lookup failed", first.ID)
	}
	if _, ok := c.Metric("no-such-metric"); ok {
		t.Fatal("unknown id found")
	}
	if first.CanonicalID != "" && len(c.ByCanonicalID(first.CanonicalID)) == 0 {
		t.Fatalf("ByCanonicalID(%q) found nothing", first.CanonicalID)
	}
}

func TestParseRejectsDrift(t *testing.T) {
	for name, doc := range map[string]string{
		"unknown field": `{"metric_count":0,"metrics":[],"surprise":1}`,
		"count":         `{"metric_count":2,"metrics":[{"id":"a"}]}`,
		"duplicate":     `{"metric_count":2,"metrics":[{"id":"a"},{"id":"a"}]}`,
		"no id":         `{"metric_count":1,"metrics":[{"title":"x"}]}`,
	} {
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
