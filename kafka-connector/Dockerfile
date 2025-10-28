# =======================
# 2) Go 后端构建（go-pipeline-server）
# =======================
FROM docker.m.daocloud.io/library/golang:1.24.8 AS go-build
WORKDIR /goapp
COPY go-pipeline-server/ /goapp/go-pipeline-server/
WORKDIR /goapp/go-pipeline-server

# 如有私有依赖可设置 GOPRIVATE
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/admin


# =======================
# 3) 最终镜像：cp-kafka-connect + Go + 前端静态
# =======================
FROM docker.m.daocloud.io/confluentinc/cp-kafka-connect:7.6.1

# 安装 ES Sink 插件（与原来一致）
RUN confluent-hub install --no-prompt confluentinc/kafka-connect-elasticsearch:15.0.1

USER root
WORKDIR /app

# 放入 Go 二进制与前端静态文件
COPY --from=go-build /out/admin /usr/local/bin/admin
COPY log-adm/dist /app/static

# Kafka Connect 插件目录
ENV CONNECT_PLUGIN_PATH="/usr/share/java,/usr/share/confluent-hub-components"

# Go 监听 80；Kafka Connect 在容器内 8083（供后端通过 http://127.0.0.1:8083 调用）
EXPOSE 8801

# 可选健康检查（后端应提供 /admin/health 或替换为你的健康检查路径）
HEALTHCHECK --interval=30s --timeout=3s --start-period=20s --retries=5 \
  CMD wget -qO- http://127.0.0.1/admin/verify/pipeline || exit 1

# 同时启动：Kafka Connect（后台）+ Go 后端（前台，提供静态与 /admin/*）
# main.go 已支持 --listen 与 --static-dir；工作目录 /app 下有 config.yaml
CMD ["bash","-lc", "\
  /etc/confluent/docker/run & \
  LISTEN=:8801 STATIC_DIR=/app/static /usr/local/bin/admin \
"]
