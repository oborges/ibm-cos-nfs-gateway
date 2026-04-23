package config

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
)

// Config represents the application configuration
type Config struct {
	Server      ServerConfig      `mapstructure:"server"`
	COS         COSConfig         `mapstructure:"cos"`
	Cache       CacheConfig       `mapstructure:"cache"`
	Performance PerformanceConfig `mapstructure:"performance"`
	Logging     LoggingConfig     `mapstructure:"logging"`
	Staging     StagingConfig     `mapstructure:"staging"`
}

// ServerConfig represents NFS server configuration
type ServerConfig struct {
	NFSPort        int    `mapstructure:"nfs_port"`
	MetricsEnabled bool   `mapstructure:"metrics_enabled"`
	MetricsPort    int    `mapstructure:"metrics_port"`
	HealthEnabled  bool   `mapstructure:"health_enabled"`
	HealthPort     int    `mapstructure:"health_port"`
	DebugEnabled   bool   `mapstructure:"debug_enabled"`
	DebugPort      int    `mapstructure:"debug_port"`
	MaxConnections int    `mapstructure:"max_connections"`
	ReadTimeout    string `mapstructure:"read_timeout"`
	WriteTimeout   string `mapstructure:"write_timeout"`
}

// COSConfig represents IBM Cloud COS configuration
type COSConfig struct {
	Endpoint   string `mapstructure:"endpoint"`
	Bucket     string `mapstructure:"bucket"`
	Region     string `mapstructure:"region"`
	AuthType   string `mapstructure:"auth_type"` // "iam" or "hmac"
	APIKey     string `mapstructure:"api_key"`
	ServiceID  string `mapstructure:"service_id"`
	AccessKey  string `mapstructure:"access_key"`
	SecretKey  string `mapstructure:"secret_key"`
	MaxRetries int    `mapstructure:"max_retries"`
	Timeout    string `mapstructure:"timeout"`
}

// CacheConfig represents caching configuration
type CacheConfig struct {
	Metadata MetadataCacheConfig `mapstructure:"metadata"`
	Data     DataCacheConfig     `mapstructure:"data"`
}

// MetadataCacheConfig represents metadata cache configuration
type MetadataCacheConfig struct {
	Enabled    bool   `mapstructure:"enabled"`
	SizeMB     int    `mapstructure:"size_mb"`
	TTLSeconds int    `mapstructure:"ttl_seconds"`
	MaxEntries int    `mapstructure:"max_entries"`
}

// DataCacheConfig represents data cache configuration
type DataCacheConfig struct {
	Enabled   bool   `mapstructure:"enabled"`
	SizeGB    int    `mapstructure:"size_gb"`
	Path      string `mapstructure:"path"`
	ChunkSize int    `mapstructure:"chunk_size_kb"`
}

// PerformanceConfig represents performance tuning configuration
type PerformanceConfig struct {
	ReadAheadKB          int `mapstructure:"read_ahead_kb"`
	WriteBufferKB        int `mapstructure:"write_buffer_kb"`
	MultipartThresholdMB int `mapstructure:"multipart_threshold_mb"`
	MultipartChunkMB     int `mapstructure:"multipart_chunk_mb"`
	WorkerPoolSize       int `mapstructure:"worker_pool_size"`
	MaxConcurrentReads   int `mapstructure:"max_concurrent_reads"`
	MaxConcurrentWrites  int `mapstructure:"max_concurrent_writes"`
}

// LoggingConfig represents logging configuration
type LoggingConfig struct {
	Level  string `mapstructure:"level"`  // debug, info, warn, error
	Format string `mapstructure:"format"` // json, text
	Output string `mapstructure:"output"` // stdout, stderr, file path
}

// StagingConfig represents staging layer configuration
type StagingConfig struct {
	Enabled           bool   `mapstructure:"enabled"`
	RootDir           string `mapstructure:"root_dir"`
	SyncInterval      string `mapstructure:"sync_interval"`
	SyncThresholdMB   int64  `mapstructure:"sync_threshold_mb"`
	MaxDirtyAge       string `mapstructure:"max_dirty_age"`
	SyncOnClose       bool   `mapstructure:"sync_on_close"`
	MaxStagingSizeGB  int64  `mapstructure:"max_staging_size_gb"`
	MaxDirtyFiles     int    `mapstructure:"max_dirty_files"`
	SyncWorkerCount   int    `mapstructure:"sync_worker_count"`
	SyncQueueSize     int    `mapstructure:"sync_queue_size"`
	MaxSyncRetries    int    `mapstructure:"max_sync_retries"`
	RetryBackoffInit  string `mapstructure:"retry_backoff_initial"`
	RetryBackoffMax   string `mapstructure:"retry_backoff_max"`
	CleanAfterSync    bool   `mapstructure:"clean_after_sync"`
	StaleFileAge      string `mapstructure:"stale_file_age"`
}

