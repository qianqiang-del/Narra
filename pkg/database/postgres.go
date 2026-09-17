package database

import (
	"fmt"
	"time"

	"narra/pkg/config"
	"narra/pkg/logger"

	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

var postgresDB *gorm.DB

// InitPostgres 初始化 PostgreSQL 连接。
func InitPostgres(cfg *config.PostgresConfig) (*gorm.DB, error) {
	dsn := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=disable TimeZone=UTC",
		cfg.Host,
		cfg.Port,
		cfg.Username,
		cfg.Password,
		cfg.Database,
	)

	gormConfig := &gorm.Config{
		// 外键约束由实体关联字段上的 constraint tag 声明、AutoMigrate 建（名字与既有库对象
		// 逐字对齐，存量库 HasConstraint 命中后直接跳过）。不要把这个开关加回来：
		// 加回来新环境就只剩表和列，外键全缺。
		SkipDefaultTransaction: true,
	}

	if config.Get().App.Mode == "debug" {
		gormConfig.Logger = gormlogger.Default.LogMode(gormlogger.Info)
	} else {
		gormConfig.Logger = gormlogger.Default.LogMode(gormlogger.Silent)
	}

	db, err := gorm.Open(postgres.Open(dsn), gormConfig)
	if err != nil {
		logger.Error("PostgreSQL 连接失败", zap.Error(err))
		return nil, fmt.Errorf("PostgreSQL 连接失败: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		logger.Error("获取 SQL 数据库实例失败", zap.Error(err))
		return nil, fmt.Errorf("获取 SQL 数据库实例失败: %w", err)
	}

	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetConnMaxLifetime(time.Hour)

	if err := sqlDB.Ping(); err != nil {
		logger.Error("PostgreSQL Ping 失败", zap.Error(err))
		return nil, fmt.Errorf("PostgreSQL Ping 失败: %w", err)
	}

	postgresDB = db
	logger.Info("PostgreSQL 连接成功",
		zap.String("host", cfg.Host),
		zap.Int("port", cfg.Port),
		zap.String("database", cfg.Database),
	)

	return db, nil
}

// GetPostgres 获取已初始化的 PostgreSQL 实例。
func GetPostgres() *gorm.DB {
	if postgresDB == nil {
		panic("PostgreSQL 未初始化")
	}
	return postgresDB
}

// ClosePostgres 关闭 PostgreSQL 连接。
func ClosePostgres() error {
	if postgresDB == nil {
		return nil
	}

	sqlDB, err := postgresDB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
