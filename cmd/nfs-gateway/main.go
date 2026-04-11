package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/oborges/cos-nfs-gateway/internal/cache"
	"github.com/oborges/cos-nfs-gateway/internal/config"
	"github.com/oborges/cos-nfs-gateway/internal/cos"
	"github.com/oborges/cos-nfs-gateway/internal/feature"
	"github.com/oborges/cos-nfs-gateway/internal/health"
	"github.com/oborges/cos-nfs-gateway/internal/lock"
	"github.com/oborges/cos-nfs-gateway/internal/logging"
	"github.com/oborges/cos-nfs-gateway/internal/metrics"
	"github.com/oborges/cos-nfs-gateway/internal/nfs"
	"github.com/oborges/cos-nfs-gateway/internal/posix"
	"github.com/oborges/cos-nfs-gateway/internal/staging"
	nfshelper "github.com/willscott/go-nfs/helpers"
	"go.uber.org/zap"
)

var (
	// Version is set during build
	Version = "dev"

	// Command line flags
	configPath = flag.String("config", "", "Path to configuration file")
	version    = flag.Bool("version", false, "Print version and exit")
)

func main() {
	flag.Parse()

	// Print version and exit
	if *version {
		fmt.Printf("IBM Cloud COS NFS Gateway v%s\n", Version)
		os.Exit(0)
	}

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// Initialize logging
	logConfig := logging.Config{
		Level:  cfg.Logging.Level,
		Format: cfg.Logging.Format,
		Output: cfg.Logging.Output,
	}
	if err := logging.Initialize(logConfig); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logging: %v\n", err)
		os.Exit(1)
	}
	defer logging.Sync()

	logging.Info("Starting IBM Cloud COS NFS Gateway",
		zap.String("version", Version),
	)

	// Log effective configuration
	logging.Info("Configuration loaded",
		zap.Int("write_buffer_kb", cfg.Performance.WriteBufferKB),
		zap.Int("write_buffer_bytes", cfg.Performance.WriteBufferKB*1024),
		zap.Int("multipart_threshold_mb", cfg.Performance.MultipartThresholdMB),
		zap.Int("multipart_chunk_mb", cfg.Performance.MultipartChunkMB),
		zap.Int("read_ahead_kb", cfg.Performance.ReadAheadKB),
		zap.Bool("data_cache_enabled", cfg.Cache.Data.Enabled),
		zap.Bool("metadata_cache_enabled", cfg.Cache.Metadata.Enabled),
		zap.Int("data_cache_size_gb", cfg.Cache.Data.SizeGB),
		zap.Int("metadata_cache_size_mb", cfg.Cache.Metadata.SizeMB),
		zap.String("log_level", cfg.Logging.Level),
	)

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = ctx // Used for future context-aware operations

	// Initialize COS client
	cosClient, err := cos.NewClient(&cfg.COS)
	if err != nil {
		logging.Fatal("Failed to initialize COS client", zap.Error(err))
	}
	defer cosClient.Close()

	logging.Info("COS client initialized successfully")

	// Initialize caches
	metadataCache := cache.NewMetadataCache(&cfg.Cache.Metadata)
	dataCache, err := cache.NewDataCache(&cfg.Cache.Data)
	if err != nil {
		logging.Fatal("Failed to initialize data cache", zap.Error(err))
	}

	logging.Info("Caches initialized successfully")

	// Initialize POSIX operations handler
	operations := posix.NewOperationsHandler(cosClient, metadataCache, dataCache)

	// Initialize lock manager
	lockManager := lock.NewManager(5 * time.Minute)
	defer lockManager.Close()

	// Initialize metrics
	metrics.Initialize()
	if err := metrics.StartMetricsServer(cfg.Server.MetricsPort); err != nil {
		logging.Error("Failed to start metrics server", zap.Error(err))
	}

	// Initialize health checks
	healthChecker := health.NewChecker()
	healthChecker.RegisterCheck("cos", health.COSHealthCheck(func(ctx context.Context) error {
		_, err := cosClient.ObjectExists(ctx, ".health")
		return err
	}))
	healthChecker.RegisterCheck("cache", health.CacheHealthCheck(
		metadataCache.IsEnabled,
		func() interface{} { return metadataCache.Stats() },
	))

	if err := health.StartHealthServer(cfg.Server.HealthPort, healthChecker); err != nil {
		logging.Error("Failed to start health server", zap.Error(err))
	}

	// Initialize feature flags
	featureFlags := feature.LoadFeatureFlags(cfg)
	logging.Info("Feature flags loaded",
		zap.Bool("staging_enabled", featureFlags.IsStagingEnabled()))

	// Initialize staging components if enabled
	var stagingManager *staging.StagingManager
	var syncWorker *staging.SyncWorker
	
	if featureFlags.IsStagingEnabled() {
		logging.Info("Initializing staging architecture",
			zap.String("root_dir", cfg.Staging.RootDir),
			zap.Int64("sync_threshold_mb", cfg.Staging.SyncThresholdMB),
			zap.Int("sync_worker_count", cfg.Staging.SyncWorkerCount))
		
		// Create staging manager
		stagingManager, err = staging.NewStagingManager(&cfg.Staging)
		if err != nil {
			logging.Fatal("Failed to initialize staging manager", zap.Error(err))
		}
		defer stagingManager.Shutdown()
		
		// Create COS client adapter for sync worker
		cosClientAdapter := &staging.COSClientAdapter{Client: cosClient}
		
		// Create sync worker
		syncWorker = staging.NewSyncWorker(stagingManager, cosClientAdapter, &cfg.Staging)
		
		// Start sync worker
		syncWorker.Start()
		defer syncWorker.Stop()
		
		logging.Info("Staging architecture initialized successfully")
	}

	// Initialize NFS filesystem and server
	zapLogger := logging.GetLogger()
	nfsLogger := nfs.NewLogger(zapLogger)
	
	// Create billy.Filesystem implementation with config
	cosFilesystem := nfs.NewCOSFilesystemWithConfig(operations, nfsLogger, "/", &cfg.Performance, stagingManager, syncWorker, featureFlags)
	
	// Wrap with null auth handler, then caching handler
	authHandler := nfshelper.NewNullAuthHandler(cosFilesystem)
	cachedHandler := nfshelper.NewCachingHandler(authHandler, 1000)
	
	nfsAddress := fmt.Sprintf(":%d", cfg.Server.NFSPort)
	nfsServer, err := nfs.NewServer(cachedHandler, nfsAddress, nfsLogger)
	if err != nil {
		logging.Fatal("Failed to create NFS server", zap.Error(err))
	}

	if err := nfsServer.Start(); err != nil {
		logging.Fatal("Failed to start NFS server", zap.Error(err))
	}
	defer nfsServer.Stop()

	logging.Info("NFS Gateway started successfully",
		zap.Int("nfs_port", cfg.Server.NFSPort),
		zap.Int("metrics_port", cfg.Server.MetricsPort),
		zap.Int("health_port", cfg.Server.HealthPort),
	)

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	<-sigChan
	logging.Info("Received shutdown signal, shutting down gracefully...")

	// Shutdown NFS server
	if err := nfsServer.Stop(); err != nil {
		logging.Error("Error stopping NFS server", zap.Error(err))
	}

	// Close lock manager
	if err := lockManager.Close(); err != nil {
		logging.Error("Error closing lock manager", zap.Error(err))
	}

	// Clear caches
	metadataCache.Clear()
	if err := dataCache.Clear(); err != nil {
		logging.Error("Error clearing data cache", zap.Error(err))
	}

	// Close COS client
	if err := cosClient.Close(); err != nil {
		logging.Error("Error closing COS client", zap.Error(err))
	}

	logging.Info("Shutdown complete")
}

// Made with Bob
