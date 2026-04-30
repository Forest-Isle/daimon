package observability

const (
	defaultServiceName = "ironclaw"
	defaultLibraryName = "github.com/Forest-Isle/IronClaw"
)

// Config configures OpenTelemetry tracing and metrics bootstrap.
type Config struct {
	Enabled     bool    `yaml:"enabled"`
	ServiceName string  `yaml:"service_name"` // default: "ironclaw"
	Exporter    string  `yaml:"exporter"`     // "otlp_grpc" | "otlp_http" | "stdout" | "noop"
	Endpoint    string  `yaml:"endpoint"`     // OTLP endpoint, e.g. "localhost:4317"
	SampleRate  float64 `yaml:"sample_rate"`  // 0.0-1.0, default 1.0
}

func (c Config) normalized() Config {
	if c.ServiceName == "" {
		c.ServiceName = defaultServiceName
	}
	if c.SampleRate <= 0 {
		c.SampleRate = 1.0
	}
	if c.SampleRate > 1.0 {
		c.SampleRate = 1.0
	}
	return c
}
