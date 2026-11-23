package store

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net"
	"regexp"
	"strconv"
	"time"

	gomysql "github.com/go-sql-driver/mysql"
	mysqlDriver "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/imlinhanchao/echo-service/internal/config"
	"github.com/imlinhanchao/echo-service/internal/store/models"
)

var dbNameRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// Open connects to MySQL (creating the database if needed), configures the
// connection pool and runs GORM AutoMigrate for all entities.
func Open(cfg *config.Config) (*gorm.DB, error) {
	if !dbNameRe.MatchString(cfg.MySQL.DB) {
		return nil, fmt.Errorf("非法数据库名 %q（只允许字母、数字、下划线）", cfg.MySQL.DB)
	}
	if err := ensureDatabase(cfg.MySQL); err != nil {
		return nil, err
	}

	dsn := buildDSN(cfg.MySQL, cfg.MySQL.DB)
	var db *gorm.DB
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		db, err = gorm.Open(mysqlDriver.Open(dsn), &gorm.Config{
			Logger:  logger.Default.LogMode(logger.Warn),
			NowFunc: func() time.Time { return time.Now().UTC() },
		})
		if err == nil {
			break
		}
		delay := time.Duration(1<<attempt) * time.Second
		log.Printf("[db] 连接失败（第 %d 次）: %v；%s 后重试", attempt+1, err, delay)
		time.Sleep(delay)
	}
	if err != nil {
		return nil, fmt.Errorf("连接 MySQL 失败（检查 127.0.0.1:3306 与账号权限）: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(20)
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)

	if err := db.AutoMigrate(
		&models.User{},
		&models.Session{},
		&models.ProviderAccount{},
		&models.Book{},
		&models.Job{},
		&models.Audiobook{},
		&models.UserSettings{},
		&models.APIToken{},
		&models.UserProgress{},
		&models.UserNote{},
	); err != nil {
		return nil, fmt.Errorf("AutoMigrate 失败: %w", err)
	}
	return db, nil
}

func ensureDatabase(c config.MySQLConfig) error {
	db, err := sql.Open("mysql", buildDSN(c, ""))
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 先检查库是否已存在：最小权限账号（例如 Docker Compose 创建的、只被授权到
	// 具体库的用户）没有全局 CREATE 权限，但对已存在的库不需要建库。
	var name string
	err = db.QueryRowContext(ctx,
		"SELECT SCHEMA_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = ?", c.DB,
	).Scan(&name)
	if err == nil {
		return nil
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("检查数据库 %s 是否存在失败: %w", c.DB, err)
	}
	stmt := fmt.Sprintf(
		"CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci",
		c.DB,
	)
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("数据库 %s 不存在且创建失败（请检查账号是否有 CREATE 权限，或预先建库）: %w", c.DB, err)
	}
	return nil
}

func buildDSN(c config.MySQLConfig, dbName string) string {
	cfg := gomysql.NewConfig()
	cfg.User = c.User
	cfg.Passwd = c.Password
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	cfg.DBName = dbName
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.Params = map[string]string{"charset": "utf8mb4"}
	return cfg.FormatDSN()
}
