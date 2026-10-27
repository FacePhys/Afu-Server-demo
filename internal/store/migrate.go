package store

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

// 重复执行迁移时可以忽略的MySQL错误：表、列、索引已存在。
var alreadyApplied = map[uint16]bool{1050: true, 1060: true, 1061: true}

// ApplyMigrations 按文件名顺序执行目录下的 *.sql，重复执行时跳过已存在的表、列、索引。
// 给集成测试建表用；生产库由 MySQL 首次启动或按部署说明手动执行。
func ApplyMigrations(db *gorm.DB, dir string) error {
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		for _, stmt := range strings.Split(string(b), ";\n") {
			if !hasSQL(stmt) {
				continue
			}
			var me *mysql.MySQLError
			if err := db.Exec(stmt).Error; err != nil && !(errors.As(err, &me) && alreadyApplied[me.Number]) {
				return err
			}
		}
	}
	return nil
}

// hasSQL 去掉 -- 注释和空行后还有内容。
func hasSQL(stmt string) bool {
	for _, line := range strings.Split(stmt, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "--") {
			return true
		}
	}
	return false
}
