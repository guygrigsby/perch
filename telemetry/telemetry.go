// Package telemetry exports an app's metrics over OTLP/HTTP. Lifted from
// sockd's internal/telemetry, metrics only.
//
// It reads only the standard OTEL_* variables, plus
// OTEL_EXPORTER_OTLP_HEADERS_FILE for hosts that mount secrets as files.
// With OTEL_EXPORTER_OTLP_ENDPOINT unset nothing is exported and nothing
// fails: the meter provider is a no-op. Export never blocks or fails a
// caller: the periodic reader exports on its own goroutine, and a failed
// export is counted and logged once per failure streak, never retried into
// a backlog. Every histogram is base-2 exponential, one native series per
// label set, and only the labels the app allows leave the process.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/exemplar"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
)

// The variables Setup reads. Only their names and whether each is set are
// ever logged: OTEL_EXPORTER_OTLP_HEADERS carries the backend's token.
const (
	EnvEndpoint    = "OTEL_EXPORTER_OTLP_ENDPOINT"
	EnvProtocol    = "OTEL_EXPORTER_OTLP_PROTOCOL"
	EnvHeaders     = "OTEL_EXPORTER_OTLP_HEADERS"
	EnvHeadersFile = "OTEL_EXPORTER_OTLP_HEADERS_FILE"
	EnvServiceName = "OTEL_SERVICE_NAME"
	EnvResource    = "OTEL_RESOURCE_ATTRIBUTES"
	EnvInterval    = "OTEL_METRIC_EXPORT_INTERVAL"
)

var envNames = []string{EnvEndpoint, EnvProtocol, EnvHeaders, EnvHeadersFile, EnvServiceName, EnvResource, EnvInterval}

// MinInterval is the shortest export interval Setup accepts. Grafana Cloud
// includes one data point per minute per series; a shorter interval bills
// every series again.
const MinInterval = time.Minute

// HistogramMaxSize bounds an exponential histogram's buckets. Grafana Cloud
// bills a native histogram as a quarter series per active bucket, so 40
// buckets cost at most 10 series per label set.
const HistogramMaxSize = 40

// Config is what the app decides. The zero value is usable.
type Config struct {
	// ServiceName is service.name when OTEL_SERVICE_NAME is unset.
	ServiceName string
	// Labels are the attribute keys the app's own instruments may export.
	// Every other attribute is dropped by the view. Empty keeps none.
	Labels []attribute.Key
	// Drop names instruments that are never exported.
	Drop []string
}

// Env is the startup log's view of the OTEL_* variables: each name and
// whether it is set, never a value.
type Env func(string) string

func (e Env) LogValue() slog.Value {
	attrs := make([]slog.Attr, len(envNames))
	for i, n := range envNames {
		attrs[i] = slog.Bool(n, e(n) != "")
	}
	return slog.GroupValue(attrs...)
}

// Telemetry is the installed pipeline. The one Setup returns with no
// endpoint exports nothing.
type Telemetry struct {
	provider metric.MeterProvider
	shutdown func(context.Context) error
}

// MeterProvider is the provider the app makes its meters from: a no-op one
// when nothing is exported.
func (t *Telemetry) MeterProvider() metric.MeterProvider { return t.provider }

// Enabled reports whether anything is exported.
func (t *Telemetry) Enabled() bool { return t.shutdown != nil }

// Shutdown flushes and stops exporting, giving up when ctx ends.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	if t.shutdown == nil {
		return nil
	}
	return t.shutdown(ctx)
}

