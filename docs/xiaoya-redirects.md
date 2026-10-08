# 不修改小雅的多级重定向解析

此文适用于本 fork 的源码构建。上游发布镜像可能来自另一份源码；仅修改环境变量不会把本 fork 的功能加入旧镜像。

## 播放行为

当 STRM 指向 `xiaoya.host:5678` 等已识别小雅地址时，go-emby 使用 `XIAOYA_URL` 作为服务端入口，保留文件路径与查询参数（去除占位 `sign=SIGN_STR`）。

例如，小雅未修改时的真实链路可以是：

```text
go-emby → http://127.0.0.1:5678/d/影片
        → http://172.17.0.2:45678/d/影片
        → https://云盘CDN/影片?签名
VidHub  ← 302，Location 直接为 CDN 地址
```

仅向可信解析源发请求；CDN 地址返回给播放器，服务器不下载视频。每一跳使用播放器的 User-Agent 与 X-Alist-OriUA，不转发 Emby 令牌、Authorization、Cookie 或 Referer。相对 Location 按当前请求地址解析，跳转中的签名查询参数保持原样。

默认仅信任配置的入口源。额外可信源通过 `XIAOYA_REDIRECT_ORIGINS` 明确列出，逗号分隔，只接受 `http(s)://主机:端口`，不接受路径、账号、查询参数或片段。不要将其他内网管理服务加入列表。该列表仅用于小雅 STRM，不扩大 NanShare 的权限。

最多请求 5 个解析地址，整条链共用 40 秒上限并响应客户端取消。未知内网跳转、循环和解析失败均返回 502；不会回退成客户端无法访问的小雅内部地址。

## 同机 Linux / NAS 部署

桥接网络下，能访问宿主机发布的 5678 并不代表能访问小雅返回的 Docker 内部 IP。已确认宿主机可访问原始小雅入口和 45678 时，可以只把 go-emby 改为 host 网络。PostgreSQL 继续使用桥接网络，其宿主机端口只绑定 127.0.0.1；使用仓库已有的 `compose.host.yaml` 连接它。host 模式共享宿主机网络，应用实际监听端口由 `LISTEN` 决定（默认 8097），不是 `HTTP_PORT`。

先确认小雅原始入口返回的内部地址，不要猜 IP。示例 `172.17.0.2` 不是固定保证，容器重建后可能变化，届时必须更新可信源列表并重建 go-emby。不要为方便而放开整个内网。

在部署目录现有 `.env` 中合并以下设置，保留已有密码、目录、授权与其他配置：

```dotenv
IMAGE_REPOSITORY=go-emby-xiaoya
IMAGE_TAG=local
NETWORK_MODE=host
XIAOYA_URL=http://127.0.0.1:5678
XIAOYA_REDIRECT_ORIGINS=http://172.17.0.2:45678
```

从本 fork 源码根目录构建（在目标架构的机器构建，或使用支持对应平台的 buildx）：

```sh
docker build -t go-emby-xiaoya:local .
```

将修改后的 `compose.yaml` 和 `compose.host.yaml` 与已有部署配置审慎合并。应用和 schema-prepare 必须引用同一 fork 镜像。Compose 默认仍指向上游镜像，不设置 `IMAGE_REPOSITORY` 会启动原来的程序。

使用生产数据库前，先在独立测试部署和数据库副本上验证版本兼容性，不能把“同名仓库”视为 schema 和功能完全相同。`schema-prepare` 会执行数据库迁移；生产部署前应保存完整可恢复的数据库备份及原有镜像标识。

确认使用独立测试数据后，在测试部署目录执行：

```sh
docker compose -f compose.yaml -f compose.host.yaml config --quiet
docker compose -f compose.yaml -f compose.host.yaml up -d
```

选择其他测试监听端口时，在测试 override 中设置 `services.go-emby.environment.LISTEN`，并为测试 PostgreSQL 设置不同的 `POSTGRES_HOST_PORT`。测试使用独立的数据库/应用数据目录，避免与线上服务竞争端口或共享数据。

## 验收与切换

1. 检查容器实际环境变量包含解析入口和可信源列表，并确认运行的是 fork 镜像。
2. 使用有效普通用户的播放请求测试（不是绕过用户播放逻辑的 API key），302 的 Location 应直接指向 CDN。
3. 用相同 User-Agent 验证该 CDN 地址的 Range 请求返回 206，再用 VidHub 重新播放。
4. 确认该链路使用的是小雅原始入口后，才撤销之前临时增加的小雅直链配置；验证原 Emby 与 go-emby 均可播放。

本 fork 的解析不依赖小雅新增监听端口、Nginx 修改或 `go-emby_default` 网络别名。小雅保持其原始配置即可。

## 只验证解析器，不启动新服务

可在能访问小雅原始入口和内部地址的 Linux 主机上，显式设置 `XIAOYA_TEST_SOURCE` 为一个真实小雅 STRM URL，连同上述 `XIAOYA_URL` 和 `XIAOYA_REDIRECT_ORIGINS` 运行：

```sh
go test -run '^TestXiaoyaLiveResolver$' -v -count=1 .
```

未设置时该测试跳过。它不连接数据库、不启动 go-emby 服务、不下载视频，只输出最终 CDN 的源地址，不输出签名参数。生产切换仍需验证完整服务与数据库兼容性。
