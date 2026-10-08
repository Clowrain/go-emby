# .env 中文填写说明

首次部署时，把仓库的 `.env.example` 复制成同目录的 `.env`。授权地址已经填好；小雅解析默认关闭，需要时填写解析入口和可信跳转源。免费模式下授权码可以不填。模板中的注释均为中文。

## 1. 获取配置

```bash
git clone https://github.com/sd87671067/go-emby.git
cd go-emby
[ -e .env ] || (umask 077; cp .env.example .env)
chmod 600 .env
mkdir -p media secrets
nano .env
```

Docker 的 `pull` 命令只下载镜像，不会生成宿主机 `.env`。上面的复制步骤会生成已预填地址的配置；已有配置不会被覆盖。更新仓库后，已有用户可以按需手动修改三个地址，不要覆盖自己的密码。

## 2. 客户需要填写的项目

| 变量 | 填写说明 |
| --- | --- |
| `POSTGRES_PASSWORD` | 必填，数据库密码。建议执行 `openssl rand -hex 24` 生成，只使用十六进制字符，避免数据库连接地址的特殊字符转义问题。 |
| `ADMIN_PASSWORD` | 必填，初始管理员 `admin` 的密码，至少 12 个字符；建议单独生成，与数据库密码不同。 |
| `LICENSE_KEY` | 付费模式填写有效授权码；授权服务开启免费模式时留空即可。 |

例如，免费模式保留 `LICENSE_KEY=`；付费模式填写 `LICENSE_KEY='你的授权码'`。填写时不要把示例文字当作真实密码或授权码。包含 `$`、`#` 或空格的值请使用英文单引号包围。

是否免费由授权服务决定，留空授权码本身不会切换模式，也不会跳过授权校验。免费模式仍需能连接授权服务。

## 3. 授权地址与小雅解析

```dotenv
# 授权服务地址，通常不需要修改
LICENSE_SERVER_URL=https://tl.macacaaca.top
# 小雅解析入口：以下示例仅适用于与小雅同机的 Linux host 网络
XIAOYA_URL=http://127.0.0.1:5678
# 额外可信跳转源：按实际小雅容器 IP 填写，不能包含 /d/ 路径
XIAOYA_REDIRECT_ORIGINS=http://172.17.0.2:45678
```

环境变量区分大小写。Compose 显式传递 `XIAOYA_URL` 和 `XIAOYA_REDIRECT_ORIGINS`，默认均为空。修改 `.env` 后需用相同的 Compose 文件组合执行 `up -d` 重建应用容器；`restart` 不会更新环境变量。NanShare 的 `NANSHARE_URL` 仍是单独的可选配置，当前 Compose 模板不自动注入。

解析器最多请求 5 个可信地址，总超时 40 秒。允许同源相对跳转；跨源解析仅限 `XIAOYA_REDIRECT_ORIGINS` 中列出的完整源（协议、主机、端口）。遇到最终外部地址即返回客户端，不请求 CDN 视频正文。未知内网跳转、循环、超限和上游错误返回 502，不向客户端泄露内部跳转地址。

仅修改解析代码不能解决容器网络隔离：入口和中间跳转地址必须都能从 go-emby 访问。无需修改小雅的同机部署步骤见 [小雅连续跳转与 fork 镜像部署](docs/xiaoya-redirects.md)。

## 4. 启动与访问

默认媒体目录为当前仓库下的 `./media`，将 STRM、NFO、海报等文件放入其中；已有其他媒体目录时，才需要修改 `MEDIA_PATH`。容器内统一使用 `/media`，媒体文件需要允许 UID/GID `65532:65532` 读取。

```bash
docker compose config --quiet
docker compose pull && docker compose up -d
docker compose ps
```

浏览器访问 `http://你的服务器地址:8097`，用户名 `admin`，密码为首次初始化时设置的 `ADMIN_PASSWORD`。配置准备好之后，拉取并启动只需上面的 `docker compose pull && docker compose up -d` 一行命令。

## 5. 其他可选设置

| 变量 | 默认值与说明 |
| --- | --- |
| `IMAGE_TAG` | `latest`，需要固定版本时再修改。 |
| `IMAGE_REPOSITORY` | 默认上游镜像仓库；自行构建 fork 时必须设置为实际构建的镜像名，应用和 schema-prepare 使用相同镜像。 |
| `HTTP_PORT` | `8097`，宿主机端口冲突时修改。 |
| `MEDIA_PATH` | `./media`，宿主机媒体目录，必须提前存在。 |
| `LICENSE_CA_FILE` | 留空，使用系统信任的公共证书；私有 CA 场景将证书放入 `secrets/license-ca.crt`，填写 `/run/secrets/license-ca.crt`。 |
| `PUBLIC_URL` | 留空，按需填写客户端可访问的本服务完整地址。 |
| `PUBLIC_PLAYBACK_URL` | 留空，按需配置对外播放入口。 |
| `BOOTSTRAP_API_KEY` | 留空，按需配置首次初始化的接口密钥。 |
| `PROBE_PLAYBACK_URL` | 留空，按需配置后台媒体探测入口。 |

## 6. 已有部署注意事项

`ADMIN_PASSWORD` 只用于空数据库首次初始化，后续修改管理员密码请使用管理界面。已有 PostgreSQL 数据库不能只修改 `.env` 的 `POSTGRES_PASSWORD`，还需同步修改数据库角色密码。升级时保留 `.env`、媒体、证书和数据卷，不要执行会删除数据卷的 `docker compose down -v`。

不要公开自己的 `.env`。仓库只提供不含客户密码和授权码的模板。本次配置与文档调整不修改程序、播放逻辑、接口或授权机制。
