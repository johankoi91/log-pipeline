-- Kafka 源表（拉流不落盘）
CREATE TABLE IF NOT EXISTS logs.src_kafka_app_logs
(
  ts        String,
  level     String,
  env       String,
  app       String,
  host      String,
  code      String,
  message   String,
  attrs     String,

  _topic     String,
  _offset    UInt64,
  _partition UInt64,
  _timestamp DateTime
)
ENGINE = Kafka
SETTINGS
  kafka_broker_list = 'kafka:9092',
  kafka_topic_list  = 'app_logs.prod',
  kafka_group_name  = 'ch_app_logs_g1',
  kafka_format      = 'JSONEachRow',
  kafka_num_consumers = 6,
  kafka_max_block_size = 65536,
  input_format_allow_errors_num = 100,
  input_format_allow_errors_ratio = 0.05;

-- 物化视图：类型转换 + 去重 token + 入事实表
CREATE MATERIALIZED VIEW IF NOT EXISTS logs.mv_app_logs_consume
TO logs.fact_app_log
AS
SELECT
  coalesce(parseDateTimeBestEffortOrNull(ts), _timestamp) AS ts,
  level, env, app, host,
  toInt32OrZero(code) AS code,
  message,
  tryParseJson(attrs) AS attrs,
  concat(toString(_partition), '-', toString(_offset)) AS dedup_token,
  now64(3) AS ingest_time
FROM logs.src_kafka_app_logs
WHERE ts IS NOT NULL OR _timestamp IS NOT NULL;
