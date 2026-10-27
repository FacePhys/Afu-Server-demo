-- 每份上报相对上一次已确认值的增量：计次、有效帧数、时长(毫秒)。后台按天统计用它，
-- 不再用 measurements 求和：一条 measurement 现在是一次计次，只带到计次那一刻的数据。
-- 重复上报不入库，换号那份上报是基线，增量都是0。
-- 已部署的库不会自动执行本文件，按部署说明手动执行一次。
ALTER TABLE reports
  ADD COLUMN delta_sessions BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '新增计次',
  ADD COLUMN delta_frames   BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '新增有效帧数',
  ADD COLUMN delta_dt_ms    BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '新增时长(毫秒)';

-- 升级前的上报按当时的 measurements 补算(那时一条 measurement 是一次完整测量)，按天统计和升级前一致。
-- 只补增量全为0、带 measurements 的行。
UPDATE reports r
JOIN (SELECT install_id, seq, COUNT(*) AS n, SUM(frames) AS frames, SUM(dt_ms) AS dt_ms
      FROM measurements GROUP BY install_id, seq) m ON m.install_id = r.install_id AND m.seq = r.seq
SET r.delta_sessions = m.n, r.delta_frames = m.frames, r.delta_dt_ms = m.dt_ms
WHERE r.delta_sessions = 0 AND r.delta_frames = 0 AND r.delta_dt_ms = 0;
