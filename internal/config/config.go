package config

import (
	"fmt"
	"strings"
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
	Enabled    bool `mapstructure:"enabled"`
	SizeMB     int  `mapstructure:"size_mb"`
	TTLSeconds int  `mapstructure:"ttl_seconds"`
	MaxEntries int  `mapstructure:"max_entries"`
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
	MaxFullObjectReadMB  int `mapstructure:"max_full_object_read_mb"`
	MaxBufferedWriteMB   int `mapstructure:"max_buffered_write_mb"`
	MaxDirectoryEntries  int `mapstructure:"max_directory_entries"`
}

const (
	DefaultReadAheadKB         = 8192
	DefaultMaxFullObjectReadMB = 512
	DefaultMaxBufferedWriteMB  = 512
	DefaultMaxDirectoryEntries = 100000
)

// LoggingConfig represents logging configuration
type LoggingConfig struct {
	Level  string `mapstructure:"level"`  // debug, info, warn, error
	Format string `mapstructure:"format"` // json, text
	Output string `mapstructure:"output"` // stdout, stderr, file path
}

// StagingConfig represents staging layer configuration
type StagingConfig struct {
	Enabled                      bool   `mapstructure:"enabled"`
	RootDir                      string `mapstructure:"root_dir"`
	SyncInterval                 string `mapstructure:"sync_interval"`
	SyncThresholdMB              int64  `mapstructure:"sync_threshold_mb"`
	MaxDirtyAge                  string `mapstructure:"max_dirty_age"`
	SyncOnClose                  bool   `mapstructure:"sync_on_close"`
	MaxStagingSizeGB             int64  `mapstructure:"max_staging_size_gb"`
	MaxDirtyFiles                int    `mapstructure:"max_dirty_files"`
	SyncWorkerCount              int    `mapstructure:"sync_worker_count"`
	SyncQueueSize                int    `mapstructure:"sync_queue_size"`
	MaxSyncRetries               int    `mapstructure:"max_sync_retries"`
	RetryBackoffInit             string `mapstructure:"retry_backoff_initial"`
	RetryBackoffMax              string `mapstructure:"retry_backoff_max"`
	CleanAfterSync               bool   `mapstructure:"clean_after_sync"`
	StaleFileAge                 string `mapstructure:"stale_file_age"`
	BackpressureEnabled          bool   `mapstructure:"backpressure_enabled"`
	BackpressureMode             string `mapstructure:"backpressure_mode"`
	BackpressureHighWatermarkPct int    `mapstructure:"backpressure_high_watermark_percent"`
	BackpressureCritWatermarkPct int    `mapstructure:"backpressure_critical_watermark_percent"`
	BackpressureWaitTimeout      string `mapstructure:"backpressure_wait_timeout"`
	BackpressureCheckInterval    string `mapstructure:"backpressure_check_interval"`
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

	// Enable environment variable overrides for nested config keys, e.g.
	// cos.api_key -> NFS_GATEWAY_COS_API_KEY.
	v.SetEnvPrefix("NFS_GATEWAY")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	if err := bindEnvOverrides(v); err != nil {
		return nil, err
	}

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

func bindEnvOverrides(v *viper.Viper) error {
	keys := []string{
		"server.nfs_port",
		"server.metrics_enabled",
		"server.metrics_port",
		"server.health_enabled",
		"server.health_port",
		"server.debug_enabled",
		"server.debug_port",
		"server.max_connections",
		"server.read_timeout",
		"server.write_timeout",
		"cos.endpoint",
		"cos.bucket",
		"cos.region",
		"cos.auth_type",
		"cos.api_key",
		"cos.service_id",
		"cos.access_key",
		"cos.secret_key",
		"cos.max_retries",
		"cos.timeout",
		"cache.metadata.enabled",
		"cache.metadata.size_mb",
		"cache.metadata.ttl_seconds",
		"cache.metadata.max_entries",
		"cache.data.enabled",
		"cache.data.size_gb",
		"cache.data.path",
		"cache.data.chunk_size_kb",
		"performance.read_ahead_kb",
		"performance.write_buffer_kb",
		"performance.multipart_threshold_mb",
		"performance.multipart_chunk_mb",
		"performance.worker_pool_size",
		"performance.max_concurrent_reads",
		"performance.max_concurrent_writes",
		"performance.max_full_object_read_mb",
		"performance.max_buffered_write_mb",
		"performance.max_directory_entries",
		"logging.level",
		"logging.format",
		"logging.output",
		"staging.enabled",
		"staging.root_dir",
		"staging.sync_interval",
		"staging.sync_threshold_mb",
		"staging.max_dirty_age",
		"staging.sync_on_close",
		"staging.max_staging_size_gb",
		"staging.max_dirty_files",
		"staging.sync_worker_count",
		"staging.sync_queue_size",
		"staging.max_sync_retries",
		"staging.retry_backoff_initial",
		"staging.retry_backoff_max",
		"staging.clean_after_sync",
		"staging.stale_file_age",
		"staging.backpressure_enabled",
		"staging.backpressure_mode",
		"staging.backpressure_high_watermark_percent",
		"staging.backpressure_critical_watermark_percent",
		"staging.backpressure_wait_timeout",
		"staging.backpressure_check_interval",
	}

	for _, key := range keys {
		if err := v.BindEnv(key); err != nil {
			return fmt.Errorf("failed to bind environment variable for %s: %w", key, err)
		}
	}

	return nil
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
	v.SetDefault("performance.read_ahead_kb", DefaultReadAheadKB)
	v.SetDefault("performance.write_buffer_kb", 4096)
	v.SetDefault("performance.multipart_threshold_mb", 100)
	v.SetDefault("performance.multipart_chunk_mb", 10)
	v.SetDefault("performance.worker_pool_size", 100)
	v.SetDefault("performance.max_concurrent_reads", 50)
	v.SetDefault("performance.max_concurrent_writes", 25)
	v.SetDefault("performance.max_full_object_read_mb", DefaultMaxFullObjectReadMB)
	v.SetDefault("performance.max_buffered_write_mb", DefaultMaxBufferedWriteMB)
	v.SetDefault("performance.max_directory_entries", DefaultMaxDirectoryEntries)

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
	v.SetDefault("staging.backpressure_enabled", true)
	v.SetDefault("staging.backpressure_mode", "block")
	v.SetDefault("staging.backpressure_high_watermark_percent", 80)
	v.SetDefault("staging.backpressure_critical_watermark_percent", 95)
	v.SetDefault("staging.backpressure_wait_timeout", "30s")
	v.SetDefault("staging.backpressure_check_interval", "250ms")
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

// GetBackpressureWaitTimeout returns the parsed max backpressure wait duration.
func (c *StagingConfig) GetBackpressureWaitTimeout() (time.Duration, error) {
	return time.ParseDuration(c.BackpressureWaitTimeout)
}

// GetBackpressureCheckInterval returns the parsed backpressure polling interval.
func (c *StagingConfig) GetBackpressureCheckInterval() (time.Duration, error) {
	return time.ParseDuration(c.BackpressureCheckInterval)
}

// Made with Bob
