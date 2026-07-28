// Package database contains shared database clients.
package database

import (
	"code-agent/pkg/log"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// DB stores the shared MySQL connection.
var DB *gorm.DB

// InitMySQL initializes the MySQL database connection.
func InitMySQL(dsn string) {
	var err error
	DB, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal("failed to connect database", err)
	}

	sqlDB, err := DB.DB()
	if err != nil {
		log.Fatal("failed to get sql.DB", err)
	}

	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(100)
	sqlDB.SetConnMaxLifetime(time.Hour)

	log.Info("MySQL database connected successfully")
}
