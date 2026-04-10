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
}

// ServerConfig represents NFS server configuration
type ServerConfig struct {
	NFSPort        int    `mapstructure:"nfs_port"`
	MetricsPort    int    `mapstructure:"metrics_port"`
	HealthPort     int    `mapstructure:"health_port"`
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
	v.SetDefault("server.metrics_port", 8080)
	v.SetDefault("server.health_port", 8081)
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

// Made with Bob
