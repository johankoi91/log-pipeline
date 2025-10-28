-- 分区键 = host:app，保证“同机同服务”严格顺序（分区内有序）
function make_key(tag, ts, record)
  local host = record["host"] or "unknown-host"
  local app  = record["app"]  or "unknown-app"
  record["key"] = host .. ":" .. app
  return 1, ts, record
end


