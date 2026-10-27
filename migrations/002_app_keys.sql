-- App Key 由内部后台生成或导入；注册、上报带的 app_key 必须存在且已启用。
-- 已部署的库(数据卷不是空的)不会自动执行本文件，按部署说明手动执行一次。
CREATE TABLE IF NOT EXISTS app_keys (
  id          BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  app_key     VARCHAR(64)  COLLATE utf8mb4_bin NOT NULL,      -- 区分大小写
  remark      VARCHAR(255) NOT NULL DEFAULT '',
  status      VARCHAR(16)  NOT NULL DEFAULT 'enabled',       -- enabled / disabled
  created_by  VARCHAR(64)  NOT NULL DEFAULT '',              -- 创建人：后台Token的name
  created_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uk_app_key (app_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
