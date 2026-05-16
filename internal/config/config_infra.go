package config

import "time"

type StoreConfig struct {
	Path string `yaml:"path"`
}

type MemoryConfig struct {
	Enabled                  bool              `yaml:"enabled"`
	StorageType              string            `yaml:"storage_type"` // "file" or "sqlite" (default: "file")
	StorageDir               string            `yaml:"storage_dir"`  // directory for file-based storage (default: ~/.IronClaw/memory)
	EmbeddingModel           string            `yaml:"embedding_model"`
	EmbeddingBaseURL         string            `yaml:"embedding_base_url"` // base URL for embedding API (default: https://api.openai.com/v1/embeddings)
	OpenAIAPIKey             string            `yaml:"openai_api_key"`
	FactExtraction           bool              `yaml:"fact_extraction"`            // enable LLM fact extraction
	SimilarityThreshold      float64           `yaml:"similarity_threshold"`       // dedup threshold (default 0.85)
	ConsolidationInterval    time.Duration     `yaml:"consolidation_interval"`     // session->user promotion interval
	BM25Weight               float64           `yaml:"bm25_weight"`                // BM25 weight in RRF (default 0.4)
	VectorWeight             float64           `yaml:"vector_weight"`              // vector weight in RRF (default 0.6)
	EnableVSS                bool              `yaml:"enable_vss"`                 // enable HNSW indexing via sqlite-vss
	VectorDimension          int               `yaml:"vector_dimension"`           // embedding dimension (default: 1536)
	EnableSearchCache        bool              `yaml:"enable_search_cache"`        // enable search result caching
	SearchCacheSize          int               `yaml:"search_cache_size"`          // max cached queries (default: 500)
	SearchCacheTTL           time.Duration     `yaml:"search_cache_ttl"`           // cache TTL (default: 5min)
	FileStorage              FileStorageConfig `yaml:"file_storage"`               // file storage specific settings
	ReflectionCountThreshold int               `yaml:"reflection_count_threshold"` // default 10
	ReflectionDriftThreshold float64           `yaml:"reflection_drift_threshold"` // default 0.7
	ReflectionL2Trigger      int               `yaml:"reflection_l2_trigger"`      // default 5
	RetentionEpisodic        time.Duration     `yaml:"retention_episodic"`         // e.g., "720h" for 30 days
	RetentionSemantic        time.Duration     `yaml:"retention_semantic"`         // e.g., "8760h" for 365 days
	RetentionProcedural      time.Duration     `yaml:"retention_procedural"`       // 0 = never
}

// FileStorageConfig holds file-based storage specific settings.
type FileStorageConfig struct {
	FlushInterval  time.Duration `yaml:"flush_interval"`  // transaction log flush interval (default: 5s)
	ChunkThreshold int           `yaml:"chunk_threshold"` // facts per file before chunking (default: 200)
	Compression    bool          `yaml:"compression"`     // enable gzip compression for large files
}

// KnowledgeConfig holds configuration for the Phase 2 knowledge base package.
type KnowledgeConfig struct {
	Enabled           bool           `yaml:"enabled"`
	ChunkSize         int            `yaml:"chunk_size"`
	ChunkOverlap      int            `yaml:"chunk_overlap"`
	BM25Weight        float64        `yaml:"bm25_weight"`
	VectorWeight      float64        `yaml:"vector_weight"`
	GraphEnabled      bool           `yaml:"graph_enabled"`
	IngestDirs        []string       `yaml:"ingest_dirs"`
	Reranker          RerankerConfig `yaml:"reranker"`
	EnableSearchCache bool           `yaml:"enable_search_cache"` // enable search result caching
	SearchCacheSize   int            `yaml:"search_cache_size"`   // max cached queries (default: 500)
	SearchCacheTTL    time.Duration  `yaml:"search_cache_ttl"`    // cache TTL (default: 5min)
}

// RerankerConfig configures the optional LLM-based reranker.
type RerankerConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Provider string `yaml:"provider"` // "llm" or "none"
}

// GraphConfig holds configuration for the Phase 3 knowledge graph.
type GraphConfig struct {
	Enabled bool `yaml:"enabled"`
}

type SchedulerConfig struct {
	Enabled      bool          `yaml:"enabled"`
	PollInterval time.Duration `yaml:"poll_interval"`
}

type ServerConfig struct {
	Addr    string `yaml:"addr"`
	Enabled bool   `yaml:"enabled"`
}

type DashboardConfig struct {
	Enabled bool   `yaml:"enabled"`
	Addr    string `yaml:"addr"`
	Token   string `yaml:"token"`
}

// HealthConfig configures the health check HTTP endpoint.
type HealthConfig struct {
	Port int `yaml:"port"` // default: 9090
}

type LogConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}
