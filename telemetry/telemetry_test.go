package telemetry

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestSetupWithoutEndpointExportsNothing(t *testing.T) {
	tel, err := Setup(context.Background(), env(nil), slog.New(slog.DiscardHandler), Config{})
	if err != nil {
		t.Fatal(err)
	}
	if tel.Enabled() {
		t.Fatal("enabled with no endpoint")
	}
	c, err := tel.MeterProvider().Meter("x").Int64Counter("c")
	if err != nil {
		t.Fatal(err)
	}
	c.Add(context.Background(), 1)
	if err := tel.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSetupRefusesUnsafeConfigWithoutQuotingSecrets(t *testing.T) {
	const secret = "Authorization=Basic%20c2VjcmV0"
	for name, e := range map[string]map[string]string{
		"plain http off loopback": {EnvEndpoint: "http://otlp.example.com", EnvHeaders: secret},
		"userinfo":                {EnvEndpoint: "https://u:p@otlp.example.com"},
		"grpc":                    {EnvEndpoint: "https://otlp.example.com", EnvProtocol: "grpc"},
		"bad header":              {EnvEndpoint: "https://otlp.example.com", EnvHeaders: "c2VjcmV0"},
		"both header sources":     {EnvEndpoint: "https://otlp.example.com", EnvHeaders: secret, EnvHeadersFile: "/x"},
		"interval under a minute": {EnvEndpoint: "https://otlp.example.com", EnvInterval: "15000"},
		"interval not a number":   {EnvEndpoint: "https://otlp.example.com", EnvInterval: "1m"},
	} {
		t.Run(name, func(t *testing.T) {
			tel, err := Setup(context.Background(), env(e), slog.New(slog.DiscardHandler), Config{})
			if err == nil {
				t.Fatal("accepted")
			}
			if tel.Enabled() {
				t.Fatal("installed on error")
			}
			if strings.Contains(err.Error(), "c2VjcmV0") {
				t.Fatalf("error quotes the secret: %v", err)
			}
		})
	}
}

func TestEnvLogNamesVariablesNeverValues(t *testing.T) {
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("cfg", "otel", Env(env(map[string]string{EnvHeaders: "Authorization=secret"})))
	if strings.Contains(buf.String(), "secret") || !strings.Contains(buf.String(), EnvHeaders+"=true") {
		t.Fatalf("log line: %s", buf.String())
	}
}

func TestHeadersFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "h")
	if err := os.WriteFile(p, []byte("Authorization=Basic%20abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := loadHeaders(env(map[string]string{EnvHeadersFile: p}))
	if err != nil || h["Authorization"] != "Basic abc" {
		t.Fatalf("headers %v, %v", h, err)
	}
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadHeaders(env(map[string]string{EnvHeadersFile: p})); err == nil {
		t.Fatal("world-readable headers file accepted")
	}
}

func TestViewKeepsAllowedLabelsAndExponentialHistograms(t *testing.T) {
	r := sdkmetric.NewManualReader()
	mp := NewMeterProvider(r, nil, Config{Labels: []attribute.Key{"kind"}, Drop: []string{"gone"}})
	m := mp.Meter("app")
	c, _ := m.Int64Counter("c")
	h, _ := m.Float64Histogram("h")
	g, _ := m.Int64Counter("gone")
	ctx := context.Background()
	c.Add(ctx, 1, metric.WithAttributes(attribute.String("kind", "a"), attribute.String("member", "id")))
	h.Record(ctx, 1.5)
	g.Add(ctx, 1)
	var rm metricdata.ResourceMetrics
	if err := r.Collect(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	seen := map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			seen[m.Name] = m
		}
	}
	if _, ok := seen["gone"]; ok {
		t.Fatal("dropped instrument exported")
	}
	sum := seen["c"].Data.(metricdata.Sum[int64])
	if attrs := sum.DataPoints[0].Attributes; attrs.Len() != 1 || !attrs.HasValue("kind") {
		t.Fatalf("labels %v", attrs.ToSlice())
	}
	if _, ok := seen["h"].Data.(metricdata.ExponentialHistogram[float64]); !ok {
		t.Fatalf("histogram is %T", seen["h"].Data)
	}
}

func TestExportFailureNeverReachesTheCallerAndIsCounted(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	var logs bytes.Buffer
	tel, err := Setup(context.Background(), env(map[string]string{EnvEndpoint: srv.URL}),
		slog.New(slog.NewTextHandler(&logs, nil)), Config{ServiceName: "t"})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := tel.MeterProvider().Meter("app").Int64Counter("c")
	c.Add(context.Background(), 1)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := tel.Shutdown(ctx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown returned the export failure: %v", err)
	}
	if calls == 0 || !strings.Contains(logs.String(), "metric export failing") {
		t.Fatalf("calls %d, logs %s", calls, logs.String())
	}
}

func TestResource(t *testing.T) {
	r := Resource(env(map[string]string{EnvResource: "deployment.environment=production,team=x"}), "poold")
	got := map[string]string{}
	for _, kv := range r.Attributes() {
		got[string(kv.Key)] = kv.Value.AsString()
	}
	if got["service.name"] != "poold" || got["deployment.environment"] != "production" || got["team"] != "" || got["service.instance.id"] == "" {
		t.Fatalf("resource %v", got)
	}
}