// Setup installs the global meter provider and starts the Go runtime
// metrics, exporting to OTEL_EXPORTER_OTLP_ENDPOINT. log takes the
// pipeline's own errors. With no endpoint it returns a Telemetry that
// exports nothing. Errors name variables, never their values; on error
// nothing is installed.
func Setup(ctx context.Context, getenv func(string) string, log *slog.Logger, cfg Config) (*Telemetry, error) {
	off := &Telemetry{provider: noop.NewMeterProvider()}
	endpoint := strings.TrimRight(getenv(EnvEndpoint), "/")
	if endpoint == "" {
		return off, nil
	}
	if err := checkEndpoint(endpoint); err != nil {
		return off, err
	}
	if p := getenv(EnvProtocol); p != "" && p != "http/protobuf" {
		return off, fmt.Errorf("%s %q: only http/protobuf is supported", EnvProtocol, p)
	}
	interval, err := exportInterval(getenv)
	if err != nil {
		return off, err
	}
	headers, err := loadHeaders(getenv)
	if err != nil {
		return off, err
	}
	exp, err := otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(endpoint+"/v1/metrics"), otlpmetrichttp.WithHTTPClient(exportClient()),
		otlpmetrichttp.WithHeaders(headers), otlpmetrichttp.WithCompression(otlpmetrichttp.GzipCompression))
	if err != nil {
		return off, fmt.Errorf("%s: metrics exporter: %w", EnvEndpoint, err)
	}
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) { log.Warn("telemetry", "err", err) }))
	me := &metricExporter{Exporter: exp, log: log}
	mp := NewMeterProvider(sdkmetric.NewPeriodicReader(me, sdkmetric.WithInterval(interval)), Resource(getenv, cfg.ServiceName), cfg)
	m := mp.Meter("github.com/guygrigsby/perch/telemetry")
	if _, err := m.Int64ObservableCounter("telemetry.metric_exports.failed", metric.WithUnit("{export}"),
		metric.WithDescription("Metric exports that failed."),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error { o.Observe(me.failed.Load()); return nil })); err != nil {
		log.Warn("telemetry: register counter", "err", err)
	}
	if err := runtime.Start(runtime.WithMeterProvider(mp)); err != nil {
		log.Warn("telemetry: runtime metrics unavailable", "err", err)
	}
	otel.SetMeterProvider(mp)
	return &Telemetry{provider: mp, shutdown: mp.Shutdown}, nil
}

// NewMeterProvider is the provider Setup installs, over any reader: tests
// pass a ManualReader to see exactly what would be exported.
func NewMeterProvider(reader sdkmetric.Reader, res *resource.Resource, cfg Config) *sdkmetric.MeterProvider {
	if res == nil {
		res = resource.Empty()
	}
	return sdkmetric.NewMeterProvider(sdkmetric.WithResource(res), sdkmetric.WithReader(reader),
		sdkmetric.WithView(View(cfg)), sdkmetric.WithExemplarFilter(exemplar.AlwaysOffFilter))
}

// runtimeScope keeps its own closed enumerations (go.memory.type and the
// like): filtering them out would sum distinct quantities into one.
const runtimeScope = "go.opentelemetry.io/contrib/instrumentation/runtime"

// View keeps only cfg.Labels on the app's instruments, drops cfg.Drop and
// makes every histogram exponential with at most HistogramMaxSize buckets.
func View(cfg Config) sdkmetric.View {
	allowed := attribute.NewAllowKeysFilter(cfg.Labels...)
	drop := make(map[string]bool, len(cfg.Drop))
	for _, n := range cfg.Drop {
		drop[n] = true
	}
	return func(i sdkmetric.Instrument) (sdkmetric.Stream, bool) {
		s := sdkmetric.Stream{Name: i.Name, Description: i.Description, Unit: i.Unit}
		if i.Scope.Name != runtimeScope {
			s.AttributeFilter = allowed
		}
		switch {
		case drop[i.Name]:
			s.Aggregation = sdkmetric.AggregationDrop{}
		case i.Kind == sdkmetric.InstrumentKindHistogram:
			s.Aggregation = sdkmetric.AggregationBase2ExponentialHistogram{MaxSize: HistogramMaxSize, MaxScale: 20}
		}
		return s, true
	}
}

// metricExporter counts failed exports and reports success to the reader,
// since the failure is counted and logged here. Cumulative sums lost with
// one interval arrive with the next.
type metricExporter struct {
	sdkmetric.Exporter
	log     *slog.Logger
	failed  atomic.Int64
	failing atomic.Bool
}

func (e *metricExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	err := e.Exporter.Export(ctx, rm)
	switch {
	case err != nil:
		e.failed.Add(1)
		if !e.failing.Swap(true) {
			e.log.Warn("telemetry: metric export failing", "err", err)
		}
	case e.failing.Swap(false):
		e.log.Info("telemetry: metric export recovered")
	}
	return nil
}

