package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadUsesNestedEnvironmentOverrides(t *testing.T) {
	t.Setenv("NFS_GATEWAY_COS_API_KEY", "env-api-key")
	t.Setenv("NFS_GATEWAY_COS_BUCKET", "env-bucket")
	t.Setenv("NFS_GATEWAY_CACHE_DATA_ENABLED", "false")
	t.Setenv("NFS_GATEWAY_PERFORMANCE_MAX_FULL_OBJECT_READ_MB", "64")
	t.Setenv("NFS_GATEWAY_PERFORMANCE_MAX_BUFFERED_WRITE_MB", "128")
	t.Setenv("NFS_GATEWAY_PERFORMANCE_MAX_DIRECTORY_ENTRIES", "500")

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	configData := []byte(`
server:
  nfs_port: 2049
  metrics_port: 8080
  health_port: 8081
  max_connections: 1000
  read_timeout: "30s"
  write_timeout: "30s"
cos:
  endpoint: "s3.us-south.cloud-object-storage.appdomain.cloud"
  bucket: "file-bucket"
  region: "us-south"
  auth_type: "iam"
  max_retries: 3
  timeout: "30s"
cache:
  metadata:
    enabled: false
  data:
    enabled: true
    size_gb: 1
    path: "/should-not-be-created"
    chunk_size_kb: 1024
performance:
  read_ahead_kb: 1024
  write_buffer_kb: 4096
  multipart_threshold_mb: 100
  multipart_chunk_mb: 10
  worker_pool_size: 100
  max_concurrent_reads: 50
  max_concurrent_writes: 25
  max_full_object_read_mb: 512
  max_buffered_write_mb: 512
  max_directory_entries: 100000
logging:
  level: "info"
  format: "json"
  output: "stdout"
staging:
  enabled: false
`)

	if err := os.WriteFile(configPath, configData, 0600); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.COS.APIKey != "env-api-key" {
		t.Fatalf("COS.APIKey = %q, want env-api-key", cfg.COS.APIKey)
	}
	if cfg.COS.Bucket != "env-bucket" {
		t.Fatalf("COS.Bucket = %q, want env-bucket", cfg.COS.Bucket)
	}
	if cfg.Cache.Data.Enabled {
		t.Fatalf("Cache.Data.Enabled = true, want false from env override")
	}
	if cfg.Performance.MaxFullObjectReadMB != 64 {
		t.Fatalf("Performance.MaxFullObjectReadMB = %d, want 64", cfg.Performance.MaxFullObjectReadMB)
	}
	if cfg.Performance.MaxBufferedWriteMB != 128 {
		t.Fatalf("Performance.MaxBufferedWriteMB = %d, want 128", cfg.Performance.MaxBufferedWriteMB)
	}
	if cfg.Performance.MaxDirectoryEntries != 500 {
		t.Fatalf("Performance.MaxDirectoryEntries = %d, want 500", cfg.Performance.MaxDirectoryEntries)
	}
}
