package store

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gorm.io/gorm"
)

// ApplyMigrations 按文件名顺序执行目录下的 *.sql，只执行 CREATE TABLE 语句(都带 IF NOT EXISTS，可重复执行)。
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
			if strings.Contains(stmt, "CREATE TABLE") {
				if err := db.Exec(stmt).Error; err != nil {
					return err
				}
			}
		}
	}
	return nil
}