// Load loads configuration from file and environment variables
func Load(configPath string) (*Config, error) {
	v := viper.New()

	// Set default values
	setDefaults(v)

	// Set config file path
	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath("/etc/nfs-gateway/")
		v.AddConfigPath("$HOME/.nfs-gateway")
		v.AddConfigPath("./configs")
		v.AddConfigPath(".")
	}

	// Enable environment variable overrides
	v.AutomaticEnv()
	v.SetEnvPrefix("NFS_GATEWAY")

	// Read config file
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
		// Config file not found; using defaults and env vars
	}

	// Unmarshal config
	var config Config
	if err := v.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Validate configuration
	if err := Validate(&config); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &config, nil
}

// setDefaults sets default configuration values
func setDefaults(v *viper.Viper) {
	// Server defaults
	v.SetDefault("server.nfs_port", 2049)
	v.SetDefault("server.metrics_enabled", false)
	v.SetDefault("server.metrics_port", 8080)
	v.SetDefault("server.health_enabled", false)
	v.SetDefault("server.health_port", 8081)
	v.SetDefault("server.debug_enabled", false)
	v.SetDefault("server.debug_port", 8082)
	v.SetDefault("server.max_connections", 1000)
	v.SetDefault("server.read_timeout", "30s")
	v.SetDefault("server.write_timeout", "30s")

	// COS defaults
	v.SetDefault("cos.auth_type", "iam")
	v.SetDefault("cos.max_retries", 3)
	v.SetDefault("cos.timeout", "30s")

	// Metadata cache defaults
	v.SetDefault("cache.metadata.enabled", true)
	v.SetDefault("cache.metadata.size_mb", 256)
	v.SetDefault("cache.metadata.ttl_seconds", 60)
	v.SetDefault("cache.metadata.max_entries", 10000)

	// Data cache defaults
	v.SetDefault("cache.data.enabled", true)
	v.SetDefault("cache.data.size_gb", 10)
	v.SetDefault("cache.data.path", "/var/cache/nfs-gateway")
	v.SetDefault("cache.data.chunk_size_kb", 1024)

	// Performance defaults
	v.SetDefault("performance.read_ahead_kb", 1024)
	v.SetDefault("performance.write_buffer_kb", 4096)
	v.SetDefault("performance.multipart_threshold_mb", 100)
	v.SetDefault("performance.multipart_chunk_mb", 10)
	v.SetDefault("performance.worker_pool_size", 100)
	v.SetDefault("performance.max_concurrent_reads", 50)
	v.SetDefault("performance.max_concurrent_writes", 25)

	// Logging defaults
	v.SetDefault("logging.level", "info")
	v.SetDefault("logging.format", "json")
	v.SetDefault("logging.output", "stdout")

	// Staging defaults (disabled by default for safety)
	v.SetDefault("staging.enabled", false)
	v.SetDefault("staging.root_dir", "/var/staging/nfs-gateway")
	v.SetDefault("staging.sync_interval", "30s")
	v.SetDefault("staging.sync_threshold_mb", 10)
	v.SetDefault("staging.max_dirty_age", "5m")
	v.SetDefault("staging.sync_on_close", false)
	v.SetDefault("staging.max_staging_size_gb", 10)
	v.SetDefault("staging.max_dirty_files", 1000)
	v.SetDefault("staging.sync_worker_count", 4)
	v.SetDefault("staging.sync_queue_size", 100)
	v.SetDefault("staging.max_sync_retries", 3)
	v.SetDefault("staging.retry_backoff_initial", "1s")
	v.SetDefault("staging.retry_backoff_max", "60s")
	v.SetDefault("staging.clean_after_sync", true)
	v.SetDefault("staging.stale_file_age", "24h")
}

// GetReadTimeout returns the parsed read timeout duration
func (c *ServerConfig) GetReadTimeout() (time.Duration, error) {
	return time.ParseDuration(c.ReadTimeout)
}

// GetWriteTimeout returns the parsed write timeout duration
func (c *ServerConfig) GetWriteTimeout() (time.Duration, error) {
	return time.ParseDuration(c.WriteTimeout)
}

// GetTimeout returns the parsed COS timeout duration
func (c *COSConfig) GetTimeout() (time.Duration, error) {
	return time.ParseDuration(c.Timeout)
}

// GetTTL returns the metadata cache TTL as a duration
func (c *MetadataCacheConfig) GetTTL() time.Duration {
	return time.Duration(c.TTLSeconds) * time.Second
}

// GetSyncInterval returns the parsed sync interval duration
func (c *StagingConfig) GetSyncInterval() (time.Duration, error) {
	return time.ParseDuration(c.SyncInterval)
}

// GetMaxDirtyAge returns the parsed max dirty age duration
func (c *StagingConfig) GetMaxDirtyAge() (time.Duration, error) {
	return time.ParseDuration(c.MaxDirtyAge)
}

// GetRetryBackoffInitial returns the parsed initial retry backoff duration
func (c *StagingConfig) GetRetryBackoffInitial() (time.Duration, error) {
	return time.ParseDuration(c.RetryBackoffInit)
}

// GetRetryBackoffMax returns the parsed max retry backoff duration
func (c *StagingConfig) GetRetryBackoffMax() (time.Duration, error) {
	return time.ParseDuration(c.RetryBackoffMax)
}

// GetStaleFileAge returns the parsed stale file age duration
func (c *StagingConfig) GetStaleFileAge() (time.Duration, error) {
	return time.ParseDuration(c.StaleFileAge)
}

// Made with Bob
