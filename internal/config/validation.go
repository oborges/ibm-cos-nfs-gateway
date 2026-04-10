package config

import (
	"fmt"
	"os"
	"strings"
)

// Validate validates the configuration
func Validate(config *Config) error {
	if err := validateServer(&config.Server); err != nil {
		return fmt.Errorf("server config: %w", err)
	}

	if err := validateCOS(&config.COS); err != nil {
		return fmt.Errorf("cos config: %w", err)
	}

	if err := validateCache(&config.Cache); err != nil {
		return fmt.Errorf("cache config: %w", err)
	}

	if err := validatePerformance(&config.Performance); err != nil {
		return fmt.Errorf("performance config: %w", err)
	}

	if err := validateLogging(&config.Logging); err != nil {
		return fmt.Errorf("logging config: %w", err)
	}

	return nil
}

// validateServer validates server configuration
func validateServer(config *ServerConfig) error {
	if config.NFSPort < 1 || config.NFSPort > 65535 {
		return fmt.Errorf("invalid nfs_port: %d (must be 1-65535)", config.NFSPort)
	}

	if config.MetricsPort < 1 || config.MetricsPort > 65535 {
		return fmt.Errorf("invalid metrics_port: %d (must be 1-65535)", config.MetricsPort)
	}

	if config.HealthPort < 1 || config.HealthPort > 65535 {
		return fmt.Errorf("invalid health_port: %d (must be 1-65535)", config.HealthPort)
	}

	if config.MaxConnections < 1 {
		return fmt.Errorf("invalid max_connections: %d (must be > 0)", config.MaxConnections)
	}

	if _, err := config.GetReadTimeout(); err != nil {
		return fmt.Errorf("invalid read_timeout: %w", err)
	}

	if _, err := config.GetWriteTimeout(); err != nil {
		return fmt.Errorf("invalid write_timeout: %w", err)
	}

	return nil
}

// validateCOS validates COS configuration
func validateCOS(config *COSConfig) error {
	if config.Endpoint == "" {
		return fmt.Errorf("endpoint is required")
	}

	if config.Bucket == "" {
		return fmt.Errorf("bucket is required")
	}

	if config.Region == "" {
		return fmt.Errorf("region is required")
	}

	authType := strings.ToLower(config.AuthType)
	if authType != "iam" && authType != "hmac" {
		return fmt.Errorf("invalid auth_type: %s (must be 'iam' or 'hmac')", config.AuthType)
	}

	// Validate IAM authentication
	if authType == "iam" {
		if config.APIKey == "" {
			return fmt.Errorf("api_key is required for IAM authentication")
		}
	}

	// Validate HMAC authentication
	if authType == "hmac" {
		if config.AccessKey == "" {
			return fmt.Errorf("access_key is required for HMAC authentication")
		}
		if config.SecretKey == "" {
			return fmt.Errorf("secret_key is required for HMAC authentication")
		}
	}

	if config.MaxRetries < 0 {
		return fmt.Errorf("invalid max_retries: %d (must be >= 0)", config.MaxRetries)
	}

	if _, err := config.GetTimeout(); err != nil {
		return fmt.Errorf("invalid timeout: %w", err)
	}

	return nil
}

// validateCache validates cache configuration
func validateCache(config *CacheConfig) error {
	// Validate metadata cache
	if config.Metadata.Enabled {
		if config.Metadata.SizeMB < 1 {
			return fmt.Errorf("invalid metadata cache size_mb: %d (must be > 0)", config.Metadata.SizeMB)
		}
		if config.Metadata.TTLSeconds < 1 {
			return fmt.Errorf("invalid metadata cache ttl_seconds: %d (must be > 0)", config.Metadata.TTLSeconds)
		}
		if config.Metadata.MaxEntries < 1 {
			return fmt.Errorf("invalid metadata cache max_entries: %d (must be > 0)", config.Metadata.MaxEntries)
		}
	}

	// Validate data cache
	if config.Data.Enabled {
		if config.Data.SizeGB < 1 {
			return fmt.Errorf("invalid data cache size_gb: %d (must be > 0)", config.Data.SizeGB)
		}
		if config.Data.Path == "" {
			return fmt.Errorf("data cache path is required")
		}
		if config.Data.ChunkSize < 1 {
			return fmt.Errorf("invalid data cache chunk_size_kb: %d (must be > 0)", config.Data.ChunkSize)
		}

		// Check if cache directory exists or can be created
		if _, err := os.Stat(config.Data.Path); os.IsNotExist(err) {
			if err := os.MkdirAll(config.Data.Path, 0755); err != nil {
				return fmt.Errorf("cannot create cache directory %s: %w", config.Data.Path, err)
			}
		}
	}

	return nil
}

// validatePerformance validates performance configuration
func validatePerformance(config *PerformanceConfig) error {
	if config.ReadAheadKB < 0 {
		return fmt.Errorf("invalid read_ahead_kb: %d (must be >= 0)", config.ReadAheadKB)
	}

	if config.WriteBufferKB < 1 {
		return fmt.Errorf("invalid write_buffer_kb: %d (must be > 0)", config.WriteBufferKB)
	}

	if config.MultipartThresholdMB < 1 {
		return fmt.Errorf("invalid multipart_threshold_mb: %d (must be > 0)", config.MultipartThresholdMB)
	}

	if config.MultipartChunkMB < 1 {
		return fmt.Errorf("invalid multipart_chunk_mb: %d (must be > 0)", config.MultipartChunkMB)
	}

	if config.MultipartChunkMB > config.MultipartThresholdMB {
		return fmt.Errorf("multipart_chunk_mb (%d) cannot be larger than multipart_threshold_mb (%d)",
			config.MultipartChunkMB, config.MultipartThresholdMB)
	}

	if config.WorkerPoolSize < 1 {
		return fmt.Errorf("invalid worker_pool_size: %d (must be > 0)", config.WorkerPoolSize)
	}

	if config.MaxConcurrentReads < 1 {
		return fmt.Errorf("invalid max_concurrent_reads: %d (must be > 0)", config.MaxConcurrentReads)
	}

	if config.MaxConcurrentWrites < 1 {
		return fmt.Errorf("invalid max_concurrent_writes: %d (must be > 0)", config.MaxConcurrentWrites)
	}

	return nil
}

// validateLogging validates logging configuration
func validateLogging(config *LoggingConfig) error {
	level := strings.ToLower(config.Level)
	validLevels := []string{"debug", "info", "warn", "error"}
	valid := false
	for _, l := range validLevels {
		if level == l {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("invalid log level: %s (must be one of: %v)", config.Level, validLevels)
	}

	format := strings.ToLower(config.Format)
	if format != "json" && format != "text" {
		return fmt.Errorf("invalid log format: %s (must be 'json' or 'text')", config.Format)
	}

	if config.Output == "" {
		return fmt.Errorf("log output is required")
	}

	return nil
}

// Made with Bob
