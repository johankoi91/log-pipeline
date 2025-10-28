import React, { useEffect, useState } from "react";
import { Button, Space, Card, Typography, message, Divider } from "antd";
import axios from "axios";
import { ClientConfig, useClientConfigReady, resolveUrl } from "../ClientConfig";

const { Text } = Typography;
const MAX_LOGS = 300; // 最多保留 300 条


const logOk = (label, status, data) =>
  `✅ ${label} (${status})\n${JSON.stringify(data, null, 2)}`;
const logErr = (label, err) =>
  `❌ ${label}\n${JSON.stringify(err?.response?.data || err?.message, null, 2)}`;

async function req(method, path, label, setLogs) {
  try {
    let url = resolveUrl(ClientConfig.value.api_base_url, path)
    const { data, status } = await axios({ method, url });
    setLogs(prev => [logOk(label, status, data), ...prev].slice(0, MAX_LOGS));
  } catch (e) {
    setLogs(prev => [logErr(label, e), ...prev].slice(0, MAX_LOGS));
    message.error(`${label} 失败`);
  }
}

export default function AdminActions() {
  const [logs, setLogs] = useState([]);
  const [showInitCard, setShowInitCard] = useState(true); // 新增：控制初始化卡片显隐
  const { ready, error, cfg } = useClientConfigReady();

  // 页面初始化时静默探测 connector 状态
  useEffect(() => {
    if (ready && cfg?.api_base_url) {
      (async () => {
        try {
          message.success({key:'cfg-ready', content:`${cfg.api_base_url} useClientConfigReady`});
          const res = await fetch(`${cfg.api_base_url}/admin/verify/sink-status`, {
            method: "GET",
          });
          if (!res.ok) { // 非 2xx：认为未就绪 → 显示初始化卡片
            setShowInitCard(true);
            return;
          }
          const json = await res.json().catch(() => ({}));
          const state = json?.data?.connector?.state;
          if (state === "RUNNING") { // 满足“RUNNING”即隐藏初始化卡片；其余状态均显示
            setShowInitCard(false);
          } else {
            setShowInitCard(true);
          }
        } catch (e) { // 网络/解析异常：保守显示卡片，便于手动初始化
          setShowInitCard(true);
        }
      })();
  }
  }, [ready, cfg?.api_base_url]);


  return (
    <Space direction="vertical" size="large" style={{ width: "100%", maxWidth: 1100 }}>
      {/* 初始化 / 更新 卡片：按探测结果决定是否渲染 */}
      {showInitCard && (
        <Card title="初始化 / 更新（Elasticsearch & Kafka Connect）">
          <Space wrap>
            <Button onClick={() => req("post", "/admin/es/pipeline", "PUT Ingest Pipeline", setLogs)}>
              PUT _ingest/pipeline
            </Button>
            <Button onClick={() => req("post", "/admin/es/ilm", "PUT ILM Policy", setLogs)}>
              PUT _ilm/policy
            </Button>
            <Button onClick={() => req("post", "/admin/es/template", "PUT Index Template", setLogs)}>
              PUT _index_template
            </Button>
            <Button onClick={() => req("post", "/admin/es/data-stream", "Create Data Stream", setLogs)}>
              PUT _data_stream
            </Button>
            <Button onClick={() => req("post", "/admin/connect/sink", "Register ES Sink Connector", setLogs)}>
              Register connectors sink
            </Button>
          </Space>
        </Card>
      )}

      {/* 验证查看 */}
      <Card title="验证 / 查看（Elasticsearch）">
        <Space wrap>
          <Button onClick={() => req("get", "/admin/query/data-streams", "ILM Explain (data stream)", setLogs)}>
           query data-streams
          </Button>
          <Button onClick={() => req("get", "/admin/verify/ilm-explain", "ILM Explain (data stream)", setLogs)}>
            _ilm/explain info
          </Button>
          <Button onClick={() => req("get", "/admin/verify/template", "Get Index Template", setLogs)}>
            查看 _index_template
          </Button>
          <Button onClick={() => req("get", "/admin/verify/pipeline", "Get Ingest Pipeline", setLogs)}>
            查看 _ingest/pipeline
          </Button>
        </Space>
      </Card>

      {/* 维护操作 */}
      <Card title="常用维护（Kafka Connect）">
        <Space wrap>
           <Button onClick={() => req("get", "/admin/verify/sink-status", "Connector Status", setLogs)}>
            查看 Connectors Status
          </Button>
          <Button onClick={() => req("get", "/admin/connect/config", "Get Connector Config", setLogs)}>
            查看 Connectors 配置
          </Button>
          {/* <Button onClick={() => req("put", "/admin/connect/pause", "Pause Connector", setLogs)}>
            暂停
          </Button>
          <Button onClick={() => req("put", "/admin/connect/resume", "Resume Connector", setLogs)}>
            恢复
          </Button> */}
          {/* <Button danger onClick={() => req("delete", "/admin/connect/delete", "删除 Connector", setLogs)}>
            删除（谨慎）
          </Button> */}
        </Space>
      </Card>

      {/* 返回日志（新增清除按钮） */}
      <Card
        size="small"
        title="返回日志（最新在上）"
        extra={
          <Space>
            <Button size="small" onClick={() => setLogs([])}>
              清除
            </Button>
          </Space>
        }
      >
        <Space direction="vertical" style={{ width: "100%" }}>
          {logs.map((line, idx) => (
            <pre
              key={idx}
              style={{
                margin: 0,
                whiteSpace: "pre-wrap",
                wordBreak: "break-word",
                maxHeight: 360,          // 单条过长时自动折叠滚动
                overflowY: "auto",
                background: "#fafafa",
                padding: 8,
                borderRadius: 6,
              }}
            >
              <Text>{line}</Text>
            </pre>
          ))}
        </Space>
      </Card>
    </Space>
  );
}
