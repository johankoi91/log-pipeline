CREATE DATABASE IF NOT EXISTS logs;

CREATE TABLE IF NOT EXISTS logs.fact_app_log
(
  ts           DateTime64(3, 'Asia/Shanghai'),
  date         Date MATERIALIZED toDate(ts),

  level        LowCardinality(String),
  env          LowCardinality(String),
  app          LowCardinality(String),
  host         LowCardinality(String),

  code         Int32,
  message      String,
  attrs        JSON,

  dedup_token  String,   -- 去重用：partition-offset
  ingest_time  DateTime64(3, 'Asia/Shanghai') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(dedup_token)
PARTITION BY toYYYYMMDD(ts)
ORDER BY (date, app, host, ts)
TTL ts + INTERVAL 14 DAY DELETE
SETTINGS index_granularity = 8192;

-- 可选：信息检索加速（子串/分词类 Bloom 索引）
ALTER TABLE logs.fact_app_log
  ADD INDEX IF NOT EXISTS ix_msg_token
  message TYPE tokenbf_v1(16384, 3, 0) GRANULARITY 4;

ALTER TABLE logs.fact_app_log
  ADD INDEX IF NOT EXISTS ix_msg_ngram
  message TYPE ngrambf_v1(3, 16384, 3, 0) GRANULARITY 4;