// exportInterval is OTEL_METRIC_EXPORT_INTERVAL in milliseconds, default
// and floor MinInterval.
func exportInterval(getenv func(string) string) (time.Duration, error) {
	raw := getenv(EnvInterval)
	if raw == "" {
		return MinInterval, nil
	}
	ms, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s is not milliseconds", EnvInterval)
	}
	d := time.Duration(ms) * time.Millisecond
	if d < MinInterval {
		return 0, fmt.Errorf("%s below %d: a shorter interval multiplies the billed data points", EnvInterval, MinInterval.Milliseconds())
	}
	return d, nil
}

// processInstanceID separates cumulative streams during rolling deploys and
// restarts. Generated once per process.
var processInstanceID = sync.OnceValue(func() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + strconv.Itoa(os.Getpid())
})

// Resource carries service.name and deployment.environment from the
// standard variables, plus a process-lifetime service.instance.id. Every
// other configured attribute is ignored. fallback is service.name when
// neither variable names one.
func Resource(getenv func(string) string, fallback string) *resource.Resource {
	name := getenv(EnvServiceName)
	var env string
	for kv := range strings.SplitSeq(getenv(EnvResource), ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		v, err := url.PathUnescape(strings.TrimSpace(v))
		if err != nil {
			continue
		}
		switch strings.TrimSpace(k) {
		case "deployment.environment":
			env = v
		case "service.name":
			if name == "" {
				name = v
			}
		}
	}
	if name == "" {
		name = fallback
	}
	attrs := []attribute.KeyValue{attribute.String("service.instance.id", processInstanceID())}
	if name != "" {
		attrs = append(attrs, attribute.String("service.name", name))
	}
	if env != "" {
		attrs = append(attrs, attribute.String("deployment.environment", env))
	}
	return resource.NewSchemaless(attrs...)
}

const exportTimeoutPerRequest = 10 * time.Second

// exportClient never follows a redirect, which would carry the
// Authorization header wherever the response points.
func exportClient() *http.Client {
	return &http.Client{
		Transport: http.DefaultTransport.(*http.Transport).Clone(),
		Timeout:   exportTimeoutPerRequest,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("telemetry: OTLP endpoint redirected; redirects are refused")
		},
	}
}

// checkEndpoint refuses an endpoint that would send the Authorization
// header in the clear: plain http is allowed only to loopback.
func checkEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%s is not a URL", EnvEndpoint)
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(endpoint, "#") {
		return fmt.Errorf("%s must not carry userinfo, a query or a fragment: credentials go in %s", EnvEndpoint, EnvHeaders)
	}
	if u.Scheme == "https" {
		return nil
	}
	host := u.Hostname()
	if ip, err := netip.ParseAddr(host); u.Scheme == "http" && (host == "localhost" || err == nil && ip.IsLoopback()) {
		return nil
	}
	return fmt.Errorf("%s must be https (plain http only to loopback): the exporter sends the backend token", EnvEndpoint)
}

// loadHeaders reads OTEL_EXPORTER_OTLP_HEADERS, or the file
// OTEL_EXPORTER_OTLP_HEADERS_FILE names. Errors never quote the content.
func loadHeaders(getenv func(string) string) (map[string]string, error) {
	raw, path, src := getenv(EnvHeaders), getenv(EnvHeadersFile), EnvHeaders
	switch {
	case raw != "" && path != "":
		return nil, fmt.Errorf("%s and %s are both set: set one", EnvHeaders, EnvHeadersFile)
	case path != "":
		var err error
		if raw, err = readSecretFile(path); err != nil {
			return nil, fmt.Errorf("%s: %w", EnvHeadersFile, err)
		}
		src = EnvHeadersFile
	}
	h, err := parseHeaders(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", src, err)
	}
	return h, nil
}

// readSecretFile refuses a file readable by group or others.
func readSecretFile(path string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%s is readable by group or others (mode %v): make it 0600", path, fi.Mode().Perm())
	}
	b, err := os.ReadFile(path) //nolint:gosec // the operator names the secret file
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(b), "\r\n"), nil
}

// parseHeaders reads comma-separated key=value pairs with URL-encoded
// values.
func parseHeaders(raw string) (map[string]string, error) {
	h := map[string]string{}
	for i, part := range strings.Split(raw, ",") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, fmt.Errorf("entry %d is not key=value (content withheld)", i+1)
		}
		v, err := url.PathUnescape(strings.TrimSpace(v))
		if err != nil {
			return nil, fmt.Errorf("entry %d: value is not URL-encoded (content withheld)", i+1)
		}
		h[k] = v
	}
	return h, nil
}
